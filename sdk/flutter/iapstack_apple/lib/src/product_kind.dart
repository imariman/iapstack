/// Apple product kinds supported by the current IAPStack server slice.
enum AppleProductKind {
  /// Durable one-time product.
  nonConsumable('non_consumable'),

  /// Auto-renewable subscription.
  subscription('subscription');

  const AppleProductKind(this.evidenceValue);

  /// Value expected by the IAPStack Apple evidence contract.
  final String evidenceValue;
}
