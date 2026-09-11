package com.iapstack.googleplay

import com.iapstack.core.IapStackClient
import com.iapstack.core.IapStackConfig
import com.iapstack.core.IapStackRetryPolicy
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import java.net.URI
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertSame
import kotlin.test.assertTrue
import kotlin.time.Duration.Companion.seconds

class GooglePlayIapStackTest {
  @Test
  fun delegatesBillingAvailabilityChecks() = runBlocking {
    val googlePlay = googlePlay(platform = FakePlatform(available = false))
    assertFalse(googlePlay.isAvailable())
  }

  @Test
  fun requiresANonEmptyCatalogWithUniqueNormalizedIds() {
    val client = unusedClient()
    assertFailsWith<IllegalArgumentException> {
      GooglePlayIapStack(client, emptyMap(), FakePlatform())
    }
    assertFailsWith<IllegalArgumentException> {
      GooglePlayIapStack(
        client,
        mapOf(
          "premium" to GooglePlayProductKind.NON_CONSUMABLE,
          " premium " to GooglePlayProductKind.NON_CONSUMABLE,
        ),
        FakePlatform(),
      )
    }
  }

  @Test
  fun rejectsAccountIdentifiersLongerThanTheBillingLimit() = runBlocking {
    val platform = FakePlatform()
    val googlePlay = googlePlay(platform = platform)
    assertFailsWith<IllegalArgumentException> {
      googlePlay.launchPurchase(
        externalCustomerId = "a".repeat(65),
        product = lifetimeProduct(),
      )
    }
    assertNull(platform.launchedProduct)
  }

  @Test
  fun queriesConfiguredProductsAndLaunchesWithCustomerBinding() = runBlocking {
    val product = monthlyProduct()
    val platform = FakePlatform(
      productQuery = GooglePlayProductQuery(listOf(product), emptySet()),
    )
    val googlePlay = googlePlay(platform = platform)

    val query = googlePlay.queryProducts(setOf("premium_monthly"))
    googlePlay.launchPurchase(externalCustomerId = "customer-1", product = query.products.single())

    assertSame(product, query.products.single())
    assertEquals(setOf("premium_monthly"), platform.queriedProductIds)
    assertSame(product, platform.launchedProduct)
    assertEquals("customer-1", platform.launchedAccountId)
  }

  @Test
  fun verifyPurchaseSendsPurchaseTokenEvidence() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(MockResponse().setBody(VERIFICATION_JSON).setResponseCode(200))
      val client = client(server)
      val googlePlay = googlePlay(client = client)
      val purchase = GooglePlayPurchase(
        purchaseToken = "token-abc",
        productIds = listOf("premium_lifetime"),
        status = GooglePlayPurchaseStatus.PURCHASED,
        isAcknowledged = false,
        obfuscatedAccountId = "customer-1",
      )

      val result = googlePlay.verifyPurchase("customer-1", purchase)
      val recorded = server.takeRequest()
      val body = recorded.body.readUtf8()

      assertTrue(body.contains("\"purchase_token\":\"token-abc\""))
      assertTrue(body.contains("\"product_kind\":\"non_consumable\""))
      assertTrue(result.entitlements.single().grantsAccess)
      client.close()
    }
  }

  private fun googlePlay(
    platform: FakePlatform = FakePlatform(),
    client: IapStackClient = unusedClient(),
  ) = GooglePlayIapStack(
    client = client,
    productKinds = mapOf(
      "premium_lifetime" to GooglePlayProductKind.NON_CONSUMABLE,
      "premium_monthly" to GooglePlayProductKind.SUBSCRIPTION,
    ),
    platform = platform,
  )

  private fun unusedClient() = IapStackClient(
    IapStackConfig(
      baseUri = URI.create("https://iap.example"),
      applicationId = "application-1",
      customerToken = "customer-token",
    ),
    httpClient = OkHttpClient(),
  )

  private fun client(server: MockWebServer) = IapStackClient(
    IapStackConfig(
      baseUri = URI.create(server.url("/proxy").toString().trimEnd('/')),
      applicationId = "application-1",
      customerToken = "customer-token",
      timeout = 1.seconds,
      retryPolicy = IapStackRetryPolicy(maxAttempts = 1),
      allowInsecureHttp = true,
    ),
    httpClient = OkHttpClient(),
  )

  private fun lifetimeProduct() = GooglePlayProduct(
    id = "premium_lifetime",
    kind = GooglePlayProductKind.NON_CONSUMABLE,
    title = "Premium Lifetime",
    description = "Premium access",
    price = "$49.99",
    priceMicros = 49_990_000,
    currencyCode = "USD",
  )

  private fun monthlyProduct() = GooglePlayProduct(
    id = "premium_monthly",
    kind = GooglePlayProductKind.SUBSCRIPTION,
    title = "Premium Monthly",
    description = "Premium access",
    price = "$4.99",
    priceMicros = 4_990_000,
    currencyCode = "USD",
    offerToken = "base-plan-offer",
    basePlanId = "monthly",
    pricingPhases = listOf(
      GooglePlayPricingPhase(
        billingCycleCount = 0,
        billingPeriod = "P1M",
        formattedPrice = "$4.99",
        priceMicros = 4_990_000,
        currencyCode = "USD",
        recurrence = GooglePlayPricingRecurrence.INFINITE,
      ),
    ),
  )

  class FakePlatform(
    private val available: Boolean = true,
    private val productQuery: GooglePlayProductQuery = GooglePlayProductQuery(emptyList(), emptySet()),
  ) : GooglePlayIapPlatform {
    override val purchaseUpdates: Flow<GooglePlayPurchase> = emptyFlow()
    var queriedProductIds: Set<String>? = null
    var launchedProduct: GooglePlayProduct? = null
    var launchedAccountId: String? = null

    override suspend fun isAvailable(): Boolean = available

    override suspend fun queryProducts(productIds: Set<String>): GooglePlayProductQuery {
      queriedProductIds = productIds
      return productQuery
    }

    override suspend fun launchPurchase(product: GooglePlayProduct, obfuscatedAccountId: String) {
      launchedProduct = product
      launchedAccountId = obfuscatedAccountId
    }

    override suspend fun ownedPurchases(obfuscatedAccountId: String): List<GooglePlayPurchase> = emptyList()
  }

  companion object {
    private const val VERIFICATION_JSON =
      """{"verified_at":"2026-08-24T20:00:00Z","customer_id":"customer-internal","entitlements":[{"key":"premium","access":"allowed","reason":"purchase_valid","version":1}]}"""
  }
}
