import Foundation
import IAPStackApple
import XCTest
@testable import IAPStackSampleCore

private let customer = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
private let verification = #"{"verified_at":"2026-09-10T12:00:00Z","customer_id":"internal","entitlements":[{"key":"premium","access":"allowed","reason":"verified","version":1,"effective_starts_at":null,"effective_ends_at":null}]}"#
private let snapshot = #"{"customer_id":"internal","entitlements":[]}"#

@MainActor
final class PurchaseStoreTests: XCTestCase {
  override func setUp() { StubProtocol.reset() }

  func testSessionWithoutLowercaseUUIDCustomerIsRejected() async {
    let store = PurchaseStore(loader: StubLoader(externalCustomerId: customer.uppercased()), platform: Platform())
    await store.connect(endpoint: "https://host.example/session", loginToken: "temporary-login")
    XCTAssertFalse(store.isConnected)
  }

  func testCatalogCanBeLoadedBeforeAuthentication() async {
    let store = PurchaseStore(loader: StubLoader(), platform: Platform())
    await store.queryProducts()
    XCTAssertEqual(store.products.count, 1)
    XCTAssertTrue(store.canMakePayments)
    XCTAssertFalse(store.isConnected)
  }

  func testPurchaseVerifiesThenFinishesAndPublishesEntitlements() async {
    let platform = Platform()
    let store = await connectedStore(platform: platform)
    StubProtocol.enqueue(status: 200, body: verification)
    await store.purchase(platform.product)
    XCTAssertEqual(platform.finished, ["tx-1"])
    XCTAssertEqual(store.entitlements.map(\.key), ["premium"])
    XCTAssertTrue(store.entitlements[0].grantsAccess())
    XCTAssertEqual(store.pendingCount, 0)
  }

