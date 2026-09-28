package com.iapstack.googleplay

import android.app.Activity
import android.content.Context
import com.android.billingclient.api.*
import java.io.Closeable
import java.util.concurrent.ConcurrentHashMap
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/**
 * Play Billing adapter. Keep one instance per active app session and collect [purchaseUpdates]
 * once, from startup. Call [close] when the session ends. Re-query products before checkout.
 * The activity provider must return the current foreground Activity, not a retained old Activity.
 * No purchase is acknowledged or consumed on device.
 */
class BillingClientGooglePlayPlatform internal constructor(
  private val activity: () -> Activity?,
  productKinds: Map<String, GooglePlayProductKind>,
  factory: (PurchasesUpdatedListener) -> BillingClient,
) : GooglePlayIapPlatform, Closeable {
  constructor(
    context: Context,
    productKinds: Map<String, GooglePlayProductKind>,
    activity: () -> Activity?,
  ) : this(activity, productKinds, { listener ->
    BillingClient.newBuilder(context.applicationContext)
      .setListener(listener)
      .enablePendingPurchases(PendingPurchasesParams.newBuilder()
        .enableOneTimeProducts().enablePrepaidPlans().build())
      .build()
  })

  private val catalog = productKinds.toMap().also {
    require(it.isNotEmpty() && it.keys.all { id -> id.isNotBlank() && id == id.trim() })
  }
  private val updates = Channel<GooglePlayPurchase>(Channel.UNLIMITED)
  override val purchaseUpdates = updates.receiveAsFlow()
  private val pending = ConcurrentHashMap.newKeySet<CompletableDeferred<*>>()
  private val connection = Mutex()
  private val selectionOperations = Mutex()
  private val selections = mutableMapOf<String, Pair<GooglePlayProduct, ProductDetails>>()
  @Volatile private var closed = false
  private val billing = factory(PurchasesUpdatedListener { result, purchases ->
    if (result.responseCode == BillingClient.BillingResponseCode.OK) {
      purchases.orEmpty().forEach { updates.trySend(it.toModel(restored = false)) }
    } else {
      updates.trySend(GooglePlayPurchase(
        purchaseToken = "", productIds = emptyList(), isAcknowledged = false,
        status = if (result.responseCode == BillingClient.BillingResponseCode.USER_CANCELED)
          GooglePlayPurchaseStatus.CANCELLED else GooglePlayPurchaseStatus.FAILED,
        errorCode = billingError(result.responseCode).code,
      ))
    }
  })

  override suspend fun isAvailable(): Boolean = withContext(Dispatchers.Main.immediate) {
    try {
      connect()
      true
    } catch (error: GooglePlayIapStackException) {
      if (error.code == "billing_timeout") throw error
      false
    }
  }

  override suspend fun queryProducts(productIds: Set<String>): GooglePlayProductQuery =
    withContext(Dispatchers.Main.immediate) {
      selectionOperations.withLock {
        require(productIds.isNotEmpty() && catalog.keys.containsAll(productIds))
        connect()
        selections.entries.removeAll { it.value.first.id in productIds }
        val found = mutableListOf<GooglePlayProduct>()
        val queried = mutableMapOf<String, Pair<GooglePlayProduct, ProductDetails>>()
        for ((kind, ids) in productIds.groupBy { catalog.getValue(it) }) {
          // Play accepts at most 20 products per details request.
          for (batch in ids.chunked(20)) {
            val params = QueryProductDetailsParams.newBuilder().setProductList(batch.map {
              QueryProductDetailsParams.Product.newBuilder().setProductId(it)
                .setProductType(kind.billingType).build()
            }).build()
            val details = request<List<ProductDetails>> { reply ->
              billing.queryProductDetailsAsync(params) { result, response ->
                reply(runCatching {
                  checkResult(result)
                  for (unfetched in response.unfetchedProductList) {
                    if (unfetched.productId !in batch || unfetched.productType != kind.billingType) {
                      throw invalidProduct("Google Play returned an unexpected unfetched product")
                    }
                    when (unfetched.statusCode) {
                      UnfetchedProduct.StatusCode.PRODUCT_NOT_FOUND,
                      UnfetchedProduct.StatusCode.NO_ELIGIBLE_OFFER -> Unit
                      else -> throw GooglePlayIapStackException(
                        "product_query_failed_${unfetched.statusCode}", "Google Play could not fetch product details")
                    }
                  }
                  response.productDetailsList
                })
              }
            }
            for (detail in details) {
              if (detail.productId !in batch || detail.productType != kind.billingType) {
                throw GooglePlayIapStackException("invalid_plugin_response", "Google Play returned an unexpected product")
              }
              for (product in detail.toModels(kind)) {
                found.add(product)
                queried[product.selectionKey] = product to detail
              }
            }
          }
        }
        selections.putAll(queried)
        GooglePlayProductQuery(found, productIds - found.map { it.id }.toSet())
      }
    }

  override suspend fun launchPurchase(product: GooglePlayProduct, obfuscatedAccountId: String) =
    withContext(Dispatchers.Main.immediate) {
      selectionOperations.withLock {
        require(obfuscatedAccountId.isNotBlank() && obfuscatedAccountId == obfuscatedAccountId.trim() &&
          obfuscatedAccountId.length <= 64)
        connect()
        val selected = selections[product.selectionKey]
        if (selected?.first != product) throw GooglePlayIapStackException(
          "product_not_queried", "Query and select the Google Play offer before purchase")
        val foreground = activity()?.takeUnless { it.isFinishing || it.isDestroyed }
          ?: throw GooglePlayIapStackException("activity_unavailable", "A foreground Activity is required")
        val item = BillingFlowParams.ProductDetailsParams.newBuilder().setProductDetails(selected.second)
        product.offerToken?.let(item::setOfferToken)
        checkResult(billing.launchBillingFlow(foreground, BillingFlowParams.newBuilder()
          .setObfuscatedAccountId(obfuscatedAccountId).setProductDetailsParamsList(listOf(item.build())).build()))
      }
    }

  override suspend fun ownedPurchases(obfuscatedAccountId: String): List<GooglePlayPurchase> =
    withContext(Dispatchers.Main.immediate) {
      connect()
      catalog.values.toSet().flatMap { kind ->
        request<List<Purchase>> { reply ->
          billing.queryPurchasesAsync(QueryPurchasesParams.newBuilder().setProductType(kind.billingType).build()) {
              result, purchases -> reply(runCatching { checkResult(result); purchases })
          }
        }.map { it.toModel(restored = true) }
      }.filter { it.obfuscatedAccountId == obfuscatedAccountId }
    }

  private suspend fun connect() = connection.withLock {
    checkOpen()
    if (!billing.isReady) {
      request<Unit> { reply ->
        billing.startConnection(object : BillingClientStateListener {
          override fun onBillingSetupFinished(result: BillingResult) {
            reply(runCatching { checkResult(result) })
          }
          override fun onBillingServiceDisconnected() {
            val error = billingError(BillingClient.BillingResponseCode.SERVICE_DISCONNECTED)
            pending.forEach { it.completeExceptionally(error) }
          }
        })
      }
    }
  }

  private suspend fun <T : Any> request(start: ((Result<T>) -> Unit) -> Unit): T = withTimeoutOrNull(30_000) {
    checkOpen()
    val result = CompletableDeferred<T>()
    pending.add(result)
    try {
      checkOpen()
      start { it.fold(result::complete, result::completeExceptionally) }
      result.await()
    } finally {
      pending.remove(result)
      result.cancel()
    }
  } ?: throw GooglePlayIapStackException("billing_timeout", "Google Play Billing request timed out")

  private fun checkOpen() {
    if (closed) throw GooglePlayIapStackException("platform_closed", "Google Play adapter is closed")
  }

  override fun close() {
    if (closed) return
    closed = true
    pending.forEach { it.completeExceptionally(GooglePlayIapStackException("platform_closed", "Google Play adapter is closed")) }
    billing.endConnection()
    updates.close()
  }
}

