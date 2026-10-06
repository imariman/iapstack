import Foundation
import XCTest

@testable import IAPStackApple

final class SessionLoaderTests: XCTestCase {
  private let endpoint = URL(string: "https://host.example/session")!

  override func setUp() {
    SessionStubProtocol.reset()
  }

  func testRequestUsesLoginBearerAndDecodesSession() async throws {
    SessionStubProtocol.enqueue(status: 200, body: sessionJSON(expiry: "2099-01-01T00:00:00.123Z"))
    let session = try await loader().load(endpoint: endpoint, loginToken: "temporary-login")
    XCTAssertEqual(session.baseURL, URL(string: "https://iap.example")!)
    XCTAssertEqual(session.applicationId, "app")
    XCTAssertEqual(session.externalCustomerId, "customer-1")
    XCTAssertEqual(session.customerToken, "short-lived")
    XCTAssertEqual(session.config.customerToken, "short-lived")
    XCTAssertFalse(session.description.contains("short-lived"))
    XCTAssertFalse(session.description.contains("customer-1"))
    let request = try XCTUnwrap(SessionStubProtocol.requests.first)
    XCTAssertEqual(request.httpMethod, "POST")
    XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer temporary-login")
    XCTAssertEqual(request.httpBody, Data("{}".utf8))
  }

  func testUnsafeRequestsFailBeforeSendingTheLoginBearer() async {
    for unsafe in ["http://host.example/session", "https://name:pass@host.example/session",
                   "https://host.example/session?token=bad", "https://host.example/session#fragment"] {
      await assertFails(.invalidEndpoint) { try await self.loader().load(endpoint: URL(string: unsafe)!, loginToken: "login") }
    }
    for token in ["", "two words", "line\nbreak"] {
      await assertFails(.invalidLoginToken) { try await self.loader().load(endpoint: self.endpoint, loginToken: token) }
    }
    XCTAssertEqual(IAPStackSessionError.invalidEndpoint.code, "invalid_session_request")
    XCTAssertTrue(SessionStubProtocol.requests.isEmpty)
  }

  func testUnusableSessionsAreRejected() async {
    let cases: [(String, IAPStackSessionError)] = [
      (sessionJSON(baseURL: "http://insecure.example"), .invalidResponse),
      (sessionJSON(customer: " customer-1"), .invalidResponse),
      (sessionJSON(token: ""), .invalidResponse),
      (sessionJSON(expiry: "tomorrow"), .invalidResponse),
      (#"{"token":123}"#, .invalidResponse),
      (sessionJSON(expiry: "2000-01-01T00:00:00Z"), .expired),
    ]
    for (body, expected) in cases {
      SessionStubProtocol.enqueue(status: 200, body: body)
      await assertFails(expected) { try await self.loader().load(endpoint: self.endpoint, loginToken: "login") }
    }
  }

  func testOversizedAndUnsuccessfulResponsesAreRejected() async {
    SessionStubProtocol.enqueue(status: 200, body: String(repeating: " ", count: IAPStackSessionLoader.maxResponseBytes + 1))
    await assertFails(.invalidResponse) { try await self.loader().load(endpoint: self.endpoint, loginToken: "login") }
    SessionStubProtocol.enqueue(status: 401, body: "{}")
    await assertFails(.unavailable) { try await self.loader().load(endpoint: self.endpoint, loginToken: "login") }
  }

  func testRedirectIsNotFollowedWithTheLoginBearer() async {
    SessionStubProtocol.enqueueRedirect(to: URL(string: "https://elsewhere.example/collect")!)
    SessionStubProtocol.enqueue(status: 200, body: sessionJSON())
    await assertFails(.unavailable) { try await self.loader().load(endpoint: self.endpoint, loginToken: "login") }
    XCTAssertEqual(SessionStubProtocol.requests.map(\.url?.host), ["host.example"])
  }

  private func loader() -> IAPStackSessionLoader {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.protocolClasses = [SessionStubProtocol.self]
    return IAPStackSessionLoader(session: URLSession(configuration: configuration))
  }

  private func assertFails(_ expected: IAPStackSessionError, file: StaticString = #filePath, line: UInt = #line,
                           _ action: () async throws -> IAPStackCustomerSession) async {
    do {
      _ = try await action()
      XCTFail("Expected \(expected)", file: file, line: line)
    } catch {
      // Never print the raw response: it would contain a bearer in real use.
      XCTAssertEqual(error as? IAPStackSessionError, expected, file: file, line: line)
    }
  }

  private func sessionJSON(baseURL: String = "https://iap.example", customer: String = "customer-1",
                           token: String = "short-lived", expiry: String = "2099-01-01T00:00:00Z") -> String {
    """
    {"base_url":"\(baseURL)","application_id":"app","external_customer_id":"\(customer)","token":"\(token)","expires_at":"\(expiry)"}
    """
  }
}

/// Answers queued steps; a redirect step goes through URLSession's task-delegate redirect check.
private final class SessionStubProtocol: URLProtocol {
  private enum Step {
    case respond(Int, Data)
    case redirect(URL)
  }

  private static let lock = NSLock()
  private static var steps: [Step] = []
  private static var recorded: [URLRequest] = []
  static var requests: [URLRequest] { lock.withLock { recorded } }

  static func reset() { lock.withLock { steps = []; recorded = [] } }
  static func enqueue(status: Int, body: String) { lock.withLock { steps.append(.respond(status, Data(body.utf8))) } }
  static func enqueueRedirect(to url: URL) { lock.withLock { steps.append(.redirect(url)) } }

  override class func canInit(with request: URLRequest) -> Bool { true }
  override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

  override func startLoading() {
    let step: Step? = Self.lock.withLock {
      var copy = request
      if copy.httpBody == nil, let stream = copy.httpBodyStream {
        stream.open()
        defer { stream.close() }
        var body = Data()
        var buffer = [UInt8](repeating: 0, count: 1024)
        while stream.hasBytesAvailable {
          let count = stream.read(&buffer, maxLength: buffer.count)
          if count <= 0 { break }
          body.append(buffer, count: count)
        }
        copy.httpBody = body
      }
      Self.recorded.append(copy)
      return Self.steps.isEmpty ? nil : Self.steps.removeFirst()
    }
    switch step {
    case let .respond(status, body):
      let response = HTTPURLResponse(url: request.url!, statusCode: status, httpVersion: "HTTP/1.1",
        headerFields: ["Content-Type": "application/json"])!
      client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
      client?.urlProtocol(self, didLoad: body)
      client?.urlProtocolDidFinishLoading(self)
    case let .redirect(target):
      let response = HTTPURLResponse(url: request.url!, statusCode: 307, httpVersion: "HTTP/1.1",
        headerFields: ["Location": target.absoluteString])!
      var next = request
      next.url = target
      client?.urlProtocol(self, wasRedirectedTo: next, redirectResponse: response)
      client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
      client?.urlProtocolDidFinishLoading(self)
    case nil:
      client?.urlProtocol(self, didFailWithError: URLError(.cannotConnectToHost))
    }
  }

  override func stopLoading() {}
}
