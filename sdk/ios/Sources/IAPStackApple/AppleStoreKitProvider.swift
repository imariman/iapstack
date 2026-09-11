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
      let unfinished = Task.detached { [state] in
        for await result in Transaction.unfinished {
          if let mapped = await Self.mapUpdate(result, status: .purchased, state: state) {
            continuation.yield(mapped)
          }
        }
      }
      let updates = Task.detached { [state] in
        for await result in Transaction.updates {
          if let mapped = await Self.mapUpdate(result, status: .purchased, state: state) {
            continuation.yield(mapped)
          }
        }
      }
      continuation.onTermination = { _ in
        unfinished.cancel()
        updates.cancel()
      }
    }
  }

  public func isAvailable() async throws -> Bool {
    true
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
      default:
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
          currencyCode: currencyCode(for: product),
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

    let options: Set<Product.PurchaseOption> = [.appAccountToken(appAccountUUID)]
    let result = try await storeProduct.purchase(options: options)
    switch result {
    case .success(let verification):
      _ = try await mapVerification(verification, status: .purchased)
    case .userCancelled:
      throw AppleIAPStackError(
        code: "purchase_cancelled",
        message: "The purchase was cancelled",
        userCancelled: true,
      )
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
    try await AppStore.sync()
    var purchases: [ApplePurchase] = []
    var seen = Set<String>()
    for await result in Transaction.all {
      guard case .verified = result else {
        continue
      }
      let purchase = try await mapVerification(result, status: .restored)
      if seen.insert(purchase.transactionId).inserted {
        purchases.append(purchase)
      }
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
    await transaction.finish()
  }

  private func mapVerification(
    _ verification: StoreKit.VerificationResult<Transaction>,
    status: ApplePurchaseStatus,
  ) async throws -> ApplePurchase {
    switch verification {
    case .verified(let transaction):
      let purchase = makeApplePurchase(
        from: transaction,
        signedTransaction: verification.jwsRepresentation,
        status: status,
      )
      await state.setPending(transaction, id: purchase.transactionId)
      return purchase
    case .unverified(_, let error):
      throw AppleIAPStackError(
        code: "storekit_verification_failed",
        message: "StoreKit could not verify the transaction",
        cause: error,
      )
    }
  }

  private func makeApplePurchase(
    from transaction: Transaction,
    signedTransaction: String,
    status: ApplePurchaseStatus,
  ) -> ApplePurchase {
    ApplePurchase(
      transactionId: String(describing: transaction.id),
      productId: transaction.productID,
      signedTransaction: signedTransaction,
      status: status,
      pendingCompletion: true,
      appAccountToken: transaction.appAccountToken?.uuidString.lowercased(),
      errorCode: nil,
    )
  }

  private static func mapUpdate(
    _ result: StoreKit.VerificationResult<Transaction>,
    status: ApplePurchaseStatus,
    state: State,
  ) async -> ApplePurchase? {
    switch result {
    case .verified(let transaction):
      let purchase = ApplePurchase(
        transactionId: String(describing: transaction.id),
        productId: transaction.productID,
        signedTransaction: result.jwsRepresentation,
        status: status,
        pendingCompletion: true,
        appAccountToken: transaction.appAccountToken?.uuidString.lowercased(),
        errorCode: nil,
      )
      await state.setPending(transaction, id: purchase.transactionId)
      return purchase
    case .unverified(_, let error):
      return ApplePurchase(
        transactionId: "",
        productId: "",
        signedTransaction: "",
        status: .failed,
        pendingCompletion: false,
        appAccountToken: nil,
        errorCode: "\(error)",
      )
    }
  }

  private func currencyCode(for product: Product) -> String {
    if #available(iOS 16.0, macOS 13.0, *) {
      return product.priceFormatStyle.locale.currency?.identifier
        ?? Locale.current.currency?.identifier
        ?? "USD"
    }
    return Locale.current.currencyCode ?? "USD"
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
