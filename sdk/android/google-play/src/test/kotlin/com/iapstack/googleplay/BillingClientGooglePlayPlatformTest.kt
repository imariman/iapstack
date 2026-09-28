package com.iapstack.googleplay

import android.app.Activity
import com.android.billingclient.api.*
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.*
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.mockito.ArgumentMatchers.any
import org.mockito.Mockito.*
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import kotlin.test.*

@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [28])
class BillingClientGooglePlayPlatformTest {
  private val billing = mock(BillingClient::class.java)
  private val activity = mock(Activity::class.java)
  private lateinit var listener: PurchasesUpdatedListener
  private lateinit var platform: BillingClientGooglePlayPlatform
  private val catalog = mapOf("lifetime" to GooglePlayProductKind.NON_CONSUMABLE, "monthly" to GooglePlayProductKind.SUBSCRIPTION)

  @Before fun setup() {
    Dispatchers.setMain(UnconfinedTestDispatcher())
    `when`(billing.isReady).thenReturn(true)
    platform = BillingClientGooglePlayPlatform({ activity }, catalog) { listener = it; billing }
  }

  @After fun teardown() {
    platform.close()
    Dispatchers.resetMain()
  }

  @Test fun reconnectsAfterDisconnectAndFailsInflightRequests() = runTest {
    `when`(billing.isReady).thenReturn(false)
    var connections = 0
    lateinit var state: BillingClientStateListener
    doAnswer {
      connections++
      state = it.getArgument(0)
      if (connections == 1) state.onBillingServiceDisconnected()
      else state.onBillingSetupFinished(result())
      null
    }.`when`(billing).startConnection(any(BillingClientStateListener::class.java))
    assertFalse(platform.isAvailable())
    assertTrue(platform.isAvailable())
    assertEquals(2, connections)
    `when`(billing.isReady).thenReturn(true)
    val waiting = async(start = CoroutineStart.UNDISPATCHED) { runCatching { platform.queryProducts(setOf("lifetime")) } }
    state.onBillingServiceDisconnected()
    assertEquals("billing_service_disconnected", (waiting.await().exceptionOrNull() as GooglePlayIapStackException).code)
    // A callback from the old connection cannot complete an already failed request.
    state.onBillingSetupFinished(result())
  }

  @Test fun emitsCancellationAndPendingUpdatesWithoutAcknowledgement() = runTest {
    listener.onPurchasesUpdated(result(BillingClient.BillingResponseCode.USER_CANCELED), null)
    val cancelled = platform.purchaseUpdates.first()
    assertEquals(GooglePlayPurchaseStatus.CANCELLED, cancelled.status)
    assertFalse(cancelled.canVerify)
    val pending = purchase(Purchase.PurchaseState.PENDING)
    listener.onPurchasesUpdated(result(), listOf(pending))
    val update = platform.purchaseUpdates.first()
    assertEquals(GooglePlayPurchaseStatus.PENDING, update.status)
    assertEquals("opaque-customer", update.obfuscatedAccountId)
    assertEquals("store-token", update.purchaseToken)
    verifyNoInteractions(billing)
  }

  @Test fun mapsOwnedStatesAndFiltersOtherCustomer() = runTest {
    doAnswer {
      it.getArgument<PurchasesResponseListener>(1).onQueryPurchasesResponse(result(),
        listOf(purchase(Purchase.PurchaseState.PURCHASED), purchase(Purchase.PurchaseState.PENDING),
          purchase(Purchase.PurchaseState.PURCHASED, "other-customer")))
      null
    }.`when`(billing).queryPurchasesAsync(any(QueryPurchasesParams::class.java), any(PurchasesResponseListener::class.java))
    val owned = platform.ownedPurchases("opaque-customer")
    assertEquals(4, owned.size) // Both INAPP and SUBS queried.
    assertEquals(setOf(GooglePlayPurchaseStatus.RESTORED, GooglePlayPurchaseStatus.PENDING), owned.map { it.status }.toSet())
    verify(billing, times(2)).queryPurchasesAsync(any(QueryPurchasesParams::class.java), any(PurchasesResponseListener::class.java))
    verify(billing, never()).acknowledgePurchase(any(AcknowledgePurchaseParams::class.java), any(AcknowledgePurchaseResponseListener::class.java))
    verify(billing, never()).consumeAsync(any(ConsumeParams::class.java), any(ConsumeResponseListener::class.java))
  }

