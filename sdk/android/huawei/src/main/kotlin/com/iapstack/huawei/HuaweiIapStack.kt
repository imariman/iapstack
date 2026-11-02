package com.iapstack.huawei

import com.iapstack.core.EntitlementSnapshot
import com.iapstack.core.IapStackClient
import com.iapstack.core.PurchaseSubmission
import com.iapstack.core.RestoreResult
import com.iapstack.core.RestoreSkipCounts
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
   * Returns false when Huawei IAP is unavailable in the account region.
   * Sign-in and operational failures throw a [HuaweiIapStackException].
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

    // Re-querying invalidates prior checkout handles even if HMS fails.
    queriedProducts.keys.removeAll(normalized)
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

    // Publish handles only after every kind was queried and validated.
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
   *
   * Hitting [maxRestorePagesPerKind] submits the pages already read and sets
   * [RestoreResult.pageLimitReached]. It does not discard those purchases.
   * That result cannot be resumed: the next restore reads the first pages
   * again. Raise [maxRestorePagesPerKind] to include older rows.
   * Unbound, different-customer, and unknown-product rows are counted on
   * [RestoreResult.skipped] and are not submitted.
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
    var unbound = 0
    var differentCustomer = 0
    var unknownProduct = 0
    var pageLimitReached = false

    for (kind in productKinds) {
      var continuationToken: String? = null
      val observedContinuationTokens = mutableSetOf<String>()
      var pageCount = 0
      do {
        pageCount++
        if (pageCount > maxRestorePagesPerKind) {
          // No continuation is returned. Callers raise maxRestorePagesPerKind.
          pageLimitReached = true
          break
        }
        val page = platform.ownedPurchases(
          productKind = kind,
          continuationToken = continuationToken,
        )
        for (purchase in page.purchases) {
          when (val row = classifyRestore(externalCustomerId, purchase, kind)) {
            is HuaweiRestoreRow.Accept -> {
              val evidenceKey = "${kind.name}\u0000${purchase.signature}\u0000${purchase.purchaseData}"
              if (evidenceKeys.add(evidenceKey)) {
                submissions.add(row.submission)
              }
            }
            HuaweiRestoreRow.Unbound -> unbound++
            HuaweiRestoreRow.DifferentCustomer -> differentCustomer++
            HuaweiRestoreRow.UnknownProduct -> unknownProduct++
            HuaweiRestoreRow.Ignore -> Unit
          }
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

    return client.restorePurchasesInBatches(
      purchases = submissions,
      requestId = requestId,
      skipped = RestoreSkipCounts(
        unbound = unbound,
        differentCustomer = differentCustomer,
        unknownProduct = unknownProduct,
      ),
      pageLimitReached = pageLimitReached,
    )
  }

  /**
   * Loads the current IAPStack projection without contacting Huawei.
   */
  suspend fun getEntitlements(
    externalCustomerId: String,
    requestId: String? = null,
  ): EntitlementSnapshot =
    client.getEntitlements(externalCustomerId, requestId)

  private fun classifyRestore(
    externalCustomerId: String,
    purchase: HuaweiSignedPurchase,
    productKind: HuaweiProductKind,
  ): HuaweiRestoreRow {
    if (purchase.purchaseData.trim().isEmpty() || purchase.signature.trim().isEmpty()) {
      return HuaweiRestoreRow.Ignore
    }
    val evidence = try {
      HuaweiPurchaseEvidence(purchase = purchase, productKind = productKind)
    } catch (_: HuaweiIapStackException) {
      return HuaweiRestoreRow.Ignore
    }
    val payload = try {
      evidence.developerPayload
    } catch (_: HuaweiIapStackException) {
      null
    }
    if (payload != null && payload != externalCustomerId) {
      return HuaweiRestoreRow.DifferentCustomer
    }
    val productId = try {
      evidence.productId
    } catch (_: HuaweiIapStackException) {
      null
    }
    if (productId == null || productKinds[productId] != productKind) {
      return HuaweiRestoreRow.UnknownProduct
    }
    if (payload == null) {
      return HuaweiRestoreRow.Unbound
    }
    return HuaweiRestoreRow.Accept(
      evidence.toSubmission(externalCustomerId = externalCustomerId),
    )
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

private sealed interface HuaweiRestoreRow {
  data class Accept(val submission: PurchaseSubmission) : HuaweiRestoreRow
  data object Unbound : HuaweiRestoreRow
  data object DifferentCustomer : HuaweiRestoreRow
  data object UnknownProduct : HuaweiRestoreRow
  data object Ignore : HuaweiRestoreRow
}
