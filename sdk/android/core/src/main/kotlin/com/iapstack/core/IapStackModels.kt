package com.iapstack.core

import java.time.Instant

/**
 * Optional external customer binding used by some providers.
 */
data class CustomerBinding(
  val kind: String,
  val value: String,
) {
  fun toJson(): Map<String, Any?> = mapOf("kind" to kind, "value" to value)
}

/**
 * One provider-signed purchase payload submission.
 */
data class PurchaseSubmission(
  val externalCustomerId: String,
  val claimedProducts: List<String>,
  val evidence: Map<String, Any?>,
  val customerBindings: List<CustomerBinding> = emptyList(),
) {
  fun toJson(): Map<String, Any?> {
    val json = mutableMapOf<String, Any?>(
      "external_customer_id" to externalCustomerId,
      "claimed_products" to claimedProducts,
      "evidence" to evidence,
    )
    if (customerBindings.isNotEmpty()) {
      json["customer_bindings"] = customerBindings.map(CustomerBinding::toJson)
    }
    return json
  }
}

/**
 * One entitlement projection returned by the API.
 */
data class Entitlement(
  val key: String,
  val access: String,
  val reason: String,
  val version: Int,
  val effectiveStartsAt: Instant? = null,
  val effectiveEndsAt: Instant? = null,
) {
  fun grantsAccess(): Boolean = grantsAccessAt(Instant.now())

  fun grantsAccessAt(instant: Instant): Boolean {
    if (access != "allowed") return false
    return effectiveEndsAt == null || instant.isBefore(effectiveEndsAt)
  }

  companion object {
    fun fromJson(json: Map<String, Any?>): Entitlement = Entitlement(
      key = requiredString(json, "key"),
      access = requiredString(json, "access"),
      reason = requiredString(json, "reason"),
      version = requiredInt(json, "version"),
      effectiveStartsAt = optionalDateTime(json, "effective_starts_at"),
      effectiveEndsAt = optionalDateTime(json, "effective_ends_at"),
    )
  }
}

/**
 * API response after one purchase verification.
 */
data class VerificationResult(
  val verifiedAt: Instant,
  val customerId: String,
  val entitlements: List<Entitlement>,
) {
  companion object {
    fun fromJson(json: Map<String, Any?>): VerificationResult = VerificationResult(
      verifiedAt = requiredDateTime(json, "verified_at"),
      customerId = requiredString(json, "customer_id"),
      entitlements = requiredObjectList(json, "entitlements").map { Entitlement.fromJson(it) },
    )
  }
}

/**
 * Batched verify response.
 */
data class RestoreResult(
  val results: List<VerificationResult>,
)

/**
 * Current entitlement snapshot for one external customer.
 */
data class EntitlementSnapshot(
  val customerId: String,
  val entitlements: List<Entitlement>,
) {
  companion object {
    fun fromJson(json: Map<String, Any?>): EntitlementSnapshot = EntitlementSnapshot(
      customerId = requiredString(json, "customer_id"),
      entitlements = requiredObjectList(json, "entitlements").map { Entitlement.fromJson(it) },
    )
  }
}

internal fun requiredString(json: Map<String, Any?>, key: String): String {
  val value = json[key]
  if (value !is String || value.isBlank()) {
    throw IapStackProtocolException("$key must be a non-empty string")
  }
  return value
}

internal fun requiredInt(json: Map<String, Any?>, key: String): Int {
  val value = json[key]
  return when (value) {
    is Int -> value
    is Long -> value.toInt()
    is Double -> value.toInt()
    is Float -> value.toInt()
    else -> throw IapStackProtocolException("$key must be an integer")
  }
}

internal fun requiredDateTime(json: Map<String, Any?>, key: String): Instant {
  val value = requiredString(json, key)
  return Instant.parse(value)
}

internal fun optionalDateTime(json: Map<String, Any?>, key: String): Instant? {
  val value = json[key]
  if (value == null) return null
  if (value !is String || value.isBlank()) {
    throw IapStackProtocolException("$key must be an ISO-8601 timestamp")
  }
  return Instant.parse(value)
}

internal fun requiredObjectList(json: Map<String, Any?>, key: String): List<Map<String, Any?>> {
  val value = json[key]
  if (value !is List<*>) {
    throw IapStackProtocolException("$key must be an array")
  }
  return value.map { item ->
    item as? Map<String, Any?> ?: throw IapStackProtocolException("$key entries must be objects")
  }
}
