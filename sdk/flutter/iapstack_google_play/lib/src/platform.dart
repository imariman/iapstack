import 'package:iapstack_google_play/src/product_kind.dart';

/// Current state of one Google Play purchase update.
enum GooglePlayPurchaseStatus {
  /// Payment is waiting for the customer or provider to complete it.
  pending,

  /// Payment completed and the purchase can be verified by IAPStack.
  purchased,

  /// An owned purchase was recovered from Google Play.
  restored,

  /// The customer cancelled purchase UI.
  cancelled,

  /// Google Play reported a safe purchase failure.
  failed,
}

/// Display-safe Google Play product or subscription offer.
final class GooglePlayProduct {
  /// Creates immutable product details returned by Play Billing.
  const GooglePlayProduct({
    required this.id,
    required this.kind,
    required this.title,
    required this.description,
    required this.price,
    required this.rawPrice,
    required this.currencyCode,
    this.offerToken,
  });

  /// Provider product identifier configured in Play Console and IAPStack.
  final String id;

  /// Product kind reported by Google Play Billing.
  final GooglePlayProductKind kind;

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

  /// Provider subscription offer token, or null for one-time products.
  final String? offerToken;

  /// Stable in-memory key used to select one queried product offer.
  String get selectionKey => '$id\u0000${offerToken ?? ''}';
}

/// One bounded Google Play product query result.
final class GooglePlayProductQuery {
  /// Creates one immutable query result.
  GooglePlayProductQuery({
    required List<GooglePlayProduct> products,
    required Set<String> notFoundProductIds,
  }) : products = List<GooglePlayProduct>.unmodifiable(products),
       notFoundProductIds = Set<String>.unmodifiable(notFoundProductIds);

  /// Product and offer rows returned by Google Play.
  final List<GooglePlayProduct> products;

  /// Requested product identifiers that Google Play could not resolve.
  final Set<String> notFoundProductIds;
}

/// Opaque Google Play purchase update safe for explicit server verification.
final class GooglePlayPurchase {
  /// Creates one immutable provider update without persisting its purchase token.
  GooglePlayPurchase({
    required this.purchaseToken,
    required List<String> productIds,
    required this.status,
    required this.isAcknowledged,
    this.obfuscatedAccountId,
    this.errorCode,
  }) : productIds = List<String>.unmodifiable(productIds);

  /// Opaque query token sent only to the IAPStack verification API.
  final String purchaseToken;

  /// Provider products associated with this Billing purchase.
  final List<String> productIds;

  /// Current device-side purchase status.
  final GooglePlayPurchaseStatus status;

  /// Customer binding supplied through `applicationUserName` during checkout.
  final String? obfuscatedAccountId;

  /// Whether the purchase snapshot already reports provider acknowledgement.
  final bool isAcknowledged;

  /// Safe plugin error code for failed purchase updates.
  final String? errorCode;

  /// Whether this update contains evidence that can be sent to IAPStack.
  bool get canVerify =>
      status == GooglePlayPurchaseStatus.pending ||
      status == GooglePlayPurchaseStatus.purchased ||
      status == GooglePlayPurchaseStatus.restored;

  @override
  String toString() =>
      'GooglePlayPurchase(status: ${status.name}, products: $productIds, purchaseToken: <redacted>)';
}

/// Testable boundary around Flutter's official Google Play Billing implementation.
abstract interface class GooglePlayIapPlatform {
  /// Emits purchase changes and restored purchases in provider order.
  Stream<GooglePlayPurchase> get purchaseUpdates;

  /// Reports whether Google Play Billing is available for the installed app.
  Future<bool> isAvailable();

  /// Queries current localized details for provider product identifiers.
  Future<GooglePlayProductQuery> queryProducts(Set<String> productIds);

  /// Opens Google Play purchase UI for one previously queried product offer.
  Future<void> launchPurchase({
    required GooglePlayProduct product,
    required String obfuscatedAccountId,
  });

  /// Queries currently owned purchases for an application customer binding.
  Future<List<GooglePlayPurchase>> ownedPurchases({
    required String obfuscatedAccountId,
  });
}