  @Test fun queriesRealOfferShapeAndBindsCheckoutCustomer() = runTest {
    val details = mock(ProductDetails::class.java) { invocation ->
      // BillingFlowParams also reads internal string metadata from ProductDetails.
      if (invocation.method.returnType == String::class.java) "" else RETURNS_DEFAULTS.answer(invocation)
    }
    val offer = mock(ProductDetails.OneTimePurchaseOfferDetails::class.java)
    `when`(details.productId).thenReturn("lifetime")
    `when`(details.productType).thenReturn(BillingClient.ProductType.INAPP)
    `when`(details.title).thenReturn("Lifetime")
    `when`(details.description).thenReturn("Lifetime access")
    `when`(details.oneTimePurchaseOfferDetailsList).thenReturn(listOf(offer))
    `when`(offer.formattedPrice).thenReturn("¥5,000")
    `when`(offer.priceAmountMicros).thenReturn(5_000_000_000L)
    `when`(offer.priceCurrencyCode).thenReturn("JPY")
    `when`(offer.offerToken).thenReturn("one-time-offer")
    doAnswer {
      it.getArgument<ProductDetailsResponseListener>(1).onProductDetailsResponse(result(),
        QueryProductDetailsResult.create(listOf(details), emptyList()))
      null
    }.`when`(billing).queryProductDetailsAsync(any(QueryProductDetailsParams::class.java), any(ProductDetailsResponseListener::class.java))
    doAnswer {
      val params = it.getArgument<BillingFlowParams>(1)
      // SDK exposes no public account-ID getter. Check the built request's string values.
      val strings = params.javaClass.declaredFields.filter { field -> field.type == String::class.java }.map { field ->
        field.isAccessible = true
        field.get(params)
      }
      assertTrue("opaque-customer" in strings)
      result(BillingClient.BillingResponseCode.USER_CANCELED)
    }.`when`(billing).launchBillingFlow(any(Activity::class.java), any(BillingFlowParams::class.java))
    val product = platform.queryProducts(setOf("lifetime")).products.first()
    assertEquals(5_000_000_000L, product.priceMicros)
    assertEquals("one-time-offer", product.offerToken)
    assertTrue(product.isPurchasable)
    val error = assertFailsWith<GooglePlayIapStackException> { platform.launchPurchase(product, "opaque-customer") }
    assertTrue(error.userCancelled)
    verify(billing, never()).acknowledgePurchase(any(AcknowledgePurchaseParams::class.java), any(AcknowledgePurchaseResponseListener::class.java))
  }

  @Test fun mapsSubscriptionPricingPhasesAndMissingProducts() = runTest {
    val details = mock(ProductDetails::class.java)
    val offer = mock(ProductDetails.SubscriptionOfferDetails::class.java)
    val phases = mock(ProductDetails.PricingPhases::class.java)
    val trial = mock(ProductDetails.PricingPhase::class.java)
    val recurring = mock(ProductDetails.PricingPhase::class.java)
    `when`(details.productId).thenReturn("monthly")
    `when`(details.productType).thenReturn(BillingClient.ProductType.SUBS)
    `when`(details.title).thenReturn("Monthly")
    `when`(details.description).thenReturn("Premium")
    `when`(details.subscriptionOfferDetails).thenReturn(listOf(offer))
    `when`(offer.offerToken).thenReturn("trial-token")
    `when`(offer.basePlanId).thenReturn("monthly-plan")
    `when`(offer.offerId).thenReturn("trial-offer")
    `when`(offer.offerTags).thenReturn(listOf("new-customer"))
    `when`(offer.pricingPhases).thenReturn(phases)
    `when`(phases.pricingPhaseList).thenReturn(listOf(trial, recurring))
    `when`(trial.billingCycleCount).thenReturn(1)
    `when`(trial.billingPeriod).thenReturn("P1W")
    `when`(trial.formattedPrice).thenReturn("Free")
    `when`(trial.priceCurrencyCode).thenReturn("USD")
    `when`(trial.recurrenceMode).thenReturn(ProductDetails.RecurrenceMode.FINITE_RECURRING)
    `when`(recurring.billingPeriod).thenReturn("P1M")
    `when`(recurring.formattedPrice).thenReturn("$4.99")
    `when`(recurring.priceAmountMicros).thenReturn(4_990_000L)
    `when`(recurring.priceCurrencyCode).thenReturn("USD")
    `when`(recurring.recurrenceMode).thenReturn(ProductDetails.RecurrenceMode.INFINITE_RECURRING)
    var queries = 0
    doAnswer {
      val found = if (queries++ == 0) listOf(details) else emptyList()
      it.getArgument<ProductDetailsResponseListener>(1).onProductDetailsResponse(result(),
        QueryProductDetailsResult.create(found, emptyList()))
      null
    }.`when`(billing).queryProductDetailsAsync(any(QueryProductDetailsParams::class.java), any(ProductDetailsResponseListener::class.java))
    val query = platform.queryProducts(linkedSetOf("monthly", "lifetime"))
    assertEquals(setOf("lifetime"), query.notFoundProductIds)
    val product = query.products.single()
    assertEquals("trial-token", product.offerToken)
    assertEquals("monthly-plan", product.basePlanId)
    assertEquals("trial-offer", product.offerId)
    assertEquals(listOf(GooglePlayPricingRecurrence.FINITE, GooglePlayPricingRecurrence.INFINITE), product.pricingPhases.map { it.recurrence })
    assertEquals(4_990_000L, product.pricingPhases.last().priceMicros)
    assertTrue(product.isPurchasable)
    // A fresh empty query invalidates the previously selected offer.
    platform.queryProducts(setOf("monthly"))
    assertEquals("product_not_queried", assertFailsWith<GooglePlayIapStackException> {
      platform.launchPurchase(product, "opaque-customer")
    }.code)
  }

