package com.iapstack.googleplay

/**
 * Google Play product kinds supported by IAPStack's current server slice.
 */
enum class GooglePlayProductKind(val evidenceValue: String) {
  /**
   * Durable one-time product that must not be consumed.
   */
  NON_CONSUMABLE("non_consumable"),

  /**
   * Auto-renewing or prepaid subscription.
   */
  SUBSCRIPTION("subscription");
}
