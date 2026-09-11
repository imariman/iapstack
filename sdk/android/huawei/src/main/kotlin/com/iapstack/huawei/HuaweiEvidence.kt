package com.iapstack.huawei

import com.google.gson.Gson
import com.google.gson.JsonParser
import com.google.gson.JsonSyntaxException
import com.iapstack.core.PurchaseSubmission

private val huaweiGson = Gson()

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
    if (externalCustomerId.isBlank()) {
      throw IllegalArgumentException("externalCustomerId must not be empty")
    }
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
