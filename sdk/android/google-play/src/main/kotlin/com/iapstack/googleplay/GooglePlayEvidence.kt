package com.iapstack.googleplay

import com.iapstack.core.InternalIapStackApi
import com.iapstack.core.PurchaseSubmission
import com.iapstack.core.requireExternalCustomerId

/** Play Billing's limit for `obfuscatedAccountId`, which carries the customer ID. */
private const val MAX_OBFUSCATED_ACCOUNT_ID_LENGTH = 64

/**
 * Applies core's shared customer ID rule ([requireExternalCustomerId], the
 * server's identifier rule) plus Play's own 64-character `obfuscatedAccountId`
 * limit. It runs before Play Billing opens: an ID the server rejects would be
 * charged and then never produce an entitlement.
 */
@OptIn(InternalIapStackApi::class)
internal fun requireGooglePlayCustomerId(value: String) {
  requireExternalCustomerId(value)
  require(value.length <= MAX_OBFUSCATED_ACCOUNT_ID_LENGTH) {
    "externalCustomerId must be at most 64 characters, the Google Play obfuscatedAccountId limit"
  }
}

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
    requireGooglePlayCustomerId(externalCustomerId)
    if (productId.isBlank()) {
      throw IllegalArgumentException("productId must not be empty")
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