private val GooglePlayProductKind.billingType: String
  get() = if (this == GooglePlayProductKind.SUBSCRIPTION) BillingClient.ProductType.SUBS else BillingClient.ProductType.INAPP

private fun checkResult(result: BillingResult) {
  if (result.responseCode != BillingClient.BillingResponseCode.OK) throw billingError(result.responseCode)
}

private fun billingError(code: Int) = GooglePlayIapStackException(
  code = when (code) {
    BillingClient.BillingResponseCode.USER_CANCELED -> "purchase_cancelled"
    BillingClient.BillingResponseCode.SERVICE_DISCONNECTED -> "billing_service_disconnected"
    else -> "billing_error_$code"
  },
  message = "Google Play Billing request failed (code $code)",
  userCancelled = code == BillingClient.BillingResponseCode.USER_CANCELED,
)

private fun Purchase.toModel(restored: Boolean) = GooglePlayPurchase(
  purchaseToken = purchaseToken,
  productIds = products.toList(),
  status = when (purchaseState) {
    Purchase.PurchaseState.PENDING -> GooglePlayPurchaseStatus.PENDING
    Purchase.PurchaseState.PURCHASED -> if (restored) GooglePlayPurchaseStatus.RESTORED else GooglePlayPurchaseStatus.PURCHASED
    else -> GooglePlayPurchaseStatus.FAILED
  },
  isAcknowledged = isAcknowledged,
  obfuscatedAccountId = accountIdentifiers?.obfuscatedAccountId,
)

