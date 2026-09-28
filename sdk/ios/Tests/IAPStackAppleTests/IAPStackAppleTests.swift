import Foundation
import XCTest

@testable import IAPStackApple

final class URLStubProtocol: URLProtocol {
  private enum Step {
    case respond(@Sendable (URLRequest) throws -> (HTTPURLResponse, Data))
    case stall(onStart: @Sendable () -> Void, onStop: @Sendable () -> Void)
  }

  private static let lock = NSLock()
  private static var steps: [Step] = []
  private var onStop: (@Sendable () -> Void)?

  static func reset() {
    lock.withLock { steps.removeAll() }
  }

  static func enqueue(handler: @escaping @Sendable (URLRequest) throws -> (HTTPURLResponse, Data)) {
    lock.withLock { steps.append(.respond(handler)) }
  }

  /// Enqueues a request that never answers. `onStart` runs when the request reaches
  /// the stub and `onStop` when URLSession cancels it.
  static func enqueueStall(
    onStart: @escaping @Sendable () -> Void,
    onStop: @escaping @Sendable () -> Void,
  ) {
    lock.withLock { steps.append(.stall(onStart: onStart, onStop: onStop)) }
  }

  override class func canInit(with request: URLRequest) -> Bool {
    true
  }

  override class func canonicalRequest(for request: URLRequest) -> URLRequest {
    var copy = request
    if copy.httpBody == nil, let stream = copy.httpBodyStream {
      copy.httpBodyStream = nil
      copy.httpBody = Data(reading: stream)
    }
    return copy
  }

  override func startLoading() {
    let step = Self.lock.withLock { Self.steps.isEmpty ? nil : Self.steps.removeFirst() }
    switch step {
    case nil:
      client?.urlProtocol(self, didFailWithError: NSError(domain: "iapstack-stub", code: -1))
    case let .respond(handler):
      do {
        let (response, data) = try handler(request)
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: data)
        client?.urlProtocolDidFinishLoading(self)
      } catch {
        client?.urlProtocol(self, didFailWithError: error)
      }
    case let .stall(onStart, onStop):
      self.onStop = onStop
      onStart()
    }
  }

  override func stopLoading() {
    onStop?()
    onStop = nil
  }
}

private extension Data {
  init(reading stream: InputStream) {
    self.init()
    stream.open()
    defer { stream.close() }
    let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: 1024)
    defer { buffer.deallocate() }
    while stream.hasBytesAvailable {
      let read = stream.read(buffer, maxLength: 1024)
      if read > 0 {
        append(buffer, count: read)
      } else {
        break
      }
    }
  }
}

final class IAPStackAppleTests: XCTestCase {
  func testVerifyPurchaseUsesV1ContractAndDecodesEntitlements() async throws {
    URLStubProtocol.reset()
    let baseUri = URL(string: "https://example.local/proxy")!
    var capturedRequest: URLRequest?
    let responseBody = """
    {
      "verified_at": "2026-09-10T12:00:00Z",
      "customer_id": "customer-internal",
      "entitlements": [
        {
          "key": "premium",
          "access": "allowed",
          "reason": "verified",
          "version": 3,
          "effective_starts_at": null,
          "effective_ends_at": null
        }
      ]
    }
    """
    URLStubProtocol.enqueue { request in
      capturedRequest = request
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 200,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      return (response, Data(responseBody.utf8))
    }

    let config = IAPStackConfig(
      baseUri: baseUri,
      applicationId: "application-1",
      customerToken: "customer-token",
    )
    let session = makeSession()
    let client = try IAPStackClient(config: config, session: session)
    let purchase = try PurchaseSubmission(
      externalCustomerId: "customer-external",
      claimedProducts: ["premium_lifetime"],
      evidence: [
        "signed_transaction": "a.b.c",
        "product_kind": "non_consumable",
      ],
    )

    let result = try await client.verifyPurchase(purchase, requestId: "request-client-1")

    let request = try XCTUnwrap(capturedRequest)
    XCTAssertEqual(request.httpMethod, "POST")
    XCTAssertEqual(request.url?.path, "/proxy/v1/applications/application-1/purchases:verify")
    XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer customer-token")
    XCTAssertEqual(request.value(forHTTPHeaderField: "X-Request-ID"), "request-client-1")
    XCTAssertEqual(request.value(forHTTPHeaderField: "X-IAPStack-SDK"), "ios-swift/0.1.0-dev.1")

    let decoded = try XCTUnwrap(
      request.httpBody.flatMap { try JSONSerialization.jsonObject(with: $0) as? [String: Any] },
    )
    XCTAssertEqual(decoded["external_customer_id"] as? String, "customer-external")
    let postedClaimed = decoded["claimed_products"] as? [String]
    XCTAssertEqual(postedClaimed, ["premium_lifetime"])

    let postedEvidence = decoded["evidence"] as? [String: String]
    XCTAssertEqual(postedEvidence?["product_kind"], "non_consumable")
    XCTAssertEqual(result.customerId, "customer-internal")
    XCTAssertEqual(result.entitlements.first?.key, "premium")
  }

