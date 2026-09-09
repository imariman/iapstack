package com.iapstack.huawei

/**
 * Huawei product kinds supported by IAPStack v0.1.
 */
enum class HuaweiProductKind(val priceType: Int, val evidenceValue: String) {
  /**
   * Durable one-time product.
   */
  NON_CONSUMABLE(priceType = 1, evidenceValue = "non_consumable"),

  /**
   * Auto-renewable subscription.
   */
  SUBSCRIPTION(priceType = 2, evidenceValue = "subscription");
}