private fun invalidProduct(message: String) = GooglePlayIapStackException("invalid_plugin_response", message)

private fun ProductDetails.toModels(kind: GooglePlayProductKind): List<GooglePlayProduct> {
  val id = productId?.takeIf { it.isNotBlank() }
    ?: throw invalidProduct("Google Play returned an incomplete product")
  val productTitle = title?.takeIf { it.isNotBlank() } ?: id
  val productDescription = description.orEmpty()
  if (kind == GooglePlayProductKind.NON_CONSUMABLE) {
    val offers = oneTimePurchaseOfferDetailsList ?: listOfNotNull(oneTimePurchaseOfferDetails)
    return offers.filter { it.rentalDetails == null }.map { offer ->
      val price = offer.formattedPrice
      val currency = offer.priceCurrencyCode
      if (price.isNullOrBlank() || offer.priceAmountMicros < 0 ||
        currency == null || !Regex("^[A-Z]{3}$").matches(currency)) {
        throw invalidProduct("Google Play returned invalid one-time pricing")
      }
      GooglePlayProduct(id, kind, productTitle, productDescription, price,
        offer.priceAmountMicros, currency, offerToken = offer.offerToken)
    }
  }
  return subscriptionOfferDetails.orEmpty().map { offer ->
    val nativePhases = offer.pricingPhases?.pricingPhaseList
    val token = offer.offerToken
    val basePlan = offer.basePlanId
    if (nativePhases.isNullOrEmpty() || token.isNullOrBlank() || basePlan.isNullOrBlank()) {
      throw invalidProduct("Google Play returned an incomplete subscription offer")
    }
    val phases = nativePhases.map { phase ->
      val period = phase?.billingPeriod
      val price = phase?.formattedPrice
      val currency = phase?.priceCurrencyCode
      if (phase == null || phase.billingCycleCount < 0 || period.isNullOrBlank() ||
        price.isNullOrBlank() || phase.priceAmountMicros < 0 ||
        currency == null || !Regex("^[A-Z]{3}$").matches(currency)) {
        throw invalidProduct("Google Play returned an invalid subscription pricing phase")
      }
      GooglePlayPricingPhase(phase.billingCycleCount, period, price,
        phase.priceAmountMicros, currency, when (phase.recurrenceMode) {
          ProductDetails.RecurrenceMode.FINITE_RECURRING -> GooglePlayPricingRecurrence.FINITE
          ProductDetails.RecurrenceMode.INFINITE_RECURRING -> GooglePlayPricingRecurrence.INFINITE
          ProductDetails.RecurrenceMode.NON_RECURRING -> GooglePlayPricingRecurrence.NON_RECURRING
          else -> throw invalidProduct("Google Play returned an unknown pricing recurrence")
        })
    }
    val price = phases.first()
    GooglePlayProduct(id, kind, productTitle, productDescription, price.formattedPrice, price.priceMicros,
      price.currencyCode, token, basePlan, offer.offerId, offer.offerTags.orEmpty(), phases)
  }
}
