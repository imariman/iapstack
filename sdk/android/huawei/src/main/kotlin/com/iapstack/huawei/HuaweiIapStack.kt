package com.iapstack.huawei

import com.iapstack.core.EntitlementSnapshot
import com.iapstack.core.IapStackClient
import com.iapstack.core.PurchaseSubmission
import com.iapstack.core.RestoreResult
import com.iapstack.core.VerificationResult

/**
 * High-level Huawei purchase and restore flows backed by IAPStack.
 */
class HuaweiIapStack(
  private val client: IapStackClient,
  productKinds: Map<String, HuaweiProductKind>,
  private val platform: HuaweiIapPlatform,
  private val maxRestorePagesPerKind: Int = 20,
) {
  private val productKinds: Map<String, HuaweiProductKind>
  private val queriedProducts = mutableMapOf<String, HuaweiProduct>()

  init {
    if (maxRestorePagesPerKind < 1 || maxRestorePagesPerKind > 100) {
      throw IllegalArgumentException("maxRestorePagesPerKind must be between 1 and 100")
    }
    this.productKinds = normalizedProductKinds(productKinds)
    if (this.productKinds.isEmpty()) {
      throw IllegalArgumentException("productKinds must contain at least one configured product")
    }
  }

  /**
   * Checks whether Huawei IAP is available for the current account region.
   */
  suspend fun isAvailable(): Boolean = platform.isAvailable()

  /**
   * Checks whether the current Huawei account and APK can use sandbox IAP.
   */
  suspend fun sandboxStatus(): HuaweiSandboxStatus = platform.sandboxStatus()

  /**
   * Loads configured AppGallery products and caches validated purchase choices.
   */
  suspend fun queryProducts(productIds: Set<String>): HuaweiProductQuery {
    if (productIds.isEmpty() || productIds.any { it.trim().isEmpty() }) {
      throw IllegalArgumentException("productIds must contain at least one non-empty product ID")
    }
    val normalized = productIds.map(String::trim).toMutableSet()
    val unconfigured = normalized.filter { id -> !productKinds.containsKey(id) }
    if (unconfigured.isNotEmpty()) {
      throw HuaweiIapStackException(
        code = "unconfigured_product",
        message = "Huawei product is not configured: ${unconfigured.first()}",
      )
    }

    val found = mutableMapOf<String, HuaweiProduct>()
    for (kind in HuaweiProductKind.values()) {
      val idsForKind = normalized.filter { productKinds[it] == kind }
      if (idsForKind.isEmpty()) {
        continue
      }
      val products = platform.queryProducts(
        productIds = idsForKind,
        productKind = kind,
      )
      for (product in products) {
        if (!idsForKind.contains(product.id) || product.kind != kind) {
          throw HuaweiIapStackException(
            code = "invalid_plugin_response",
            message = "Huawei returned an unexpected product",
          )
        }
        if (found.put(product.id, product) != null) {
          throw HuaweiIapStackException(
            code = "invalid_plugin_response",
            message = "Huawei returned a duplicate product",
          )
        }
      }
    }

    queriedProducts.putAll(found)
    return HuaweiProductQuery(
      products = normalized.filter(found::containsKey).map { found[it]!! },
      notFoundProductIds = normalized.toMutableSet().apply {
        removeAll(found.keys)
      },
    )
  }

  /**
   * Opens Huawei purchase UI and verifies the resulting signed evidence.
   */
  suspend fun purchaseAndVerify(
    externalCustomerId: String,
    product: HuaweiProduct,
    requestId: String? = null,
  ): VerificationResult {
    if (externalCustomerId.trim().isEmpty()) {
      throw IllegalArgumentException("externalCustomerId must not be empty")
    }
    if (queriedProducts[product.id] != product) {
      throw HuaweiIapStackException(
        code = "product_not_queried",
        message = "Query and select the Huawei product before purchase",
      )
    }
    if (!product.isPurchasable) {
      throw HuaweiIapStackException(
        code = "product_unavailable",
        message = "Huawei product is not available for a new purchase",
      )
    }
    val purchase = platform.purchase(
      productId = product.id,
      productKind = product.kind,
      developerPayload = externalCustomerId,
    )
    val evidence = HuaweiPurchaseEvidence(
      purchase = purchase,
      productKind = product.kind,
    )
    return client.verifyPurchase(
      evidence.toSubmission(
        externalCustomerId = externalCustomerId,
        expectedProductId = product.id,
      ),
      requestId,
    )
  }

  /**
   * Restores configured, customer-bound purchases for one customer.
   */
  suspend fun restorePurchases(
    externalCustomerId: String,
    productKinds: Set<HuaweiProductKind> = setOf(
      HuaweiProductKind.NON_CONSUMABLE,
      HuaweiProductKind.SUBSCRIPTION,
    ),
    requestId: String? = null,
  ): RestoreResult {
    if (externalCustomerId.trim().isEmpty()) {
      throw IllegalArgumentException("externalCustomerId must not be empty")
    }
    if (productKinds.isEmpty()) {
      return RestoreResult(results = emptyList())
    }
    val submissions = mutableListOf<PurchaseSubmission>()
    val evidenceKeys = mutableSetOf<String>()

    for (kind in productKinds) {
      var continuationToken: String? = null
      val observedContinuationTokens = mutableSetOf<String>()
      var pageCount = 0
      do {
        pageCount++
        if (pageCount > maxRestorePagesPerKind) {
          throw HuaweiIapStackException(
            code = "restore_page_limit",
            message = "Huawei restore exceeded the configured page limit",
          )
        }
        val page = platform.ownedPurchases(
          productKind = kind,
          continuationToken = continuationToken,
        )
        for (purchase in page.purchases) {
          val submission = restoreSubmission(
            externalCustomerId = externalCustomerId,
            purchase = purchase,
            productKind = kind,
          ) ?: continue
          val evidenceKey = "${kind.name}\u0000${purchase.signature}\u0000${purchase.purchaseData}"
          if (!evidenceKeys.add(evidenceKey)) {
            continue
          }
          submissions.add(submission)
        }
        continuationToken = page.continuationToken
        if (continuationToken != null && !observedContinuationTokens.add(continuationToken)) {
          throw HuaweiIapStackException(
            code = "restore_pagination_cycle",
            message = "Huawei restore returned a repeated continuation token",
          )
        }
      } while (continuationToken != null)
    }

    if (submissions.isEmpty()) {
      return RestoreResult(results = emptyList())
    }

    val results = mutableListOf<VerificationResult>()
    var start = 0
    var batchIndex = 0
    while (start < submissions.size) {
      val end = minOf(start + 100, submissions.size)
      val batch = client.restorePurchases(
        purchases = submissions.subList(start, end),
        requestId = batchRequestId(requestId, batchIndex),
      )
      results.addAll(batch.results)
      start = end
      batchIndex++
    }
    return RestoreResult(results = results)
  }

  /**
   * Loads the current IAPStack projection without contacting Huawei.
   */
  suspend fun getEntitlements(
    externalCustomerId: String,
    requestId: String? = null,
  ): EntitlementSnapshot =
    client.getEntitlements(externalCustomerId, requestId)

  private fun restoreSubmission(
    externalCustomerId: String,
    purchase: HuaweiSignedPurchase,
    productKind: HuaweiProductKind,
  ): PurchaseSubmission? {
    if (purchase.purchaseData.trim().isEmpty() || purchase.signature.trim().isEmpty()) {
      return null
    }
    return try {
      val evidence = HuaweiPurchaseEvidence(
        purchase = purchase,
        productKind = productKind,
      )
      if (productKinds[evidence.productId] != productKind ||
        evidence.developerPayload != externalCustomerId
      ) {
        return null
      }
      evidence.toSubmission(
        externalCustomerId = externalCustomerId,
      )
    } catch (_: HuaweiIapStackException) {
      null
    }
  }

  private fun normalizedProductKinds(input: Map<String, HuaweiProductKind>): Map<String, HuaweiProductKind> {
    val normalized = linkedMapOf<String, HuaweiProductKind>()
    for ((productId, kind) in input) {
      val normalizedId = productId.trim()
      if (normalizedId.isEmpty() || normalized.containsKey(normalizedId)) {
        throw IllegalArgumentException("productKinds must contain unique, non-empty product IDs")
      }
      normalized[normalizedId] = kind
    }
    return normalized
  }
}

private fun batchRequestId(requestId: String?, batchIndex: Int): String? {
  if (requestId == null || requestId.trim().isEmpty()) {
    return null
  }
  val normalized = requestId.trim()
  return if (batchIndex == 0) normalized else "$normalized-${batchIndex + 1}"
}
