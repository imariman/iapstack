package com.iapstack.huawei

/**
 * One product returned by Huawei AppGallery for the current account and locale.
 */
data class HuaweiProduct(
  val id: String,
  val kind: HuaweiProductKind,
  val title: String,
  val description: String,
  val price: String,
  val priceMicros: Int,
  val currency: String,
  val status: Int,
  val originalPrice: String? = null,
  val originalPriceMicros: Int? = null,
  val promotionalPrice: String? = null,
  val promotionalPriceMicros: Int? = null,
  val subscriptionPeriod: String? = null,
  val freeTrialPeriod: String? = null,
) {
  /**
   * Whether AppGallery allows starting a new purchase for this product.
   */
  val isPurchasable: Boolean
    get() = status == 0
}

/**
 * Result of querying one configured set of Huawei products.
 */
data class HuaweiProductQuery(
  val products: List<HuaweiProduct>,
  val notFoundProductIds: Set<String>,
)
