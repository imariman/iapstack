import Foundation

#if canImport(StoreKit)
import StoreKit

/// StoreKit 2 companion used by default on Apple platforms.
public final class AppleStoreKitPlatform: AppleIAPPlatform {
  public init() {}

  private actor State {
    var productCache: [String: Product] = [:]
    var pendingTransactions: [String: Transaction] = [:]

    func cache(product: Product) {
      productCache[product.id] = product
    }

    func product(for id: String) -> Product? {
      productCache[id]
    }

    func setPending(_ transaction: Transaction, id: String) {
      pendingTransactions[id] = transaction
    }

    func consumePending(id: String) -> Transaction? {
      defer { pendingTransactions[id] = nil }
      return pendingTransactions[id]
    }
  }

  private let state = State()

  public var purchaseUpdates: AsyncStream<ApplePurchase> {
    AsyncStream { continuation in
      Task.detached {
        for await result in Transaction.updates {
          switch result {
          case .verified(let transaction):
            let mapped = await self.makeApplePurchase(from: transaction, status: .purchased)
            continuation.yield(mapped)
            await self.state.setPending(transaction, id: mapped.transactionId)
          case .unverified(_, let error):
            continuation.yield(
              ApplePurchase(
                transactionId: "",
                productId: "",
                signedTransaction: "",
                status: .failed,
                pendingCompletion: false,
                appAccountToken: nil,
                errorCode: "\(error)",
              ),
            )
          default:
            continuation.yield(
              ApplePurchase(
                transactionId: "",
                productId: "",
                signedTransaction: "",
                status: .failed,
                pendingCompletion: false,
                appAccountToken: nil,
                errorCode: "unknown_update",
              ),
            )
          }
        }
        continuation.finish()
      }
    }
  }

  public func isAvailable() async throws -> Bool {
    return true
  }

  public func queryProducts(productIds: Set<String>) async throws -> AppleProductQuery {
    let products = try await Product.products(for: productIds)
    var mapped: [AppleProduct] = []
    for product in products {
      let kind: AppleProductKind
      switch product.type {
      case .nonConsumable:
        kind = .nonConsumable
      case .autoRenewable:
        kind = .subscription
      case .consumable:
        throw AppleIAPStackError(
          code: "unsupported_product_kind",
          message: "IAPStack does not support consumable products",
        )
      case .nonRenewable:
        throw AppleIAPStackError(
          code: "unsupported_product_kind",
          message: "IAPStack does not support non-renewable products",
        )
      @unknown default:
        throw AppleIAPStackError(
          code: "unsupported_product_kind",
          message: "IAPStack does not support this StoreKit product kind",
        )
      }

      await state.cache(product: product)
      mapped.append(
        AppleProduct(
          id: product.id,
          kind: kind,
          title: product.displayName,
          description: product.description,
          price: product.displayPrice,
          rawPrice: Double(truncating: product.price as NSDecimalNumber),
          currencyCode: "USD",
        ),
      )
    }
    return AppleProductQuery(
      products: mapped,
      notFoundProductIds: productIds.subtracting(Set(mapped.map(\.id))),
    )
  }

  public func launchPurchase(product: AppleProduct, appAccountToken: String) async throws {
    guard let appAccountUUID = UUID(uuidString: appAccountToken) else {
      throw AppleIAPStackError(
        code: "invalid_app_account_token",
        message: "App account token must be a valid UUID",
      )
    }
    guard let storeProduct = await state.product(for: product.id) else {
      throw AppleIAPStackError(
        code: "product_not_queried",
        message: "Query this product before launching purchase",
      )
    }

    let options = [Product.PurchaseOption.appAccountToken(appAccountUUID)]
    let result = try await storeProduct.purchase(options: options)
    switch result {
    case .success(let verification):
      let transaction = try await mapVerification(verification, status: .purchased)
      await state.setPending(transaction, id: transaction.transactionId)
    case .userCancelled:
      throw AppleIAPStackError(code: "purchase_cancelled", message: "The purchase was cancelled")
    case .pending:
      return
    default:
      throw AppleIAPStackError(
        code: "purchase_not_started",
        message: "StoreKit purchase did not start",
      )
    }
  }

  public func restorePurchases() async throws -> [ApplePurchase] {
    var purchases: [ApplePurchase] = []
    for await result in Transaction.currentEntitlements {
      let purchase = try await mapTransactionResult(result, status: .restored)
      purchases.append(purchase)
    }
    return purchases
  }

  public func completePurchase(_ purchase: ApplePurchase) async throws {
    guard purchase.pendingCompletion else {
      return
    }
    guard let transaction = await state.consumePending(id: purchase.transactionId) else {
      return
    }
    try await transaction.finish()
  }

  private func mapTransactionResult(_ result: VerificationResult<Transaction>, status: ApplePurchaseStatus) async throws
    -> ApplePurchase {
    switch result {
    case .verified(let transaction):
      return await makeApplePurchase(from: transaction, status: status)
    case .unverified(_, let error):
      throw AppleIAPStackError(
        code: "storekit_verification_failed",
        message: "StoreKit could not verify the transaction",
        cause: error,
      )
    @unknown default:
      throw AppleIAPStackError(
        code: "storekit_update_failed",
        message: "StoreKit returned an unknown transaction state",
      )
    }
  }

  private func mapVerification(
    _ verification: VerificationResult<Transaction>,
    status: ApplePurchaseStatus,
  ) async throws -> ApplePurchase {
    switch verification {
    case .verified(let transaction):
      return await makeApplePurchase(from: transaction, status: status)
    case .unverified(_, let error):
      throw AppleIAPStackError(
        code: "storekit_verification_failed",
        message: "StoreKit could not verify the transaction",
        cause: error,
      )
    @unknown default:
      throw AppleIAPStackError(
        code: "storekit_update_failed",
        message: "StoreKit returned an unknown transaction state",
      )
    }
  }

  private func makeApplePurchase(
    from transaction: Transaction,
    status: ApplePurchaseStatus,
  ) async -> ApplePurchase {
    let appAccountToken = transaction.appAccountToken?.uuidString.lowercased()
    return ApplePurchase(
      transactionId: String(describing: transaction.id),
      productId: transaction.productID,
      signedTransaction: transaction.jwsRepresentation,
      status: status,
      pendingCompletion: true,
      appAccountToken: appAccountToken,
      errorCode: nil,
    )
  }
}

#else

/// Non-Apple fallback that exposes a consistent error when StoreKit is unavailable.
public final class AppleStoreKitPlatform: AppleIAPPlatform {
  public init() {}

  public var purchaseUpdates: AsyncStream<ApplePurchase> {
    AsyncStream { continuation in
      continuation.finish()
    }
  }

  public func isAvailable() async throws -> Bool {
    throw AppleIAPStackError(code: "storekit_unavailable", message: "StoreKit is not available on this platform")
  }

  public func queryProducts(productIds: Set<String>) async throws -> AppleProductQuery {
    throw AppleIAPStackError(code: "storekit_unavailable", message: "StoreKit is not available on this platform")
  }

  public func launchPurchase(product: AppleProduct, appAccountToken: String) async throws {
    throw AppleIAPStackError(code: "storekit_unavailable", message: "StoreKit is not available on this platform")
  }

  public func restorePurchases() async throws -> [ApplePurchase] {
    throw AppleIAPStackError(code: "storekit_unavailable", message: "StoreKit is not available on this platform")
  }

  public func completePurchase(_ purchase: ApplePurchase) async throws {
    throw AppleIAPStackError(code: "storekit_unavailable", message: "StoreKit is not available on this platform")
  }
}

#endif