  @Test fun timesOutAndIgnoresLateCallbacks() = runTest {
    lateinit var callback: ProductDetailsResponseListener
    doAnswer { callback = it.getArgument(1); null }.`when`(billing)
      .queryProductDetailsAsync(any(QueryProductDetailsParams::class.java), any(ProductDetailsResponseListener::class.java))
    val pending = async(start = CoroutineStart.UNDISPATCHED) { runCatching { platform.queryProducts(setOf("lifetime")) } }
    advanceTimeBy(30_001)
    assertEquals("billing_timeout", (pending.await().exceptionOrNull() as GooglePlayIapStackException).code)
    callback.onProductDetailsResponse(result(), QueryProductDetailsResult.create(emptyList(), emptyList()))
    assertTrue(platform.isAvailable())
    `when`(billing.isReady).thenReturn(false)
    assertEquals("billing_timeout", assertFailsWith<GooglePlayIapStackException> { platform.isAvailable() }.code)
  }

  @Test fun preservesCallerCancellationAndOuterTimeouts() = runTest {
    val cancelled = async(start = CoroutineStart.UNDISPATCHED) { platform.queryProducts(setOf("lifetime")) }
    cancelled.cancelAndJoin()
    assertTrue(cancelled.isCancelled)
    assertFailsWith<TimeoutCancellationException> {
      withTimeout(5) { platform.queryProducts(setOf("lifetime")) }
    }
    assertTrue(platform.isAvailable())
  }

  @Test fun classifiesAllUnfetchedStatuses() = runTest {
    val unfetched = mock(UnfetchedProduct::class.java)
    `when`(unfetched.productId).thenReturn("lifetime")
    `when`(unfetched.productType).thenReturn(BillingClient.ProductType.INAPP)
    doAnswer {
      it.getArgument<ProductDetailsResponseListener>(1).onProductDetailsResponse(result(),
        QueryProductDetailsResult.create(emptyList(), listOf(unfetched)))
      null
    }.`when`(billing).queryProductDetailsAsync(any(QueryProductDetailsParams::class.java), any(ProductDetailsResponseListener::class.java))
    for (code in listOf(UnfetchedProduct.StatusCode.PRODUCT_NOT_FOUND, UnfetchedProduct.StatusCode.NO_ELIGIBLE_OFFER)) {
      `when`(unfetched.statusCode).thenReturn(code)
      assertEquals(setOf("lifetime"), platform.queryProducts(setOf("lifetime")).notFoundProductIds)
    }
    for (code in listOf(UnfetchedProduct.StatusCode.UNKNOWN, UnfetchedProduct.StatusCode.INVALID_PRODUCT_ID_FORMAT, 999)) {
      `when`(unfetched.statusCode).thenReturn(code)
      assertEquals("product_query_failed_$code", assertFailsWith<GooglePlayIapStackException> {
        platform.queryProducts(setOf("lifetime"))
      }.code)
    }
  }

  @Test fun serializesCatalogRefreshAndCheckoutSelection() = runTest {
    val callbacks = mutableListOf<ProductDetailsResponseListener>()
    doAnswer { callbacks.add(it.getArgument(1)); null }.`when`(billing)
      .queryProductDetailsAsync(any(QueryProductDetailsParams::class.java), any(ProductDetailsResponseListener::class.java))
    val first = async(start = CoroutineStart.UNDISPATCHED) { platform.queryProducts(setOf("monthly")) }
    val second = async(start = CoroutineStart.UNDISPATCHED) { platform.queryProducts(setOf("monthly")) }
    val selected = GooglePlayProduct("monthly", GooglePlayProductKind.SUBSCRIPTION, "Monthly", "Access",
      "$4.99", 4_990_000, "USD", "old-token", "monthly-plan")
    val launch = async(start = CoroutineStart.UNDISPATCHED) { runCatching { platform.launchPurchase(selected, "opaque-customer") } }
    assertEquals(1, callbacks.size)
    assertFalse(launch.isCompleted)
    callbacks[0].onProductDetailsResponse(result(), QueryProductDetailsResult.create(emptyList(), emptyList()))
    first.await()
    runCurrent()
    assertEquals(2, callbacks.size)
    assertFalse(launch.isCompleted)
    callbacks[1].onProductDetailsResponse(result(), QueryProductDetailsResult.create(emptyList(), emptyList()))
    second.await()
    assertEquals("product_not_queried", (launch.await().exceptionOrNull() as GooglePlayIapStackException).code)
    verify(billing, never()).launchBillingFlow(any(Activity::class.java), any(BillingFlowParams::class.java))
  }

