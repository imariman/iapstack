/// Huawei product kinds supported by IAPStack v0.1.
enum HuaweiProductKind {
  /// Durable one-time product.
  nonConsumable(priceType: 1, evidenceValue: 'non_consumable'),

  /// Auto-renewable subscription.
  subscription(priceType: 2, evidenceValue: 'subscription');

  const HuaweiProductKind(
      {required this.priceType, required this.evidenceValue});

  /// Huawei IAP `priceType` value.
  final int priceType;

  /// IAPStack Huawei evidence `product_kind` value.
  final String evidenceValue;
}
