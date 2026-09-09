package com.iapstack.googleplay

import com.iapstack.core.PurchaseSubmission

/**
 * Exact Google Play purchase-token evidence accepted by IAPStack.
 */
data class GooglePlayPurchaseEvidence(
  val purchaseToken: String,
  val productKind: GooglePlayProductKind,
) {
  init {
    if (purchaseToken.trim().isEmpty()) {
      throw GooglePlayIapStackException(
        code = "invalid_purchase_token",
        message = "Google Play purchase token is empty",
      )
    }
  }

  /**
   * Encodes the versioned Google Play evidence object.
   */
  fun toJson(): Map<String, Any?> = mapOf(
    "purchase_token" to purchaseToken,
    "product_kind" to productKind.evidenceValue,
  )

  /**
   * Creates a provider-neutral submission without exposing the token elsewhere.
   */
  fun toSubmission(
    externalCustomerId: String,
    productId: String,
  ): PurchaseSubmission {
    if (externalCustomerId.isBlank() || productId.isBlank()) {
      throw IllegalArgumentException("externalCustomerId and productId must not be empty")
    }
    return PurchaseSubmission(
      externalCustomerId = externalCustomerId,
      claimedProducts = listOf(productId),
      evidence = toJson(),
    )
  }

  override fun toString() =
    "GooglePlayPurchaseEvidence(productKind=${productKind.name}, purchaseToken=<redacted>)"
}
