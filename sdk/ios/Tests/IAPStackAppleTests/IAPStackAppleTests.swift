import Foundation
import XCTest

@testable import IAPStackApple

final class URLStubProtocol: URLProtocol {
  static var handlers: [(@Sendable (URLRequest) throws -> (HTTPURLResponse, Data))] = []

  static func reset() {
    handlers.removeAll()
  }

  static func enqueue(handler: @escaping @Sendable (URLRequest) throws -> (HTTPURLResponse, Data)) {
    handlers.append(handler)
  }

  override class func canInit(with request: URLRequest) -> Bool {
    true
  }

  override class func canonicalRequest(for request: URLRequest) -> URLRequest {
    request
  }

  override func startLoading() {
    guard !Self.handlers.isEmpty else {
      client?.urlProtocol(self, didFailWithError: NSError(domain: "iapstack-stub", code: -1))
      return
    }

    do {
      let handler = Self.handlers.removeFirst()
      let (response, data) = try handler(request)
      client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
      client?.urlProtocol(self, didLoad: data)
      client?.urlProtocolDidFinishLoading(self)
    } catch {
      client?.urlProtocol(self, didFailWithError: error)
    }
  }

  override func stopLoading() {}
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
    let client = IAPStackClient(config: config, session: session)
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
    XCTAssertEqual(request.value(forHTTPHeaderField: "X-IAPStack-SDK"), "swift/0.1.0-dev.1")

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
    let client = IAPStackClient(config: config, session: makeSession())
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
      capturedPath = request.url?.path
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
    let client = IAPStackClient(config: config, session: makeSession())
    _ = try await client.getEntitlements("customer/with space")

    XCTAssertEqual(capturedPath, "/proxy/v1/applications/application-1/customers/customer%2Fwith%20space/entitlements")
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
    )
    let client = IAPStackClient(config: config, session: makeSession())
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
    let client = IAPStackClient(config: config, session: makeSession())
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

  private func makeSession() -> URLSession {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.protocolClasses = [URLStubProtocol.self]
    return URLSession(configuration: configuration)
  }
}
