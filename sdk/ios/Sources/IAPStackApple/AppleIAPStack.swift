import Foundation

/// High-level companion around an `IAPStackClient` and StoreKit 2 platform.
public final class AppleIAPStack {
  /// Creates a companion with explicit product catalog and optional explicit platform.
  public init(
    client: IAPStackClient,
    productKinds: [String: AppleProductKind],
    platform: AppleIAPPlatform? = nil,
  ) {
    self.client = client
    self.productKinds = productKinds
    for (productId, kind) in productKinds {
      if productId.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
        preconditionFailure("productKinds must not contain an empty product identifier")
      }
      _ = kind
    }
    self.platform = platform ?? AppleStoreKitPlatform()
  }

  private let client: IAPStackClient
  private let productKinds: [String: AppleProductKind]
  private let platform: AppleIAPPlatform

  /// StoreKit 2 updates consumed by callers.
  public var purchaseUpdates: AsyncStream<ApplePurchase> {
    platform.purchaseUpdates
  }

  /// Whether StoreKit is available on the current runtime.
  public func isAvailable() async throws -> Bool {
    try await platform.isAvailable()
  }

  /// Queries products and validates catalog configuration against configured kinds.
  public func queryProducts(_ productIds: Set<String>) async throws -> AppleProductQuery {
    for productId in productIds where !productKinds.keys.contains(productId) {
      throw AppleIAPStackError(
        code: "unknown_product",
        message: "Product id \(productId) is not configured",
      )
    }
    let query = try await platform.queryProducts(productIds: productIds)
    for product in query.products where productKinds[product.id] != product.kind {
      throw AppleIAPStackError(
        code: "product_kind_mismatch",
        message: "App Store product \(product.id) has an unexpected kind",
      )
    }
    return query
  }

  /// Launches checkout with `externalCustomerId` as `appAccountToken`.
  public func launchPurchase(externalCustomerId: String, product: AppleProduct) async throws {
    try validateExternalCustomerId(externalCustomerId)
    let expected = productKinds[product.id]
    guard expected == product.kind else {
      throw AppleIAPStackError(
        code: "product_kind_mismatch",
        message: "Product kind does not match local catalog",
      )
    }
    try await platform.launchPurchase(product: product, appAccountToken: externalCustomerId)
  }

  /// Verifies one purchase and finishes it only after successful verification.
  public func verifyPurchase(
    externalCustomerId: String,
    purchase: ApplePurchase,
    requestId: String? = nil,
  ) async throws -> VerificationResult {
    try validateExternalCustomerId(externalCustomerId)
    let submission = try submission(
      externalCustomerId: externalCustomerId,
      purchase: purchase,
    )
    let result = try await client.verifyPurchase(submission, requestId: requestId)
    if purchase.pendingCompletion {
      try await platform.completePurchase(purchase)
    }
    return result
  }

  /// Restores StoreKit history and verifies one bounded set per API request.
  public func restorePurchases(
    externalCustomerId: String,
    requestId: String? = nil,
  ) async throws -> RestoreResult {
    try validateExternalCustomerId(externalCustomerId)
    let purchases = try await platform.restorePurchases()
    var submissions: [PurchaseSubmission] = []
    var seen: Set<String> = []
    for purchase in purchases where purchase.canVerify {
      let appAccount = purchase.appAccountToken?.lowercased()
      if appAccount != externalCustomerId || !productKinds.keys.contains(purchase.productId) {
        continue
      }
      if seen.contains(purchase.transactionId) {
        continue
      }
      seen.insert(purchase.transactionId)
      submissions.append(try submission(externalCustomerId: externalCustomerId, purchase: purchase))
    }
    if submissions.isEmpty {
      return RestoreResult(results: [])
    }
    var results: [VerificationResult] = []
    for start in stride(from: 0, to: submissions.count, by: 100) {
      let batch = Array(submissions[start..<min(start + 100, submissions.count)])
      let batchRequestId = batchRequestId(base: requestId, index: start / 100)
      results.append(contentsOf: try await client.restorePurchases(
        batch,
        requestId: batchRequestId,
      ).results)
    }
    return RestoreResult(results: results)
  }

  /// Loads entitlement projections directly from IAPStack.
  public func getEntitlements(_ externalCustomerId: String, requestId: String? = nil) async throws
    -> EntitlementSnapshot {
    try validateExternalCustomerId(externalCustomerId)
    return try await client.getEntitlements(externalCustomerId, requestId: requestId)
  }

  private func submission(externalCustomerId: String, purchase: ApplePurchase) throws -> PurchaseSubmission {
    guard purchase.canVerify else {
      if purchase.status == .cancelled {
        throw AppleIAPStackError(code: "purchase_cancelled", message: "The purchase was cancelled")
      }
      throw AppleIAPStackError(code: "purchase_not_verifiable", message: "The purchase cannot be verified yet")
    }
    let normalizedAccount = purchase.appAccountToken?.lowercased()
    if normalizedAccount != externalCustomerId {
      throw AppleIAPStackError(
        code: "customer_binding_mismatch",
        message: "App Store transaction did not match current customer",
      )
    }
    guard let productKind = productKinds[purchase.productId] else {
      throw AppleIAPStackError(code: "unknown_product", message: "Product is not configured")
    }
    return try ApplePurchaseEvidence(
      signedTransaction: purchase.signedTransaction,
      productKind: productKind,
    ).toSubmission(externalCustomerId: externalCustomerId, productId: purchase.productId)
  }

  private func validateExternalCustomerId(_ externalCustomerId: String) throws {
    let normalized = externalCustomerId.lowercased()
    let pattern = #/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/#
    guard pattern.wholeMatch(in: normalized) != nil else {
      throw AppleIAPStackError(
        code: "invalid_external_customer_id",
        message: "externalCustomerId must be lowercase UUID",
      )
    }
  }

  private func batchRequestId(base: String?, index: Int) -> String? {
    guard let value = base?.trimmingCharacters(in: .whitespacesAndNewlines), !value.isEmpty else {
      return nil
    }
    if index == 0 {
      return value
    }
    return "\(value)-\(index + 1)"
  }
}
