/// Google Play product kinds supported by IAPStack's current server slice.
enum GooglePlayProductKind {
  /// Durable one-time product that must not be consumed.
  nonConsumable('non_consumable'),

  /// Auto-renewing or prepaid subscription.
  subscription('subscription');

  const GooglePlayProductKind(this.evidenceValue);

  /// Value expected by the IAPStack Google Play evidence contract.
  final String evidenceValue;
}
