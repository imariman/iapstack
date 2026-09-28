import Foundation

/// Abstraction for StoreKit operations used by IAPStack companion flows.
public protocol AppleIAPPlatform: Sendable {
  /// Emits StoreKit transaction updates.
  var purchaseUpdates: AsyncStream<ApplePurchase> { get }

  /// Reports whether this user can make App Store payments
  /// (`AppStore.canMakePayments`).
  ///
  /// `false` means purchases are blocked, for example by Screen Time or an MDM
  /// profile. It does not mean StoreKit is missing: product queries and restore
  /// still work.
  func isAvailable() async throws -> Bool

  /// Queries catalog metadata for configured product IDs.
  func queryProducts(productIds: Set<String>) async throws -> AppleProductQuery

  /// Launches StoreKit checkout with `appAccountToken`.
  ///
  /// Returns the completed transaction, or `nil` when StoreKit reports the
  /// purchase as pending (for example Ask to Buy); pending purchases arrive later
  /// through `purchaseUpdates`.
  func launchPurchase(product: AppleProduct, appAccountToken: String) async throws -> ApplePurchase?

  /// Restores previously owned StoreKit rows.
  ///
  /// Rows that StoreKit has not finished yet have `pendingCompletion == true`.
  func restorePurchases() async throws -> [ApplePurchase]

  /// Finishes one StoreKit transaction.
  func completePurchase(_ purchase: ApplePurchase) async throws
}
