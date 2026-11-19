package com.iapstack.huawei

import com.google.gson.Gson
import com.google.gson.JsonParser
import com.google.gson.JsonSyntaxException
import com.iapstack.core.PurchaseSubmission

private val huaweiGson = Gson()

/**
 * Requires one non-empty customer identity in valid Unicode without
 * surrounding whitespace or control characters.
 *
 * This mirrors the server's identifier rule (`validateIdentifier` in
 * `internal/core/identifiers.go`): valid UTF-8, which for a Kotlin string
 * means no unpaired surrogate (UTF-8 encoding would replace it), and no C0
 * (U+0000-U+001F), DEL, or C1 (U+0080-U+009F) control character, the same set
 * as [Char.isISOControl], and no leading or trailing whitespace as Go's
 * `strings.TrimSpace` defines it. It runs before the Huawei purchase UI opens:
 * an id the server rejects would be charged as `developerPayload` and then
 * never produce an entitlement, and a padded id on restore would never match
 * the bound payload and silently skip rows. The shared cases in
 * `contracts/test-vectors/external-customer-ids.json` pin this copy to the
 * server.
 */
internal fun requireExternalCustomerId(value: String) {
  if (value.isEmpty() ||
    isGoSpace(value.first()) ||
    isGoSpace(value.last()) ||
    value.any { it.isISOControl() } ||
    hasUnpairedSurrogate(value)
  ) {
    throw IllegalArgumentException(
      "externalCustomerId must be non-empty valid Unicode without surrounding whitespace or control characters",
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
 * Exact signed Huawei purchase evidence accepted by IAPStack.
 */
class HuaweiPurchaseEvidence(
  val purchaseData: String,
  val signature: String,
  val productKind: HuaweiProductKind,
) {
  private val decodedPurchaseData: Map<String, Any?> = decodePurchaseData(purchaseData)

  init {
    if (signature.trim().isEmpty()) {
      throw HuaweiIapStackException(
        code = "invalid_signature",
        message = "Huawei purchase signature is empty",
      )
    }
  }

  /**
   * Creates evidence from the provider bridge without mutating signed payloads.
   */
  constructor(
    purchase: HuaweiSignedPurchase,
    productKind: HuaweiProductKind,
  ) : this(
    purchaseData = purchase.purchaseData,
    signature = purchase.signature,
    productKind = productKind,
  )

  /**
   * Product ID declared by Huawei in the signed payload.
   */
  val productId: String
    get() = requiredString(decodedPurchaseData, "productId")

  /**
   * Customer binding passed as Huawei `developerPayload`.
   */
  val developerPayload: String
    get() = requiredString(decodedPurchaseData, "developerPayload")

  /**
   * Encodes the versioned Huawei evidence object without rewriting payload bytes.
   */
  fun toJson(): Map<String, Any?> = mapOf(
    "purchase_data" to purchaseData,
    "signature" to signature,
    "product_kind" to productKind.evidenceValue,
  )

  /**
   * Converts to one provider-neutral backend submission after local binding checks.
   */
  fun toSubmission(
    externalCustomerId: String,
    expectedProductId: String? = null,
  ): PurchaseSubmission {
    requireExternalCustomerId(externalCustomerId)
    if (developerPayload != externalCustomerId) {
      throw HuaweiIapStackException(
        code = "customer_binding_mismatch",
        message = "Huawei developer payload does not match the current customer",
      )
    }
    if (expectedProductId != null && productId != expectedProductId) {
      throw HuaweiIapStackException(
        code = "product_binding_mismatch",
        message = "Huawei purchase product does not match the requested product",
      )
    }
    return PurchaseSubmission(
      externalCustomerId = externalCustomerId,
      claimedProducts = listOf(productId),
      evidence = toJson(),
    )
  }

  override fun toString(): String =
    "HuaweiPurchaseEvidence(productKind=${productKind.name}, purchaseData=<redacted>)"
}

private fun decodePurchaseData(value: String): Map<String, Any?> {
  if (value.isBlank()) {
    throw HuaweiIapStackException(
      code = "invalid_purchase_data",
      message = "Huawei purchase data is empty",
    )
  }
  val parsed = try {
    JsonParser.parseString(value)
  } catch (error: JsonSyntaxException) {
    throw HuaweiIapStackException(
      code = "invalid_purchase_data",
      message = "Huawei purchase data is not a valid JSON object",
      cause = error,
    )
  }
  if (!parsed.isJsonObject) {
    throw HuaweiIapStackException(
      code = "invalid_purchase_data",
      message = "Huawei purchase data must be a JSON object",
    )
  }
  return parsed.asJsonObject.entrySet().associate { (key, jsonValue) ->
    key to huaweiGson.fromJson(jsonValue, Any::class.java)
  }
}

private fun requiredString(values: Map<String, Any?>, key: String): String {
  val value = values[key]
  if (value !is String || value.isEmpty()) {
    throw HuaweiIapStackException(
      code = "invalid_purchase_data",
      message = "Huawei purchase data omitted $key",
    )
  }
  return value
}
