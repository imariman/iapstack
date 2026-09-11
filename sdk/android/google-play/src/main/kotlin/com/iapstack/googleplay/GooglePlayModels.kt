package com.iapstack.googleplay

/**
 * Current state of one Google Play purchase update.
 */
enum class GooglePlayPurchaseStatus {
  /**
   * Payment is waiting for the customer or provider to complete it.
   */
  PENDING,

  /**
   * Payment completed and the purchase can be verified by IAPStack.
   */
  PURCHASED,

  /**
   * An owned purchase was recovered from Google Play.
   */
  RESTORED,

  /**
   * The customer cancelled purchase UI.
   */
  CANCELLED,

  /**
   * Google Play reported a safe purchase failure.
   */
  FAILED,
}

/**
 * Opaque Google Play purchase update safe for explicit server verification.
 */
data class GooglePlayPurchase(
  val purchaseToken: String,
  val productIds: List<String>,
  val status: GooglePlayPurchaseStatus,
  val isAcknowledged: Boolean,
  val obfuscatedAccountId: String? = null,
  val errorCode: String? = null,
) {
  /**
   * Whether this update contains evidence that can be sent to IAPStack.
   */
  val canVerify: Boolean
    get() = when (status) {
      GooglePlayPurchaseStatus.PENDING, GooglePlayPurchaseStatus.PURCHASED,
      GooglePlayPurchaseStatus.RESTORED -> true
      else -> false
    }

  override fun toString(): String =
    "GooglePlayPurchase(status=${status.name}, products=$productIds, purchaseToken=<redacted>)"
}

/**
 * Google Play recurrence behavior for one subscription pricing phase.
 */
enum class GooglePlayPricingRecurrence {
  /**
   * The phase repeats for [billingCycleCount] cycles.
   */
  FINITE,

  /**
   * The phase repeats until the subscription is cancelled.
   */
  INFINITE,

  /**
   * The phase is charged once.
   */
  NON_RECURRING,
}

/**
 * One exact Google Play pricing phase.
 */
data class GooglePlayPricingPhase(
  val billingCycleCount: Int,
  val billingPeriod: String,
  val formattedPrice: String,
  val priceMicros: Int,
  val currencyCode: String,
  val recurrence: GooglePlayPricingRecurrence,
)

/**
 * Display-safe Google Play product, base plan, or subscription offer.
 */
data class GooglePlayProduct(
  val id: String,
  val kind: GooglePlayProductKind,
  val title: String,
  val description: String,
  val price: String,
  val priceMicros: Int,
  val currencyCode: String,
  val offerToken: String? = null,
  val basePlanId: String? = null,
  val offerId: String? = null,
  val offerTags: List<String> = emptyList(),
  val pricingPhases: List<GooglePlayPricingPhase> = emptyList(),
) {
  /**
   * Stable in-memory key used to select one queried product offer.
   */
  val selectionKey: String
    get() = "$id\u0000${offerToken.orEmpty()}"

  /**
   * Whether the row contains everything needed to open checkout.
   */
  val isPurchasable: Boolean
    get() = when {
      id.isEmpty() || price.isEmpty() || priceMicros < 0 ||
        !Regex("^[A-Z]{3}$").matches(currencyCode) -> false
      kind == GooglePlayProductKind.NON_CONSUMABLE ->
        offerToken == null && basePlanId == null && pricingPhases.isEmpty()
      kind == GooglePlayProductKind.SUBSCRIPTION ->
        !offerToken.isNullOrEmpty() &&
          !basePlanId.isNullOrEmpty() &&
          pricingPhases.isNotEmpty()
      else -> false
    }
}

/**
 * One bounded Google Play product query result.
 */
data class GooglePlayProductQuery(
  val products: List<GooglePlayProduct>,
  val notFoundProductIds: Set<String>,
)