  func testFailedVerificationRemainsUnfinishedThenCanRetry() async {
    let platform = Platform()
    let store = await connectedStore(platform: platform)
    StubProtocol.enqueue(status: 400, body: #"{"error":{"code":"invalid_evidence","message":"sensitive server text"}}"#)
    await store.purchase(platform.product)
    XCTAssertTrue(platform.finished.isEmpty)
    XCTAssertEqual(store.pendingCount, 1)
    XCTAssertFalse(store.status.contains("sensitive server text"))
    XCTAssertTrue(store.status.contains("unfinished"))
    StubProtocol.enqueue(status: 200, body: verification)
    await store.retryPending()
    XCTAssertEqual(platform.finished, ["tx-1"])
    XCTAssertEqual(store.pendingCount, 0)
  }

  func testSlowInitialRefreshCannotOverwriteNewlyVerifiedEntitlements() async throws {
    let platform = Platform()
    StubProtocol.enqueue(status: 200, body: snapshot, suspend: true)
    let store = PurchaseStore(loader: StubLoader(), platform: platform, makeClient: {
      try IAPStackClient(config: $0, session: makeSession())
    })
    let connection = Task { await store.connect(endpoint: "https://host.example/session", loginToken: "temporary-login") }
    try await eventually { StubProtocol.hasSuspendedResponse }
    StubProtocol.enqueue(status: 200, body: verification)
    platform.emit(platform.purchase)
    try await eventually { store.entitlements.contains { $0.key == "premium" } }
    StubProtocol.releaseResponse()
    await connection.value
    XCTAssertEqual(store.entitlements.map(\.key), ["premium"])
    store.disconnect()
  }

  func testCancelledPurchaseDoesNotSendVerificationOrFinish() async {
    let platform = Platform()
    platform.cancelPurchase = true
    let store = await connectedStore(platform: platform)
    await store.purchase(platform.product)
    XCTAssertEqual(store.status, "Purchase cancelled.")
    XCTAssertEqual(StubProtocol.requests.count, 1) // Initial entitlement refresh only.
    XCTAssertTrue(platform.finished.isEmpty)
  }

  func testPendingPurchaseIsVerifiedWhenUpdateArrives() async throws {
    let platform = Platform()
    platform.pendingPurchase = true
    let store = await connectedStore(platform: platform)
    await store.purchase(platform.product)
    XCTAssertTrue(store.status.contains("Awaiting approval"))
    StubProtocol.enqueue(status: 200, body: verification)
    platform.emit(platform.purchase)
    try await eventually { platform.finished == ["tx-1"] && store.pendingCount == 0 }
    XCTAssertEqual(store.entitlements.map(\.key), ["premium"])
    store.disconnect()
  }

  func testUpdateListenerSurvivesVerificationFailureAndObservesNextUpdate() async throws {
    let platform = Platform()
    let store = await connectedStore(platform: platform)
    StubProtocol.enqueue(status: 400, body: #"{"error":{"code":"invalid_evidence","message":"no"}}"#)
    platform.emit(platform.purchase)
    try await eventually { store.status.contains("unfinished") && store.pendingCount == 1 }
    XCTAssertTrue(platform.finished.isEmpty)
    StubProtocol.enqueue(status: 200, body: verification)
    platform.emit(platform.purchase)
    try await eventually { platform.finished == ["tx-1"] && store.pendingCount == 0 }
    store.disconnect()
  }

  func testForeignCustomerUpdateIsNeverVerified() async throws {
    let platform = Platform()
    let store = await connectedStore(platform: platform)
    platform.emit(ApplePurchase(transactionId: "foreign", productId: "premium_lifetime",
      signedTransaction: "a.b.c", status: .purchased, pendingCompletion: true,
      appAccountToken: UUID().uuidString.lowercased(), errorCode: nil))
    try await eventually { store.status.contains("another customer") }
    XCTAssertEqual(StubProtocol.requests.count, 1)
    XCTAssertTrue(platform.finished.isEmpty)
    store.disconnect()
  }

  func testRestoreVerifiesBeforeFinishingAndFetchesFullSnapshot() async {
    let platform = Platform()
    platform.restored = [platform.purchase]
    let store = await connectedStore(platform: platform)
    StubProtocol.enqueue(status: 200, body: "{\"results\":[\(verification)]}")
    StubProtocol.enqueue(status: 200, body: snapshot)
    await store.restore()
    XCTAssertEqual(platform.finished, ["tx-1"])
    XCTAssertEqual(StubProtocol.requests.map { $0.url!.lastPathComponent }, ["entitlements", "purchases:restore", "entitlements"])
  }

  func testRestoreClearsRememberedVerificationFailureAfterServerAcceptsIt() async {
    let platform = Platform()
    platform.restored = [platform.purchase]
    let store = await connectedStore(platform: platform)
    StubProtocol.enqueue(status: 400, body: "{}")
    await store.purchase(platform.product)
    XCTAssertEqual(store.pendingCount, 1)
    StubProtocol.enqueue(status: 200, body: "{\"results\":[\(verification)]}")
    StubProtocol.enqueue(status: 200, body: verification)
    StubProtocol.enqueue(status: 200, body: snapshot)
    await store.restore()
    XCTAssertEqual(store.pendingCount, 0)
    XCTAssertEqual(store.status, "Entitlements refreshed.")
  }

  func testFailedRestoreDoesNotFinish() async {
    let platform = Platform()
    platform.restored = [platform.purchase]
    let store = await connectedStore(platform: platform)
    StubProtocol.enqueue(status: 400, body: "{}")
    await store.restore()
    XCTAssertTrue(platform.finished.isEmpty)
    XCTAssertTrue(store.status.contains("unfinished"))
  }

  func testEmptyRestoreStillRefreshesEntitlements() async {
    let platform = Platform()
    let store = await connectedStore(platform: platform)
    StubProtocol.enqueue(status: 200, body: snapshot)
    await store.restore()
    XCTAssertEqual(StubProtocol.requests.count, 2)
    XCTAssertTrue(StubProtocol.requests.allSatisfy { $0.httpMethod == "GET" })
  }

  func testExpiredSessionCannotConnect() async {
    let store = PurchaseStore(loader: StubLoader(expiresAt: .distantPast), platform: Platform())
    await store.connect(endpoint: "https://host.example/session", loginToken: "temporary-login")
    XCTAssertFalse(store.isConnected)
    XCTAssertTrue(store.status.contains("expired"))
    XCTAssertFalse(store.isBusy)
  }

  func testDisconnectReleasesSessionAndCancelsUpdates() async throws {
    let platform = Platform()
    let store = await connectedStore(platform: platform)
    store.disconnect()
    platform.emit(platform.purchase)
    await Task.yield()
    XCTAssertFalse(store.isConnected)
    XCTAssertNil(store.expiresAt)
    XCTAssertTrue(store.entitlements.isEmpty)
    XCTAssertTrue(platform.finished.isEmpty)
    XCTAssertEqual(StubProtocol.requests.count, 1)
  }

  private func connectedStore(platform: Platform) async -> PurchaseStore {
    StubProtocol.enqueue(status: 200, body: snapshot)
    let store = PurchaseStore(loader: StubLoader(), platform: platform, makeClient: { config in
      try IAPStackClient(config: IAPStackConfig(baseUri: config.baseUri,
        applicationId: config.applicationId, customerToken: config.customerToken,
        retryPolicy: .init(maxAttempts: 1)), session: makeSession())
    })
    await store.connect(endpoint: "https://host.example/session", loginToken: "temporary-login")
    XCTAssertTrue(store.isConnected)
    return store
  }

  private func eventually(_ predicate: () -> Bool) async throws {
    for _ in 0..<200 {
      if predicate() { return }
      try await Task.sleep(nanoseconds: 10_000_000)
    }
    XCTFail("Expected asynchronous purchase state")
  }
}

private struct StubLoader: CustomerSessionLoading {
  var expiresAt = Date().addingTimeInterval(600)
  var externalCustomerId = customer
  func load(endpoint: URL, loginToken: String) async throws -> IAPStackCustomerSession {
    IAPStackCustomerSession(baseURL: URL(string: "https://iap.example")!, applicationId: "app",
      externalCustomerId: externalCustomerId, customerToken: "short-lived", expiresAt: expiresAt)
  }
}

private final class Platform: AppleIAPPlatform, @unchecked Sendable {
  let product = AppleProduct(id: "premium_lifetime", kind: .nonConsumable, title: "Lifetime",
    description: "Test", price: "$1", rawPrice: 1, currencyCode: "USD")
  let purchase = ApplePurchase(transactionId: "tx-1", productId: "premium_lifetime",
    signedTransaction: "a.b.c", status: .purchased, pendingCompletion: true, appAccountToken: customer, errorCode: nil)
  var cancelPurchase = false
  var pendingPurchase = false
  var restored: [ApplePurchase] = []
  private let lock = NSLock()
  private var completions: [String] = []
  private var continuation: AsyncStream<ApplePurchase>.Continuation?
  var finished: [String] { lock.withLock { completions } }
  var purchaseUpdates: AsyncStream<ApplePurchase> {
    AsyncStream { continuation in lock.withLock { self.continuation = continuation } }
  }
  func emit(_ purchase: ApplePurchase) { _ = lock.withLock { continuation?.yield(purchase) } }
  func isAvailable() async throws -> Bool { true }
  func queryProducts(productIds: Set<String>) async throws -> AppleProductQuery {
    AppleProductQuery(products: [product], notFoundProductIds: [])
  }
  func launchPurchase(product: AppleProduct, appAccountToken: String) async throws -> ApplePurchase? {
    if cancelPurchase { throw AppleIAPStackError(code: "purchase_cancelled", message: "Cancelled", userCancelled: true) }
    return pendingPurchase ? nil : purchase
  }
  func restorePurchases() async throws -> [ApplePurchase] { restored }
  func completePurchase(_ purchase: ApplePurchase) async throws { lock.withLock { completions.append(purchase.transactionId) } }
}

private func makeSession() -> URLSession {
  let config = URLSessionConfiguration.ephemeral
  config.protocolClasses = [StubProtocol.self]
  return URLSession(configuration: config)
}

private final class StubProtocol: URLProtocol {
  private static let lock = NSLock()
  private static var responses: [(Int, String, Bool)] = []
  private static var suspendedResponse: (() -> Void)?
  static var hasSuspendedResponse: Bool { lock.withLock { suspendedResponse != nil } }
  static func releaseResponse() {
    let response = lock.withLock { let response = suspendedResponse; suspendedResponse = nil; return response }
    response?()
  }
  private static var recorded: [URLRequest] = []
  static var requests: [URLRequest] { lock.withLock { recorded } }
  static func reset() { lock.withLock { responses = []; recorded = []; suspendedResponse = nil } }
  static func enqueue(status: Int, body: String, suspend: Bool = false) { lock.withLock { responses.append((status, body, suspend)) } }
  override class func canInit(with request: URLRequest) -> Bool { true }
  override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
  override func startLoading() {
    let result: (Int, String, Bool)? = Self.lock.withLock {
      var copy = request
      if copy.httpBody == nil, let stream = copy.httpBodyStream {
        stream.open()
        defer { stream.close() }
        var bytes = [UInt8](repeating: 0, count: 1024)
        var body = Data()
        while stream.hasBytesAvailable {
          let count = stream.read(&bytes, maxLength: bytes.count)
          if count <= 0 { break }
          body.append(bytes, count: count)
        }
        copy.httpBody = body
      }
      Self.recorded.append(copy)
      return Self.responses.isEmpty ? nil : Self.responses.removeFirst()
    }
    guard let (status, body, suspend) = result else {
      client?.urlProtocol(self, didFailWithError: URLError(.badServerResponse))
      return
    }
    let deliver = { [self] in
      client?.urlProtocol(self, didReceive: HTTPURLResponse(url: request.url!, statusCode: status,
        httpVersion: nil, headerFields: ["Content-Type": "application/json"])!, cacheStoragePolicy: .notAllowed)
      client?.urlProtocol(self, didLoad: Data(body.utf8))
      client?.urlProtocolDidFinishLoading(self)
    }
    if suspend { Self.lock.withLock { Self.suspendedResponse = deliver } } else { deliver() }
  }
  override func stopLoading() {}
}
