package com.iapstack.googleplay

import com.iapstack.core.IapStackClient
import com.iapstack.core.PurchaseSubmission
import com.iapstack.core.RestoreResult
import com.iapstack.core.VerificationResult
import com.iapstack.core.EntitlementSnapshot

/**
 * High-level Google Play purchase and restore flows backed by IAPStack.
 */
class GooglePlayIapStack(
  private val client: IapStackClient,
  productKinds: Map<String, GooglePlayProductKind>,
  private val platform: GooglePlayIapPlatform,
) {
  private val productKinds: Map<String, GooglePlayProductKind>
  private val queriedProducts = mutableMapOf<String, GooglePlayProduct>()

  init {
    this.productKinds = normalizedProductKinds(productKinds)
    if (this.productKinds.isEmpty()) {
      throw IllegalArgumentException("productKinds must contain at least one configured product")
    }
  }

  /**
   * Emits Play Billing changes that the host should process from app startup.
   */
  val purchaseUpdates = platform.purchaseUpdates

  /**
   * Reports whether Google Play Billing is available for the installed build.
   */
  suspend fun isAvailable(): Boolean = platform.isAvailable()

  /**
   * Queries localized details and validates provider kinds against the local catalog.
   */
  suspend fun queryProducts(productIds: Set<String>): GooglePlayProductQuery {
    if (productIds.isEmpty() || productIds.any { it.trim().isEmpty() }) {
      throw IllegalArgumentException("productIds must contain at least one non-empty product ID")
    }
    val normalized = productIds.map(String::trim).toSet()
    val unconfigured = normalized - productKinds.keys
    if (unconfigured.isNotEmpty()) {
      throw GooglePlayIapStackException(
        code = "unconfigured_product",
        message = "Google Play product is not configured: ${unconfigured.first()}",
      )
    }

    val existingKeys = queriedProducts.keys.toList()
    for (selectionKey in existingKeys) {
      val cached = queriedProducts[selectionKey]
      if (cached != null && cached.id in normalized) {
        queriedProducts.remove(selectionKey)
      }
    }

    val result = platform.queryProducts(normalized)
    if (!normalized.containsAll(result.notFoundProductIds)) {
      throw GooglePlayIapStackException(
        code = "invalid_plugin_response",
        message = "Google Play reported an unexpected missing product",
      )
    }

    val foundIds = mutableSetOf<String>()
    val selectionKeys = mutableSetOf<String>()
    val foundProducts = mutableListOf<GooglePlayProduct>()
    for (product in result.products) {
      val expectedKind = productKinds[product.id]
      if (product.id !in normalized || expectedKind == null || expectedKind != product.kind || !product.isPurchasable) {
        throw GooglePlayIapStackException(
          code = "invalid_plugin_response",
          message = "Google Play returned an unexpected product offer",
        )
      }
      if (!selectionKeys.add(product.selectionKey)) {
        throw GooglePlayIapStackException(
          code = "invalid_plugin_response",
          message = "Google Play returned a duplicate product offer",
        )
      }
      foundIds.add(product.id)
      foundProducts.add(product)
      queriedProducts[product.selectionKey] = product
    }
    if (foundIds.intersect(result.notFoundProductIds).isNotEmpty() ||
      !foundIds.union(result.notFoundProductIds).containsAll(normalized)
    ) {
      throw GooglePlayIapStackException(
        code = "invalid_plugin_response",
        message = "Google Play product query was incomplete",
      )
    }

    return GooglePlayProductQuery(
      products = foundProducts.toList(),
      notFoundProductIds = result.notFoundProductIds.toSet(),
    )
  }

  /**
   * Opens Play Billing UI with the customer binding required by server verification.
   */
  suspend fun launchPurchase(
    externalCustomerId: String,
    product: GooglePlayProduct,
  ) {
    validateExternalCustomerId(externalCustomerId)
    val expectedKind = productKinds[product.id]
    if (expectedKind == null || expectedKind != product.kind) {
      throw GooglePlayIapStackException(
        code = "product_kind_mismatch",
        message = "Google Play product does not match the configured catalog",
      )
    }
    if (queriedProducts[product.selectionKey] != product) {
      throw GooglePlayIapStackException(
        code = "product_not_queried",
        message = "Query and select the Google Play offer before purchase",
      )
    }
    if (!product.isPurchasable) {
      throw GooglePlayIapStackException(
        code = "product_unavailable",
        message = "Google Play product offer cannot start a purchase",
      )
    }
    platform.launchPurchase(product = product, obfuscatedAccountId = externalCustomerId)
  }

  /**
   * Verifies one pending, completed, or restored purchase with authoritative server state.
   */
  suspend fun verifyPurchase(
    externalCustomerId: String,
    purchase: GooglePlayPurchase,
    requestId: String? = null,
  ): VerificationResult {
    val submission = submission(
      externalCustomerId = externalCustomerId,
      purchase = purchase,
    )
    return client.verifyPurchase(submission, requestId)
  }

  /**
   * Restores configured, customer-bound purchases in bounded IAPStack batches.
   */
  suspend fun restorePurchases(
    externalCustomerId: String,
    requestId: String? = null,
  ): RestoreResult {
    validateExternalCustomerId(externalCustomerId)
    val ownedPurchases = platform.ownedPurchases(obfuscatedAccountId = externalCustomerId)
    val submissions = mutableListOf<PurchaseSubmission>()
    val observedTokens = mutableSetOf<String>()
    for (purchase in ownedPurchases) {
      if (purchase.status != GooglePlayPurchaseStatus.PURCHASED &&
        purchase.status != GooglePlayPurchaseStatus.RESTORED
      ) {
        continue
      }
      if (purchase.purchaseToken.trim().isEmpty() ||
        purchase.obfuscatedAccountId != externalCustomerId ||
        purchase.productIds.size != 1
      ) {
        continue
      }
      val productId = purchase.productIds.single()
      if (!productKinds.containsKey(productId) || !observedTokens.add(purchase.purchaseToken)) {
        continue
      }
      submissions.add(submission(externalCustomerId = externalCustomerId, purchase = purchase))
    }
    if (submissions.isEmpty()) {
      return RestoreResult(results = emptyList())
    }

    val results = mutableListOf<VerificationResult>()
    var batchIndex = 0
    while (batchIndex * 100 < submissions.size) {
      val start = batchIndex * 100
      val end = minOf(start + 100, submissions.size)
      val batch = client.restorePurchases(
        purchases = submissions.subList(start, end),
        requestId = batchRequestId(requestId, batchIndex),
      )
      results.addAll(batch.results)
      batchIndex++
    }
    return RestoreResult(results = results.toList())
  }

  /**
   * Loads the current IAPStack projection without contacting Google Play.
   */
  suspend fun getEntitlements(
    externalCustomerId: String,
    requestId: String? = null,
  ): EntitlementSnapshot = client.getEntitlements(externalCustomerId, requestId)

  /**
   * Creates one provider evidence submission and validates the incoming status binding.
   */
  private fun submission(
    externalCustomerId: String,
    purchase: GooglePlayPurchase,
  ): PurchaseSubmission {
    validateExternalCustomerId(externalCustomerId)
    if (!purchase.canVerify) {
      val failedStatusCode = if (purchase.status == GooglePlayPurchaseStatus.CANCELLED) {
        "purchase_cancelled"
      } else {
        "purchase_failed"
      }
      throw GooglePlayIapStackException(
        code = failedStatusCode,
        message = if (purchase.status == GooglePlayPurchaseStatus.CANCELLED) {
          "Google Play purchase was cancelled"
        } else {
          "Google Play purchase cannot be verified"
        },
        userCancelled = purchase.status == GooglePlayPurchaseStatus.CANCELLED,
      )
    }
    if (purchase.obfuscatedAccountId != externalCustomerId) {
      throw GooglePlayIapStackException(
        code = "customer_binding_mismatch",
        message = "Google Play purchase does not match the current customer",
      )
    }
    if (purchase.productIds.size != 1) {
      throw GooglePlayIapStackException(
        code = "unsupported_multi_product_purchase",
        message = "IAPStack currently requires one Google Play product per purchase",
      )
    }
    val productId = purchase.productIds.first()
    val productKind = productKinds[productId]
      ?: throw GooglePlayIapStackException(
        code = "unknown_product",
        message = "Google Play purchase product is not configured",
      )
    return GooglePlayPurchaseEvidence(
      purchaseToken = purchase.purchaseToken,
      productKind = productKind,
    ).toSubmission(
      externalCustomerId = externalCustomerId,
      productId = productId,
    )
  }

  private fun validateExternalCustomerId(externalCustomerId: String) {
    if (externalCustomerId.trim().isEmpty() ||
      externalCustomerId.trim() != externalCustomerId ||
      externalCustomerId.length > 64
    ) {
      throw IllegalArgumentException(
        "externalCustomerId must be non-empty, at most 64 characters, and have no surrounding whitespace",
      )
    }
  }

  private fun batchRequestId(requestId: String?, batchIndex: Int): String? {
    if (requestId == null || requestId.trim().isEmpty()) {
      return null
    }
    val normalized = requestId.trim()
    return if (batchIndex == 0) {
      normalized
    } else {
      "$normalized-${batchIndex + 1}"
    }
  }

  private fun normalizedProductKinds(productKinds: Map<String, GooglePlayProductKind>): Map<String, GooglePlayProductKind> {
    val normalized = LinkedHashMap<String, GooglePlayProductKind>()
    for ((productId, kind) in productKinds) {
      val normalizedId = productId.trim()
      if (normalizedId.isEmpty() || normalized.containsKey(normalizedId)) {
        throw IllegalArgumentException("productKinds must contain unique, non-empty product IDs")
      }
      normalized[normalizedId] = kind
    }
    return normalized
  }
}
