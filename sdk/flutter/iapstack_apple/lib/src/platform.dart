import 'package:iapstack_apple/src/product_kind.dart';

/// Current state of one StoreKit 2 purchase update.
enum ApplePurchaseStatus {
  /// Payment is waiting for the customer or provider to complete it.
  pending,

  /// Payment completed and the transaction can be verified by IAPStack.
  purchased,

  /// An owned transaction was recovered from StoreKit.
  restored,

  /// The customer cancelled purchase UI.
  cancelled,

  /// StoreKit reported a safe purchase failure.
  failed,
}

/// Display-safe StoreKit 2 product details.
final class AppleProduct {
  /// Creates immutable product details returned by StoreKit.
  const AppleProduct({
    required this.id,
    required this.kind,
    required this.title,
    required this.description,
    required this.price,
    required this.rawPrice,
    required this.currencyCode,
  });

  /// Provider product identifier configured in App Store Connect and IAPStack.
  final String id;

  /// Product kind reported by StoreKit 2.
  final AppleProductKind kind;

  /// Localized provider title.
  final String title;

  /// Localized provider description.
  final String description;

  /// Localized formatted price.
  final String price;

  /// Price in major currency units.
  final double rawPrice;

  /// ISO 4217 provider currency code.
  final String currencyCode;
}

/// One bounded StoreKit product query result.
final class AppleProductQuery {
  /// Creates one immutable query result.
  AppleProductQuery({
    required List<AppleProduct> products,
    required Set<String> notFoundProductIds,
  }) : products = List<AppleProduct>.unmodifiable(products),
       notFoundProductIds = Set<String>.unmodifiable(notFoundProductIds);

  /// Products returned by StoreKit.
  final List<AppleProduct> products;

  /// Requested product identifiers StoreKit could not resolve.
  final Set<String> notFoundProductIds;
}

/// Opaque StoreKit 2 purchase update safe for explicit server verification.
final class ApplePurchase {
  /// Creates one immutable provider update without persisting its JWS.
  const ApplePurchase({
    required this.transactionId,
    required this.productId,
    required this.signedTransaction,
    required this.status,
    required this.pendingCompletion,
    this.appAccountToken,
    this.errorCode,
  });

  /// StoreKit transaction identifier used only for in-memory completion routing.
  final String transactionId;

  /// Provider product associated with this transaction.
  final String productId;

  /// Compact StoreKit 2 JWS sent only to IAPStack.
  final String signedTransaction;

  /// Customer UUID supplied as `appAccountToken` during checkout.
  final String? appAccountToken;

  /// Current device-side purchase status.
  final ApplePurchaseStatus status;

  /// Whether StoreKit still requires the transaction to be finished.
  final bool pendingCompletion;

  /// Safe plugin error code for failed purchase updates.
  final String? errorCode;

  /// Whether this update contains evidence that can be sent to IAPStack.
  bool get canVerify =>
      status == ApplePurchaseStatus.purchased ||
      status == ApplePurchaseStatus.restored;

  @override
  String toString() =>
      'ApplePurchase(status: ${status.name}, productId: $productId, signedTransaction: <redacted>)';
}

/// Testable boundary around Flutter's official StoreKit 2 implementation.
abstract interface class AppleIapPlatform {
  /// Emits transaction changes from process startup in provider order.
  Stream<ApplePurchase> get purchaseUpdates;

  /// Reports whether StoreKit purchases are available for the installed app.
  Future<bool> isAvailable();

  /// Queries current localized details for provider product identifiers.
  Future<AppleProductQuery> queryProducts(Set<String> productIds);

  /// Opens StoreKit purchase UI using the required UUID customer binding.
  Future<void> launchPurchase({
    required AppleProduct product,
    required String appAccountToken,
  });

  /// Synchronizes App Store ownership and returns the current transaction history.
  Future<List<ApplePurchase>> restorePurchases();

  /// Finishes one StoreKit transaction after authoritative server verification.
  Future<void> completePurchase(ApplePurchase purchase);
}
