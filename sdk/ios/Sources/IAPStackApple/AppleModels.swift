import Foundation

/// Apple product kinds supported by IAPStack for the iOS SDK.
public enum AppleProductKind: String, Sendable {
  /// Durable non-consumable product.
  case nonConsumable = "non_consumable"

  /// Auto-renewable subscription product.
  case subscription = "subscription"
}

/// Localized product metadata returned from StoreKit 2.
public struct AppleProduct: Sendable {
  /// Creates one product metadata row.
  public init(
    id: String,
    kind: AppleProductKind,
    title: String,
    description: String,
    price: String,
    rawPrice: Double,
    currencyCode: String,
  ) {
    self.id = id
    self.kind = kind
    self.title = title
    self.description = description
    self.price = price
    self.rawPrice = rawPrice
    self.currencyCode = currencyCode
  }

  /// Apple product identifier.
  public let id: String

  /// Expected IAPStack verification route.
  public let kind: AppleProductKind

  /// Localized product title.
  public let title: String

  /// Localized product description.
  public let description: String

  /// Localized price string.
  public let price: String

  /// Price in major units.
  public let rawPrice: Double

  /// ISO-4217 currency.
  public let currencyCode: String
}

/// Bundle returned from one StoreKit 2 product query.
public struct AppleProductQuery: Sendable {
  /// Creates one product-query response.
  public init(products: [AppleProduct], notFoundProductIds: Set<String>) {
    self.products = products
    self.notFoundProductIds = notFoundProductIds
  }

  /// Product metadata available from the store.
  public let products: [AppleProduct]

  /// Requested IDs that were not known by StoreKit 2.
  public let notFoundProductIds: Set<String>
}

/// Current StoreKit 2 transaction state mirrored for companion flows.
public struct ApplePurchase: Sendable {
  /// Creates one store transaction row for verification.
  public init(
    transactionId: String,
    productId: String,
    signedTransaction: String,
    status: ApplePurchaseStatus,
    pendingCompletion: Bool,
    appAccountToken: String?,
    errorCode: String?,
  ) {
    self.transactionId = transactionId
    self.productId = productId
    self.signedTransaction = signedTransaction
    self.status = status
    self.pendingCompletion = pendingCompletion
    self.appAccountToken = appAccountToken
    self.errorCode = errorCode
  }

  /// Transaction identifier from StoreKit.
  public let transactionId: String

  /// Store product identifier.
  public let productId: String

  /// Signed JWS payload suitable for IAPStack verification.
  public let signedTransaction: String

  /// Store-side status.
  public let status: ApplePurchaseStatus

  /// Whether StoreKit still expects explicit finish.
  public let pendingCompletion: Bool

  /// App account token provided as UUID string, if any.
  public let appAccountToken: String?

  /// Optional error code from store verification.
  public let errorCode: String?

  /// Whether the payload can be forwarded to IAPStack.
  public var canVerify: Bool {
    (status == .purchased || status == .restored) &&
      !signedTransaction.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty &&
      signedTransaction.split(separator: ".").count == 3
  }
}

/// Store-side status mapped to deterministic companion values.
public enum ApplePurchaseStatus: String, Sendable {
  /// Waiting for system or user interaction.
  case pending
  /// User completed StoreKit checkout.
  case purchased
  /// Transaction was restored from app history.
  case restored
  /// User cancelled purchase UI.
  case cancelled
  /// StoreKit reported a recoverable failure.
  case failed
}

/// Evidence model sent to IAPStack from StoreKit 2.
public struct ApplePurchaseEvidence: Sendable {
  /// Creates one StoreKit evidence tuple.
  public init(signedTransaction: String, productKind: AppleProductKind) throws {
    if signedTransaction.split(separator: ".").count != 3 {
      throw AppleIAPStackError(
        code: "invalid_signed_transaction",
        message: "StoreKit signed transaction is not a compact JWS",
      )
    }
    self.signedTransaction = signedTransaction
    self.productKind = productKind
  }

  /// Raw compact JWS transaction token.
  public let signedTransaction: String

  /// Product route used by IAPStack.
  public let productKind: AppleProductKind

  /// Encodes contract payload expected by IAPStack.
  public func toDictionary() -> [String: String] {
    [
      "signed_transaction": signedTransaction,
      "product_kind": productKind.rawValue,
    ]
  }

  /// Converts to a provider-neutral submission for one product.
  public func toSubmission(
    externalCustomerId: String,
    productId: String,
  ) throws -> PurchaseSubmission {
    try PurchaseSubmission(
      externalCustomerId: externalCustomerId,
      claimedProducts: [productId],
      evidence: toDictionary(),
      customerBindings: [
        CustomerBinding(
          kind: "storefront",
          value: "apple",
        ),
      ],
    )
  }
}
