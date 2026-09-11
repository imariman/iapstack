import Foundation

/// Abstraction for StoreKit operations used by IAPStack companion flows.
public protocol AppleIAPPlatform: Sendable {
  /// Emits StoreKit transaction updates.
  var purchaseUpdates: AsyncStream<ApplePurchase> { get }

  /// Reports if StoreKit is available.
  func isAvailable() async throws -> Bool

  /// Queries catalog metadata for configured product IDs.
  func queryProducts(productIds: Set<String>) async throws -> AppleProductQuery

  /// Launches StoreKit checkout with `appAccountToken`.
  func launchPurchase(product: AppleProduct, appAccountToken: String) async throws

  /// Restores previously owned StoreKit rows.
  func restorePurchases() async throws -> [ApplePurchase]

  /// Finishes one StoreKit transaction.
  func completePurchase(_ purchase: ApplePurchase) async throws
}
