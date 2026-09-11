package com.iapstack.huawei

import com.iapstack.core.IapStackClient
import com.iapstack.core.IapStackConfig
import com.iapstack.core.IapStackRetryPolicy
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import java.net.URI
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue
import kotlin.time.Duration.Companion.seconds

class HuaweiIapStackTest {
  @Test
  fun purchasesWithCustomerBindingAndPreservesSignedData() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(MockResponse().setBody(VERIFICATION_JSON).setResponseCode(200))
      val client = client(server)
      val purchaseData = purchaseData("premium_monthly", "customer-1")
      val platform = FakePlatform(
        products = listOf(monthlyProduct()),
        purchaseResult = HuaweiSignedPurchase(purchaseData, "signature-premium_monthly"),
      )
      val huawei = HuaweiIapStack(client, productKinds, platform)

      val products = huawei.queryProducts(setOf("premium_monthly"))
      val result = huawei.purchaseAndVerify(
        externalCustomerId = "customer-1",
        product = products.products.single(),
      )
      val body = server.takeRequest().body.readUtf8()

      assertEquals("premium_monthly", platform.purchasedProductId)
      assertEquals("customer-1", platform.purchasedDeveloperPayload)
      assertTrue(body.contains("\"external_customer_id\":\"customer-1\""))
      assertTrue(body.contains(purchaseData.replace("\"", "\\\"")))
      assertTrue(body.contains("\"signature\":\"signature-premium_monthly\""))
      assertTrue(body.contains("\"product_kind\":\"subscription\""))
      assertTrue(result.entitlements.single().grantsAccess)
      client.close()
    }
  }

  @Test
  fun exposesSandboxEligibilityBeforePurchase() = runBlocking {
    val expected = HuaweiSandboxStatus(
      isSandboxUser = true,
      isSandboxApk = true,
      marketVersion = "42",
      apkVersion = "43",
    )
    val huawei = HuaweiIapStack(
      unusedClient(),
      productKinds,
      FakePlatform(sandboxResult = expected),
    )
    val status = huawei.sandboxStatus()
    assertEquals(expected, status)
    assertTrue(status.isActive)
  }

  @Test
  fun skipsRestoreRowsWithMismatchedDeveloperPayload() = runBlocking {
    val platform = FakePlatform(
      owned = listOf(
        HuaweiSignedPurchase(
          purchaseData("premium_monthly", "other-customer"),
          "signature-other",
        ),
      ),
    )
    val huawei = HuaweiIapStack(unusedClient(), productKinds, platform)
    val result = huawei.restorePurchases("customer-1")
    assertTrue(result.results.isEmpty())
  }

  @Test
  fun rejectsRepeatedContinuationTokens() = runBlocking {
    val platform = FakePlatform(
      ownedPages = listOf(
        HuaweiOwnedPurchasesPage(emptyList(), continuationToken = "page-1"),
        HuaweiOwnedPurchasesPage(emptyList(), continuationToken = "page-1"),
      ),
    )
    val huawei = HuaweiIapStack(unusedClient(), productKinds, platform)
    val error = assertFailsWith<HuaweiIapStackException> {
      huawei.restorePurchases("customer-1")
    }
    assertEquals("restore_pagination_cycle", error.code)
  }

  @Test
  fun evidenceRequiresValidJsonAndKeepsExactSignedBytes() {
    val purchaseData = purchaseData("premium_monthly", "customer-1")
    val evidence = HuaweiPurchaseEvidence(
      purchaseData = purchaseData,
      signature = "sig",
      productKind = HuaweiProductKind.SUBSCRIPTION,
    )
    assertEquals("premium_monthly", evidence.productId)
    assertEquals(purchaseData, evidence.toJson()["purchase_data"])
    assertFailsWith<HuaweiIapStackException> {
      HuaweiPurchaseEvidence("not-json", "sig", HuaweiProductKind.SUBSCRIPTION)
    }
  }

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

  private fun monthlyProduct() = HuaweiProduct(
    id = "premium_monthly",
    kind = HuaweiProductKind.SUBSCRIPTION,
    title = "Premium Monthly",
    description = "Premium access",
    price = "4.99",
    priceMicros = 4_990_000,
    currency = "USD",
    status = 0,
  )

  private fun purchaseData(productId: String, customerId: String) =
    """{"productId":"$productId","developerPayload":"$customerId"}"""

  class FakePlatform(
    private val products: List<HuaweiProduct> = emptyList(),
    private val purchaseResult: HuaweiSignedPurchase = HuaweiSignedPurchase("{}", "sig"),
    private val sandboxResult: HuaweiSandboxStatus = HuaweiSandboxStatus(false, false),
    private val owned: List<HuaweiSignedPurchase> = emptyList(),
    private val ownedPages: List<HuaweiOwnedPurchasesPage>? = null,
  ) : HuaweiIapPlatform {
    var purchasedProductId: String? = null
    var purchasedDeveloperPayload: String? = null
    private var pageIndex = 0

    override suspend fun isAvailable(): Boolean = true

    override suspend fun sandboxStatus(): HuaweiSandboxStatus = sandboxResult

    override suspend fun queryProducts(
      productIds: List<String>,
      productKind: HuaweiProductKind,
    ): List<HuaweiProduct> = products.filter { it.id in productIds && it.kind == productKind }

    override suspend fun purchase(
      productId: String,
      productKind: HuaweiProductKind,
      developerPayload: String,
    ): HuaweiSignedPurchase {
      purchasedProductId = productId
      purchasedDeveloperPayload = developerPayload
      return purchaseResult
    }

    override suspend fun ownedPurchases(
      productKind: HuaweiProductKind,
      continuationToken: String?,
    ): HuaweiOwnedPurchasesPage {
      if (ownedPages != null) {
        val page = ownedPages[pageIndex.coerceAtMost(ownedPages.lastIndex)]
        pageIndex++
        return page
      }
      return HuaweiOwnedPurchasesPage(owned, continuationToken = null)
    }
  }

  companion object {
    private val productKinds = mapOf(
      "premium_monthly" to HuaweiProductKind.SUBSCRIPTION,
      "premium_lifetime" to HuaweiProductKind.NON_CONSUMABLE,
    )
    private const val VERIFICATION_JSON =
      """{"verified_at":"2026-08-24T20:00:00Z","customer_id":"customer-internal","entitlements":[{"key":"premium","access":"allowed","reason":"purchase_valid","version":1}]}"""
  }
}
