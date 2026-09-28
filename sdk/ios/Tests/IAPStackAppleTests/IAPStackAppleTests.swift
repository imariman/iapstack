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

  func testExternalCustomerIdWithSurroundingWhitespaceIsRejected() async throws {
    URLStubProtocol.reset()
    let client = try IAPStackClient(
      config: IAPStackConfig(
        baseUri: URL(string: "https://example.local")!,
        applicationId: "application-1",
        customerToken: "customer-token",
      ),
      session: makeSession(),
    )
    do {
      _ = try await client.getEntitlements(" customer-id")
      XCTFail("expected configuration error")
    } catch IAPStackSDKError.configurationError {
      // Expected.
    }
    XCTAssertThrowsError(
      try PurchaseSubmission(
        externalCustomerId: "customer-id ",
        claimedProducts: ["sku-1"],
        evidence: ["signed_transaction": "a.b.c", "product_kind": "subscription"],
      ),
    )
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
    } catch let IAPStackSDKError.apiError(statusCode, code, _, requestId, retryable, _) {
      XCTAssertEqual(statusCode, 503)
      XCTAssertEqual(code, "http_error")
      XCTAssertEqual(requestId, "edge-1")
      XCTAssertTrue(retryable)
    }
    XCTAssertEqual(attempts.value, 2)
  }

  func testApiErrorExposesRetryAfterAsDelaySecondsOrHttpDate() async throws {
    // 2026-09-28T12:00:00Z
    let now = Date(timeIntervalSince1970: 1_790_596_800)
    let cases: [(String?, TimeInterval?)] = [
      (nil, nil),
      ("17", 17),
      ("Fri, 02 Oct 2026 00:00:00 GMT", 302_400),
      ("Fri, 31 Feb 2026 00:00:00 GMT", nil),
      ("soon", nil),
    ]
    for (header, expected) in cases {
      URLStubProtocol.reset()
      URLStubProtocol.enqueue { request in
        var headers = ["Content-Type": "application/json"]
        if let header {
          headers["Retry-After"] = header
        }
        let response = HTTPURLResponse(
          url: request.url!,
          statusCode: 429,
          httpVersion: nil,
          headerFields: headers,
        )!
        return (response, Data(#"{"error":{"code":"rate_limited","message":"slow down"}}"#.utf8))
      }
      let config = IAPStackConfig(
        baseUri: URL(string: "https://example.local")!,
        applicationId: "application-1",
        customerToken: "customer-token",
        retryPolicy: .init(maxAttempts: 1),
      )
      let client = try IAPStackClient(config: config, session: makeSession())
      client.clock = { now }
      do {
        _ = try await client.getEntitlements("customer-id")
        XCTFail("expected api error for Retry-After \(header ?? "nil")")
      } catch let IAPStackSDKError.apiError(_, _, _, _, retryable, retryAfter) {
        XCTAssertTrue(retryable)
        if let expected {
          XCTAssertEqual(retryAfter ?? -1, expected, accuracy: 0.001, "Retry-After \(header ?? "nil")")
        } else {
          XCTAssertNil(retryAfter, "Retry-After \(header ?? "nil")")
        }
      }
    }
  }

  func testParseRetryAfterAcceptsDelaySecondsAndRoundTrippingImfFixdatesOnly() {
    // 2026-09-28T12:00:00Z
    let now = Date(timeIntervalSince1970: 1_790_596_800)
    let cases: [(String?, TimeInterval?)] = [
      (nil, nil),
      ("", nil),
      ("17", 17),
      (" 5 ", 5),
      ("999999999", 999_999_999),
      ("1000000000", nil),
      ("Fri, 02 Oct 2026 00:00:00 GMT", 302_400),
      ("Mon, 28 Sep 2026 11:59:00 GMT", 0),
      ("Fri, 31 Feb 2026 00:00:00 GMT", nil),
      ("Wed, 31 Apr 2026 00:00:00 GMT", nil),
      ("Mon, 02 Oct 2026 00:00:00 GMT", nil),
      ("Friday, 02-Oct-26 00:00:00 GMT", nil),
      ("Fri Oct  2 00:00:00 2026", nil),
      ("Fri, 02 Oct 2026 00:00:00 +0000", nil),
      ("02 Oct 2026 00:00:00 GMT", nil),
      ("Fri, 02 Oct 2026 24:00:00 GMT", nil),
      ("-3", nil),
      ("1.5", nil),
      ("March 1, 2026", nil),
      ("soon", nil),
    ]
    for (header, expected) in cases {
      let parsed = parseRetryAfter(header, now: now)
      if let expected {
        XCTAssertEqual(parsed ?? -1, expected, accuracy: 0.001, "Retry-After \(header ?? "nil")")
      } else {
        XCTAssertNil(parsed, "Retry-After \(header ?? "nil")")
      }
    }
  }

  func testWaitsAtLeastTheRetryAfterCooldownBeforeRetrying() async throws {
    URLStubProtocol.reset()
    let attempts = Counter()
    URLStubProtocol.enqueue { request in
      attempts.increment()
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 503,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json", "Retry-After": "3"],
      )!
      return (response, Data(#"{"error":{"code":"provider_unavailable","message":"cooling down"}}"#.utf8))
    }
    enqueueEntitlementsResponse(attempts: attempts)
    let config = IAPStackConfig(
      baseUri: URL(string: "https://example.local")!,
      applicationId: "application-1",
      customerToken: "customer-token",
      retryPolicy: .init(maxAttempts: 2, baseDelay: 0.04, maxDelay: 0.04),
    )
    let client = try IAPStackClient(config: config, session: makeSession())
    let slept = Slept()
    client.sleep = { slept.record($0) }
    _ = try await client.getEntitlements("customer-id")
    XCTAssertEqual(attempts.value, 2)
    XCTAssertEqual(slept.values, [3])
  }

  func testElapsedRetryAfterDoesNotBlockTheRetry() async throws {
    URLStubProtocol.reset()
    let attempts = Counter()
    URLStubProtocol.enqueue { request in
      attempts.increment()
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 503,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json", "Retry-After": "0"],
      )!
      return (response, Data(#"{"error":{"code":"provider_unavailable","message":"x"}}"#.utf8))
    }
    enqueueEntitlementsResponse(attempts: attempts)
    let client = try IAPStackClient(config: retryingConfig(), session: makeSession())
    _ = try await client.getEntitlements("customer-id")
    XCTAssertEqual(attempts.value, 2)
  }

  func testRetryPolicyRaisesJitterToABudgetedRetryAfter() {
    let policy = IAPStackRetryPolicy(baseDelay: 0.1, maxDelay: 0.25)
    XCTAssertEqual(policy.delayAfter(attempt: 1, randomValue: 0.5), 0.05, accuracy: 0.0001)
    XCTAssertEqual(policy.delayAfter(attempt: 1, randomValue: 0.5, retryAfter: nil), 0.05, accuracy: 0.0001)
    XCTAssertEqual(policy.delayAfter(attempt: 1, randomValue: 0.5, retryAfter: 0), 0.05, accuracy: 0.0001)
    XCTAssertEqual(policy.delayAfter(attempt: 1, randomValue: 1, retryAfter: 0.02), 0.1, accuracy: 0.0001)
    XCTAssertEqual(policy.delayAfter(attempt: 1, randomValue: 0, retryAfter: 4), 4, accuracy: 0.0001)
    XCTAssertEqual(policy.delayAfter(attempt: 1, randomValue: 1, retryAfter: 3600), IAPStackRetryPolicy.maxRetryAfter)
    XCTAssertEqual(IAPStackRetryPolicy.maxRetryAfter, 30)

    // The budget bounds the cooldown, never the configured jitter.
    let wide = IAPStackRetryPolicy(baseDelay: 40, maxDelay: 60)
    XCTAssertEqual(wide.delayAfter(attempt: 1, randomValue: 1, retryAfter: 1), 40, accuracy: 0.0001)
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

  func testLaunchPurchaseReturnsTransactionForVerificationAndFinish() async throws {
    URLStubProtocol.reset()
    URLStubProtocol.enqueue { request in
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 200,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      let body = """
      {"verified_at": "2026-09-10T12:00:00Z", "customer_id": "customer-internal", "entitlements": []}
      """
      return (response, Data(body.utf8))
    }
    let customer = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
    let purchase = ApplePurchase(
      transactionId: "tx-1",
      productId: "premium_lifetime",
      signedTransaction: "a.b.c",
      status: .purchased,
      pendingCompletion: true,
      appAccountToken: customer,
      errorCode: nil,
    )
    let platform = FakeApplePlatform(launchResult: purchase)
    let client = try IAPStackClient(
      config: IAPStackConfig(
        baseUri: URL(string: "https://example.local")!,
        applicationId: "application-1",
        customerToken: "customer-token",
      ),
      session: makeSession(),
    )
    // `try` keeps this compiling once AppleIAPStack.init becomes throwing (#122).
    let stack = try AppleIAPStack(
      client: client,
      productKinds: ["premium_lifetime": .nonConsumable],
      platform: platform,
    )
    let product = AppleProduct(
      id: "premium_lifetime",
      kind: .nonConsumable,
      title: "Lifetime",
      description: "",
      price: "$1",
      rawPrice: 1,
      currencyCode: "USD",
    )

    let launched = try await stack.launchPurchase(externalCustomerId: customer, product: product)
    let returned = try XCTUnwrap(launched)
    XCTAssertEqual(returned.transactionId, "tx-1")

    let result = try await stack.verifyPurchase(externalCustomerId: customer, purchase: returned)
    XCTAssertEqual(result.customerId, "customer-internal")
    XCTAssertEqual(platform.completedTransactionIds, ["tx-1"])
  }

  func testRestoreFinishesOnlyUnfinishedRowsAfterBatchVerifies() async throws {
    URLStubProtocol.reset()
    URLStubProtocol.enqueue { request in
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 200,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      let row = #"{"verified_at": "2026-09-10T12:00:00Z", "customer_id": "customer-internal", "entitlements": []}"#
      return (response, Data(#"{"results": [\#(row), \#(row)]}"#.utf8))
    }
    let customer = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
    let platform = FakeApplePlatform(restoreResult: [
      restoredRow("tx-unfinished", customer: customer, pendingCompletion: true),
      restoredRow("tx-finished", customer: customer, pendingCompletion: false),
      restoredRow("tx-other-customer", customer: "9b2c1f64-1c7e-4c55-9a51-0c6f1d7e2a10", pendingCompletion: true),
    ])
    let stack = try makeAppleStack(platform: platform)

    let result = try await stack.restorePurchases(externalCustomerId: customer)

    XCTAssertEqual(result.results.count, 2)
    XCTAssertEqual(platform.completedTransactionIds, ["tx-unfinished"])
  }

  func testRestoreDoesNotFinishRowsWhenBatchFails() async throws {
    URLStubProtocol.reset()
    URLStubProtocol.enqueue { request in
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 400,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      let body = #"{"error": {"code": "invalid_request", "message": "rejected", "request_id": "request-1"}}"#
      return (response, Data(body.utf8))
    }
    let customer = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
    let platform = FakeApplePlatform(restoreResult: [
      restoredRow("tx-unfinished", customer: customer, pendingCompletion: true),
    ])
    let stack = try makeAppleStack(platform: platform)

    do {
      _ = try await stack.restorePurchases(externalCustomerId: customer)
      XCTFail("Expected the rejected batch to throw")
    } catch {
      XCTAssertEqual(platform.completedTransactionIds, [])
    }
  }

  private func makeAppleStack(platform: FakeApplePlatform) throws -> AppleIAPStack {
    let client = try IAPStackClient(
      config: IAPStackConfig(
        baseUri: URL(string: "https://example.local")!,
        applicationId: "application-1",
        customerToken: "customer-token",
        retryPolicy: .init(maxAttempts: 1),
      ),
      session: makeSession(),
    )
    // `try` keeps this compiling once AppleIAPStack.init becomes throwing (#122).
    return try AppleIAPStack(
      client: client,
      productKinds: ["premium_lifetime": .nonConsumable],
      platform: platform,
    )
  }

  private func restoredRow(_ transactionId: String, customer: String, pendingCompletion: Bool) -> ApplePurchase {
    ApplePurchase(
      transactionId: transactionId,
      productId: "premium_lifetime",
      signedTransaction: "a.b.c",
      status: .restored,
      pendingCompletion: pendingCompletion,
      appAccountToken: customer,
      errorCode: nil,
    )
  }

  func testEmptyProductIdentifierThrowsInsteadOfTrapping() throws {
    let client = try IAPStackClient(
      config: IAPStackConfig(
        baseUri: URL(string: "https://example.local")!,
        applicationId: "application-1",
        customerToken: "customer-token",
      ),
      session: makeSession(),
    )
    XCTAssertThrowsError(try AppleIAPStack(client: client, productKinds: [" ": .subscription])) { error in
      XCTAssertEqual((error as? AppleIAPStackError)?.code, "invalid_product_catalog")
    }
  }

  func testBlankRequestIdIsOmitted() async throws {
    URLStubProtocol.reset()
    var capturedRequest: URLRequest?
    URLStubProtocol.enqueue { request in
      capturedRequest = request
      let response = HTTPURLResponse(
        url: request.url!,
        statusCode: 200,
        httpVersion: nil,
        headerFields: ["Content-Type": "application/json"],
      )!
      return (response, Data(#"{"customer_id":"c","entitlements":[]}"#.utf8))
    }
    let client = try IAPStackClient(
      config: IAPStackConfig(
        baseUri: URL(string: "https://example.local")!,
        applicationId: "application-1",
        customerToken: "customer-token",
      ),
      session: makeSession(),
    )
    _ = try await client.getEntitlements("customer-id", requestId: "   ")
    XCTAssertNil(try XCTUnwrap(capturedRequest).value(forHTTPHeaderField: "X-Request-ID"))
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

/// Records the delays a client asked to sleep for.
final class Slept: @unchecked Sendable {
  private let lock = NSLock()
  private var recorded: [TimeInterval] = []

  var values: [TimeInterval] {
    lock.withLock { recorded }
  }

  func record(_ seconds: TimeInterval) {
    lock.withLock { recorded.append(seconds) }
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

final class FakeApplePlatform: AppleIAPPlatform, @unchecked Sendable {
  init(launchResult: ApplePurchase? = nil, restoreResult: [ApplePurchase] = []) {
    self.launchResult = launchResult
    self.restoreResult = restoreResult
  }

  private let launchResult: ApplePurchase?
  private let restoreResult: [ApplePurchase]
  private let lock = NSLock()
  private var completed: [String] = []

  var completedTransactionIds: [String] {
    lock.withLock { completed }
  }

  var purchaseUpdates: AsyncStream<ApplePurchase> {
    AsyncStream { $0.finish() }
  }

  func isAvailable() async throws -> Bool {
    true
  }

  func queryProducts(productIds: Set<String>) async throws -> AppleProductQuery {
    AppleProductQuery(products: [], notFoundProductIds: productIds)
  }

  func launchPurchase(product: AppleProduct, appAccountToken: String) async throws -> ApplePurchase? {
    launchResult
  }

  func restorePurchases() async throws -> [ApplePurchase] {
    restoreResult
  }

  func completePurchase(_ purchase: ApplePurchase) async throws {
    lock.withLock { completed.append(purchase.transactionId) }
  }
}