  func testRestoreRetriesOnTransientError() async throws {
    URLStubProtocol.reset()
    let baseUri = URL(string: "https://example.local")!
    var attempts = 0
    URLStubProtocol.enqueue { request in
      attempts += 1
      let responseBody = """
      {
        "error": {
          "code": "provider_unavailable",
          "message": "temporary outage",
          "request_id": "request-server-1"
        }
      }
      """
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 503,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      return (response, Data(responseBody.utf8))
    }
    URLStubProtocol.enqueue { request in
      attempts += 1
      let responseBody = """
      {
        "results": [
          {
            "verified_at": "2026-09-10T12:00:00Z",
            "customer_id": "customer-internal",
            "entitlements": []
          }
        ]
      }
      """
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 200,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      return (response, Data(responseBody.utf8))
    }

    let config = IAPStackConfig(
      baseUri: baseUri,
      applicationId: "application-1",
      customerToken: "customer-token",
      timeout: 1,
      retryPolicy: .init(maxAttempts: 2),
    )
    let client = try IAPStackClient(config: config, session: makeSession())
    let submission = try PurchaseSubmission(
      externalCustomerId: "customer-external",
      claimedProducts: ["sku-1"],
      evidence: ["signed_transaction": "a.b.c", "product_kind": "subscription"],
    )

    let result = try await client.restorePurchases([submission], requestId: "restore-1")

    XCTAssertEqual(attempts, 2)
    XCTAssertEqual(result.results.count, 1)
  }

  func testGetEntitlementsEscapesExternalCustomerInPath() async throws {
    URLStubProtocol.reset()
    var capturedPath: String?
    URLStubProtocol.enqueue { request in
      capturedPath = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.percentEncodedPath
      let responseBody = """
      {
        "customer_id": "customer-internal",
        "entitlements": []
      }
      """
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 200,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      return (response, Data(responseBody.utf8))
    }

    let config = IAPStackConfig(
      baseUri: URL(string: "https://example.local/proxy")!,
      applicationId: "application-1",
      customerToken: "customer-token",
    )
    let client = try IAPStackClient(config: config, session: makeSession())
    _ = try await client.getEntitlements("customer/with space")

    XCTAssertEqual(
      capturedPath,
      "/proxy/v1/applications/application-1/customers/customer%2Fwith%20space/entitlements",
    )
  }

  func testTimeoutTurnsIntoTimeoutError() async throws {
    URLStubProtocol.reset()
    URLStubProtocol.enqueue { request in
      Thread.sleep(forTimeInterval: 0.05)
      let responseBody = """
      {"customer_id":"customer-internal","entitlements":[]}
      """
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 200,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      return (response, Data(responseBody.utf8))
    }

    let config = IAPStackConfig(
      baseUri: URL(string: "https://example.local")!,
      applicationId: "application-1",
      customerToken: "customer-token",
      timeout: 0.001,
      retryPolicy: .init(maxAttempts: 1),
    )
    let client = try IAPStackClient(config: config, session: makeSession())
    do {
      _ = try await client.getEntitlements("customer-id")
      XCTFail("expected timeout error")
    } catch {
      guard case IAPStackSDKError.timeoutError = error else {
        XCTFail("expected timeout, got \(error)")
        return
      }
    }
  }