  @Test fun rejectsMalformedPricingPhasesWithTypedErrors() = runTest {
    val details = mock(ProductDetails::class.java)
    val offer = mock(ProductDetails.SubscriptionOfferDetails::class.java)
    val phases = mock(ProductDetails.PricingPhases::class.java)
    `when`(details.productId).thenReturn("monthly")
    `when`(details.productType).thenReturn(BillingClient.ProductType.SUBS)
    `when`(details.subscriptionOfferDetails).thenReturn(listOf(offer))
    `when`(offer.offerToken).thenReturn("token")
    `when`(offer.basePlanId).thenReturn("plan")
    `when`(offer.pricingPhases).thenReturn(phases)
    doAnswer {
      it.getArgument<ProductDetailsResponseListener>(1).onProductDetailsResponse(result(),
        QueryProductDetailsResult.create(listOf(details), emptyList()))
      null
    }.`when`(billing).queryProductDetailsAsync(any(QueryProductDetailsParams::class.java), any(ProductDetailsResponseListener::class.java))
    for (list in listOf(null, emptyList<ProductDetails.PricingPhase>())) {
      `when`(phases.pricingPhaseList).thenReturn(list)
      assertEquals("invalid_plugin_response", assertFailsWith<GooglePlayIapStackException> {
        platform.queryProducts(setOf("monthly"))
      }.code)
    }
    val corruptions: List<(ProductDetails.PricingPhase) -> Unit> = listOf(
      { `when`(it.billingCycleCount).thenReturn(-1) },
      { `when`(it.billingPeriod).thenReturn(null) },
      { `when`(it.billingPeriod).thenReturn("") },
      { `when`(it.formattedPrice).thenReturn(null) },
      { `when`(it.formattedPrice).thenReturn("") },
      { `when`(it.priceAmountMicros).thenReturn(-1) },
      { `when`(it.priceCurrencyCode).thenReturn(null) },
      { `when`(it.priceCurrencyCode).thenReturn("usd") },
      { `when`(it.recurrenceMode).thenReturn(999) },
    )
    for (corrupt in corruptions) {
      val phase = mock(ProductDetails.PricingPhase::class.java)
      `when`(phase.billingPeriod).thenReturn("P1M")
      `when`(phase.formattedPrice).thenReturn("$4.99")
      `when`(phase.priceAmountMicros).thenReturn(4_990_000)
      `when`(phase.priceCurrencyCode).thenReturn("USD")
      `when`(phase.recurrenceMode).thenReturn(ProductDetails.RecurrenceMode.INFINITE_RECURRING)
      corrupt(phase)
      `when`(phases.pricingPhaseList).thenReturn(listOf(phase))
      assertEquals("invalid_plugin_response", assertFailsWith<GooglePlayIapStackException> {
        platform.queryProducts(setOf("monthly"))
      }.code)
    }
  }

  @Test fun closesAndCancelsWithoutWaitingForMissingStoreCallback() = runTest {
    val waiting = async(start = CoroutineStart.UNDISPATCHED) { runCatching { platform.queryProducts(setOf("lifetime")) } }
    platform.close()
    assertEquals("platform_closed", (waiting.await().exceptionOrNull() as GooglePlayIapStackException).code)
    assertFalse(platform.isAvailable())
  }

  private fun result(code: Int = BillingClient.BillingResponseCode.OK) = BillingResult.newBuilder().setResponseCode(code).build()

  private fun purchase(state: Int, account: String = "opaque-customer"): Purchase {
    val purchase = mock(Purchase::class.java)
    val identifiers = mock(AccountIdentifiers::class.java)
    `when`(identifiers.obfuscatedAccountId).thenReturn(account)
    `when`(purchase.accountIdentifiers).thenReturn(identifiers)
    `when`(purchase.purchaseState).thenReturn(state)
    `when`(purchase.purchaseToken).thenReturn("store-token")
    `when`(purchase.products).thenReturn(listOf("lifetime"))
    return purchase
  }
}
