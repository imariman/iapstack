import Foundation
import Network
import XCTest
@testable import IAPStackApple

/// Uses real URLSession redirects: a URLProtocol returning 307 alone does not exercise redirect handling.
final class RedirectTests: XCTestCase {
  func testRedirectsNeverReplayCredentialsOrEvidenceWithOwnedOrInjectedSessions() async throws {
    for status in [301, 302, 303, 307, 308] {
      for injected in [false, true] {
        let server = try RedirectServer(status: status)
        let baseURL = try await server.start()
        let session = injected ? URLSession(configuration: .ephemeral) : nil
        let client = try IAPStackClient(config: IAPStackConfig(
          baseUri: baseURL, applicationId: "app", customerToken: "synthetic-customer-token",
          retryPolicy: .init(maxAttempts: 3, baseDelay: 0, maxDelay: 0), allowInsecureHttp: true
        ), session: session)
        defer { client.close(); session?.invalidateAndCancel(); server.stop() }
        let purchase = try PurchaseSubmission(externalCustomerId: "customer",
          claimedProducts: ["premium"], evidence: ["signed_transaction": "test.payload.signature"])
        do {
          _ = try await client.verifyPurchase(purchase)
          XCTFail("Expected redirect rejection (\(status), injected: \(injected))")
        } catch let error as IAPStackSDKError {
          guard case let .apiError(statusCode, _, _, _, retryable, _) = error else {
            return XCTFail("Expected API error for redirect, got \(error)")
          }
          XCTAssertEqual(statusCode, status)
          XCTAssertFalse(retryable)
        }
        XCTAssertEqual(server.requestCount, 1, "The redirect target must receive no credentials/evidence")
      }
    }
  }
}

private final class RedirectServer: @unchecked Sendable {
  private let listener: NWListener
  private let status: Int
  private let queue = DispatchQueue(label: "iapstack.redirect.test")
  private let lock = NSLock()
  private var count = 0
  private var connections: [NWConnection] = []
  var requestCount: Int { lock.withLock { count } }

  init(status: Int) throws {
    self.status = status
    let parameters = NWParameters.tcp
    parameters.requiredLocalEndpoint = .hostPort(host: .ipv4(.loopback), port: .any)
    listener = try NWListener(using: parameters)
  }

  func start() async throws -> URL {
    listener.newConnectionHandler = { [weak self] connection in self?.handle(connection) }
    return try await withCheckedThrowingContinuation { continuation in
      var resumed = false
      listener.stateUpdateHandler = { [weak self] state in
        guard let self, !resumed else { return }
        switch state {
        case .ready:
          resumed = true
          continuation.resume(returning: URL(string: "http://127.0.0.1:\(self.listener.port!.rawValue)")!)
        case .failed(let error):
          resumed = true
          continuation.resume(throwing: error)
        default: break
        }
      }
      listener.start(queue: queue)
    }
  }

  func stop() {
    listener.cancel()
    lock.withLock { connections.forEach { $0.cancel() }; connections = [] }
  }

  private func handle(_ connection: NWConnection) {
    lock.withLock { connections.append(connection) }
    connection.start(queue: queue)
    connection.receive(minimumIncompleteLength: 1, maximumLength: 65_536) { [weak self] data, _, _, error in
      guard let self, data != nil, error == nil else { connection.cancel(); return }
      self.lock.withLock { self.count += 1 }
      // Changing the hostname also exercises cross-origin redirect rejection.
      let response = "HTTP/1.1 \(self.status) Redirect\r\nLocation: http://localhost:\(self.listener.port!.rawValue)/target\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
      connection.send(content: Data(response.utf8), completion: .contentProcessed { _ in connection.cancel() })
    }
  }
}
