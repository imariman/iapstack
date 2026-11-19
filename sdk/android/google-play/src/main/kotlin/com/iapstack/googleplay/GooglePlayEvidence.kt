package com.iapstack.googleplay

import com.iapstack.core.PurchaseSubmission

/** Play Billing's limit for `obfuscatedAccountId`, which carries the customer ID. */
private const val MAX_OBFUSCATED_ACCOUNT_ID_LENGTH = 64

/**
 * Requires one non-empty customer identity in valid Unicode of at most 64
 * characters without surrounding whitespace or control characters.
 *
 * This mirrors the server's identifier rule (`validateIdentifier` in
 * `internal/core/identifiers.go`): valid UTF-8, which for a Kotlin string
 * means no unpaired surrogate (UTF-8 encoding would replace it), no C0
 * (U+0000-U+001F), DEL, or C1 (U+0080-U+009F) control character (the
 * [Char.isISOControl] set), and no leading or trailing whitespace as Go's
 * `strings.TrimSpace` defines it. The 64-character limit is Play's own
 * `obfuscatedAccountId` limit. It runs before Play Billing opens: an ID the
 * server rejects would be charged and then never produce an entitlement. The
 * shared cases in `contracts/test-vectors/external-customer-ids.json` pin
 * this copy to the server.
 */
internal fun requireExternalCustomerId(value: String) {
  if (value.isEmpty() ||
    value.length > MAX_OBFUSCATED_ACCOUNT_ID_LENGTH ||
    isGoSpace(value.first()) ||
    isGoSpace(value.last()) ||
    value.any { it.isISOControl() } ||
    hasUnpairedSurrogate(value)
  ) {
    throw IllegalArgumentException(
      "externalCustomerId must be non-empty valid Unicode of at most 64 characters " +
        "without surrounding whitespace or control characters",
    )
  }
}

/**
 * Go's `unicode.IsSpace`, which the server's `strings.TrimSpace` strips. An
 * explicit set keeps the edge check independent of the runtime's Unicode
 * tables, which [String.trim] depends on. Every member is one UTF-16 unit.
 */
private fun isGoSpace(char: Char): Boolean = when (char.code) {
  in 0x09..0x0d, 0x20, 0x85, 0xa0, 0x1680, in 0x2000..0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000 -> true
  else -> false
}

private fun hasUnpairedSurrogate(value: String): Boolean {
  var index = 0
  while (index < value.length) {
    val char = value[index]
    if (char.isHighSurrogate() && index + 1 < value.length && value[index + 1].isLowSurrogate()) {
      index += 2
      continue
    }
    if (char.isSurrogate()) return true
    index++
  }
  return false
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
    requireExternalCustomerId(externalCustomerId)
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