  func testInvalidOptionalDatesBecomeProtocolErrors() async throws {
    URLStubProtocol.reset()
    URLStubProtocol.enqueue { request in
      let responseBody = """
      {
        "verified_at": "2026-09-10T12:00:00Z",
        "customer_id": "customer-internal",
        "entitlements": [
          {
            "key": "premium",
            "access": "allowed",
            "reason": "verified",
            "version": 1,
            "effective_starts_at": "not-a-timestamp",
            "effective_ends_at": null
          }
        ]
      }
      """
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 200,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      return (response, Data(responseBody.utf8))
    }

    let config = IAPStackConfig(
      baseUri: URL(string: "https://example.local")!,
      applicationId: "application-1",
      customerToken: "customer-token",
    )
    let client = try IAPStackClient(config: config, session: makeSession())
    let submission = try PurchaseSubmission(
      externalCustomerId: "customer",
      claimedProducts: ["sku-1"],
      evidence: ["signed_transaction": "a.b.c", "product_kind": "non_consumable"],
    )
    do {
      _ = try await client.verifyPurchase(submission)
      XCTFail("expected protocol error")
    } catch {
      guard case IAPStackSDKError.protocolError = error else {
        XCTFail("expected protocolError, got \(error)")
        return
      }
    }
  }

  func testApplePurchaseEvidenceValidatesJwsShape() {
    XCTAssertNoThrow(try ApplePurchaseEvidence(signedTransaction: "a.b.c", productKind: .nonConsumable))
    XCTAssertThrowsError(
      try ApplePurchaseEvidence(signedTransaction: "not-a-jws", productKind: .subscription),
    )
  }

  func testOversizedResponseBecomesProtocolError() async throws {
    URLStubProtocol.reset()
    URLStubProtocol.enqueue { request in
      let responseBody = "{\"value\":\"toolarge\"}".data(using: .utf8)!
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 200,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      return (response, responseBody)
    }

    let config = IAPStackConfig(
      baseUri: URL(string: "https://example.local")!,
      applicationId: "application-1",
      customerToken: "customer-token",
      maxResponseBytes: 5,
    )
    let client = try IAPStackClient(config: config, session: makeSession())
    let submission = try PurchaseSubmission(
      externalCustomerId: "customer",
      claimedProducts: ["sku-1"],
      evidence: ["signed_transaction": "a.b.c", "product_kind": "non_consumable"],
    )
    do {
      _ = try await client.verifyPurchase(submission)
      XCTFail("expected protocol error")
    } catch {
      guard case IAPStackSDKError.protocolError = error else {
        XCTFail("expected protocolError, got \(error)")
        return
      }
    }
  }

  func testProxyHtmlErrorIsRetriedAndSurfacesApiError() async throws {
    URLStubProtocol.reset()
    let attempts = Counter()
    for _ in 0 ..< 2 {
      URLStubProtocol.enqueue { request in
        attempts.increment()
        let response = HTTPURLResponse(
          url: request.url!,
          statusCode: 503,
          httpVersion: nil,
          headerFields: ["Content-Type": "text/html", "X-Request-ID": "edge-1"],
        )!
        return (response, Data("<html>Service Unavailable</html>".utf8))
      }
    }
    let config = IAPStackConfig(
      baseUri: URL(string: "https://example.local")!,
      applicationId: "application-1",
      customerToken: "customer-token",
      retryPolicy: .init(maxAttempts: 2, baseDelay: 0, maxDelay: 0),
    )
    let client = try IAPStackClient(config: config, session: makeSession())
    do {
      _ = try await client.getEntitlements("customer-id")
      XCTFail("expected api error")
    } catch let IAPStackSDKError.apiError(statusCode, code, _, requestId, retryable) {
      XCTAssertEqual(statusCode, 503)
      XCTAssertEqual(code, "http_error")
      XCTAssertEqual(requestId, "edge-1")
      XCTAssertTrue(retryable)
    }
    XCTAssertEqual(attempts.value, 2)
  }

