import 'package:iapstack_huawei/src/product_kind.dart';

/// One product returned by Huawei AppGallery for the current account and locale.
final class HuaweiProduct {
  /// Creates one immutable Huawei catalog product.
  const HuaweiProduct({
    required this.id,
    required this.kind,
    required this.title,
    required this.description,
    required this.price,
    required this.priceMicros,
    required this.currency,
    required this.status,
    this.originalPrice,
    this.originalPriceMicros,
    this.promotionalPrice,
    this.promotionalPriceMicros,
    this.subscriptionPeriod,
    this.freeTrialPeriod,
  });

  /// AppGallery Connect product ID.
  final String id;

  /// IAPStack product kind configured for [id].
  final HuaweiProductKind kind;

  /// Localized product name.
  final String title;

  /// Localized product description.
  final String description;

  /// Localized current price including the currency symbol.
  final String price;

  /// Current price in millionths of [currency].
  final int priceMicros;

  /// ISO 4217 currency code.
  final String currency;

  /// Huawei product status. Only status `0` can start a new purchase.
  final int status;

  /// Localized original price, when Huawei reports a promotion.
  final String? originalPrice;

  /// Original price in millionths of [currency].
  final int? originalPriceMicros;

  /// Localized subscription promotional price.
  final String? promotionalPrice;

  /// Subscription promotional price in millionths of [currency].
  final int? promotionalPriceMicros;

  /// ISO 8601 subscription billing period.
  final String? subscriptionPeriod;

  /// ISO 8601 subscription free-trial period.
  final String? freeTrialPeriod;

  /// Whether AppGallery permits starting a new purchase for this product.
  bool get isPurchasable => status == 0;

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is HuaweiProduct &&
          id == other.id &&
          kind == other.kind &&
          title == other.title &&
          description == other.description &&
          price == other.price &&
          priceMicros == other.priceMicros &&
          currency == other.currency &&
          status == other.status &&
          originalPrice == other.originalPrice &&
          originalPriceMicros == other.originalPriceMicros &&
          promotionalPrice == other.promotionalPrice &&
          promotionalPriceMicros == other.promotionalPriceMicros &&
          subscriptionPeriod == other.subscriptionPeriod &&
          freeTrialPeriod == other.freeTrialPeriod;

  @override
  int get hashCode => Object.hash(
        id,
        kind,
        title,
        description,
        price,
        priceMicros,
        currency,
        status,
        originalPrice,
        originalPriceMicros,
        promotionalPrice,
        promotionalPriceMicros,
        subscriptionPeriod,
        freeTrialPeriod,
      );
}

/// Result of querying one configured set of Huawei products.
final class HuaweiProductQuery {
  /// Creates an immutable query result.
  HuaweiProductQuery({
    required List<HuaweiProduct> products,
    required Set<String> notFoundProductIds,
  })  : products = List<HuaweiProduct>.unmodifiable(products),
        notFoundProductIds = Set<String>.unmodifiable(notFoundProductIds);

  /// Products returned by AppGallery in requested product-ID order.
  final List<HuaweiProduct> products;

  /// Requested product IDs not returned by AppGallery.
  final Set<String> notFoundProductIds;
}