  func testCloseDoesNotInvalidateInjectedSession() async throws {
    URLStubProtocol.reset()
    for _ in 0 ..< 1 {
      URLStubProtocol.enqueue { request in
        let response = HTTPURLResponse(
          url: request.url!,
          statusCode: 200,
          httpVersion: nil,
          headerFields: ["Content-Type": "application/json"],
        )!
        return (response, Data(#"{"customer_id":"c","entitlements":[]}"#.utf8))
      }
    }
    let session = makeSession()
    let config = IAPStackConfig(
      baseUri: URL(string: "https://example.local")!,
      applicationId: "application-1",
      customerToken: "customer-token",
    )
    let closedClient = try IAPStackClient(config: config, session: session)
    closedClient.close()
    do {
      _ = try await closedClient.getEntitlements("customer-id")
      XCTFail("expected closed-client error")
    } catch {
      assertClientClosed(error)
    }

    let snapshot = try await IAPStackClient(config: config, session: session).getEntitlements("customer-id")
    XCTAssertEqual(snapshot.customerId, "c")
  }

  func testCloseDuringRequestFailsWithoutRetrying() async throws {
    URLStubProtocol.reset()
    let attempts = Counter()
    let started = expectation(description: "attempt started")
    let stopped = expectation(description: "attempt cancelled")
    URLStubProtocol.enqueueStall(
      onStart: {
        attempts.increment()
        started.fulfill()
      },
      onStop: { stopped.fulfill() },
    )
    enqueueEntitlementsResponse(attempts: attempts)
    let client = try makeOwningClient(config: retryingConfig())
    let task = Task { try await client.getEntitlements("customer-id") }
    await fulfillment(of: [started], timeout: 5)

    client.close()

    do {
      _ = try await task.value
      XCTFail("expected closed-client error")
    } catch {
      assertClientClosed(error)
    }
    await fulfillment(of: [stopped], timeout: 5)
    XCTAssertEqual(attempts.value, 1)
  }

  func testCallAfterCloseFailsWithoutCreatingTask() async throws {
    URLStubProtocol.reset()
    let attempts = Counter()
    enqueueEntitlementsResponse(attempts: attempts)
    let client = try makeOwningClient(config: retryingConfig())

    client.close()

    do {
      _ = try await client.getEntitlements("customer-id")
      XCTFail("expected closed-client error")
    } catch {
      assertClientClosed(error)
    }
    XCTAssertEqual(attempts.value, 0)
  }

  func testRequestCancelledByInjectedSessionIsNotRetried() async throws {
    URLStubProtocol.reset()
    let attempts = Counter()
    let started = expectation(description: "attempt started")
    URLStubProtocol.enqueueStall(
      onStart: {
        attempts.increment()
        started.fulfill()
      },
      onStop: {},
    )
    enqueueEntitlementsResponse(attempts: attempts)
    let session = makeSession()
    let client = try IAPStackClient(config: retryingConfig(), session: session)
    let task = Task { try await client.getEntitlements("customer-id") }
    await fulfillment(of: [started], timeout: 5)

    session.invalidateAndCancel()

    do {
      _ = try await task.value
      XCTFail("expected transport error")
    } catch let error as IAPStackSDKError {
      guard case let .transportError(_, cause) = error else {
        XCTFail("expected transportError, got \(error)")
        return
      }
      XCTAssertEqual((cause as? URLError)?.code, .cancelled)
      XCTAssertFalse(error.isRetryable)
    }
    XCTAssertEqual(attempts.value, 1)
  }

  func testResponseAtExactlyMaxResponseBytesIsAccepted() async throws {
    URLStubProtocol.reset()
    let body = Data(#"{"customer_id":"c","entitlements":[]}"#.utf8)
    for _ in 0 ..< 2 {
      URLStubProtocol.enqueue { request in
        let response = HTTPURLResponse(
          url: request.url!,
          statusCode: 200,
          httpVersion: nil,
          headerFields: ["Content-Type": "application/json", "Content-Length": "\(body.count)"],
        )!
        return (response, body)
      }
    }
    func config(maxResponseBytes: Int) -> IAPStackConfig {
      IAPStackConfig(
        baseUri: URL(string: "https://example.local")!,
        applicationId: "application-1",
        customerToken: "customer-token",
        maxResponseBytes: maxResponseBytes,
      )
    }

    let atLimit = try IAPStackClient(config: config(maxResponseBytes: body.count), session: makeSession())
    let snapshot = try await atLimit.getEntitlements("customer-id")
    XCTAssertEqual(snapshot.customerId, "c")

    let belowBody = try IAPStackClient(config: config(maxResponseBytes: body.count - 1), session: makeSession())
    do {
      _ = try await belowBody.getEntitlements("customer-id")
      XCTFail("expected protocol error")
    } catch {
      guard case IAPStackSDKError.protocolError = error else {
        XCTFail("expected protocolError, got \(error)")
        return
      }
    }
  }

  func testCallerCancellationIsNotRetriedOrReportedAsTimeout() async throws {
    URLStubProtocol.reset()
    let attempts = Counter()
    let started = expectation(description: "attempt started")
    let stopped = expectation(description: "attempt cancelled")
    URLStubProtocol.enqueueStall(
      onStart: {
        attempts.increment()
        started.fulfill()
      },
      onStop: { stopped.fulfill() },
    )
    // A retry would get this response and return instead of throwing.
    enqueueEntitlementsResponse(attempts: attempts)
    let client = try IAPStackClient(config: retryingConfig(), session: makeSession())
    let task = Task { try await client.getEntitlements("customer-id") }
    await fulfillment(of: [started], timeout: 5)

    task.cancel()

    do {
      _ = try await task.value
      XCTFail("expected cancellation")
    } catch is CancellationError {
      // Expected.
    } catch {
      XCTFail("expected CancellationError, got \(error)")
    }
    await fulfillment(of: [stopped], timeout: 5)
    XCTAssertEqual(attempts.value, 1)
  }

  private func makeSession() -> URLSession {
    URLSession(configuration: stubConfiguration())
  }

  private func stubConfiguration() -> URLSessionConfiguration {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.protocolClasses = [URLStubProtocol.self]
    return configuration
  }

  /// A client that owns (and on close invalidates) a stubbed session.
  private func makeOwningClient(config: IAPStackConfig) throws -> IAPStackClient {
    try IAPStackClient(config: config, session: nil, ownedSessionConfiguration: stubConfiguration())
  }

  private func retryingConfig() -> IAPStackConfig {
    IAPStackConfig(
      baseUri: URL(string: "https://example.local")!,
      applicationId: "application-1",
      customerToken: "customer-token",
      timeout: 30,
      retryPolicy: .init(maxAttempts: 3, baseDelay: 0, maxDelay: 0),
    )
  }

  private func enqueueEntitlementsResponse(attempts: Counter) {
    URLStubProtocol.enqueue { request in
      attempts.increment()
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 200,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      return (response, Data(#"{"customer_id":"c","entitlements":[]}"#.utf8))
    }
  }

  private func assertClientClosed(_ error: Error, file: StaticString = #filePath, line: UInt = #line) {
    guard case let IAPStackSDKError.configurationError(message) = error else {
      XCTFail("expected closed-client error, got \(error)", file: file, line: line)
      return
    }
    XCTAssertEqual(message, "IAPStackClient is closed", file: file, line: line)
    XCTAssertFalse((error as? IAPStackSDKError)?.isRetryable ?? true, file: file, line: line)
  }
}

final class Counter: @unchecked Sendable {
  private let lock = NSLock()
  private var count = 0

  var value: Int {
    lock.withLock { count }
  }

  func increment() {
    lock.withLock { count += 1 }
  }
}
