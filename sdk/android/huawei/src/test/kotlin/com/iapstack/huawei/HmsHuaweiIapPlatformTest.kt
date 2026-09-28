package com.iapstack.huawei

import android.app.Activity
import android.content.Intent
import com.google.gson.JsonParser
import com.huawei.hmf.tasks.Task
import com.huawei.hmf.tasks.TaskCompletionSource
import com.huawei.hms.iap.IapApiException
import com.huawei.hms.iap.IapClient
import com.huawei.hms.iap.entity.*
import com.huawei.hms.support.api.client.Status
import com.iapstack.core.IapStackClient
import com.iapstack.core.IapStackConfig
import com.iapstack.core.IapStackRetryPolicy
import java.net.URI
import kotlinx.coroutines.*
import kotlinx.coroutines.test.*
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.mockito.ArgumentMatchers.any
import org.mockito.ArgumentMatchers.anyInt
import org.mockito.Mockito.*
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import kotlin.test.*

@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [28])
class HmsHuaweiIapPlatformTest {
  private val client = mock(IapClient::class.java)
  private val activity = mock(Activity::class.java)
  private lateinit var platform: HmsHuaweiIapPlatform
  private val kind = HuaweiProductKind.NON_CONSUMABLE
  private val payload = " {\n  \"productId\": \"lifetime\", \"developerPayload\": \"opaque-customer\", \"extra\": \"\\u0061\"\n} "

  @Before fun setup() {
    Dispatchers.setMain(UnconfinedTestDispatcher())
    platform = HmsHuaweiIapPlatform(activity, client, 41380)
    `when`(client.isEnvReady).thenReturn(success(IsEnvReadyResult()))
  }
  @After fun teardown() { platform.close(); Dispatchers.resetMain() }

  @Test fun classifiesEnvironmentAndSignInFailures() = runTest {
    `when`(client.isEnvReady).thenReturn(failure(IapApiException(Status(OrderStatusCode.ORDER_HWID_NOT_LOGIN))))
    assertEquals("hms_sign_in_required", assertFailsWith<HuaweiIapStackException> { platform.isAvailable() }.code)
    `when`(client.isEnvReady).thenReturn(success(IsEnvReadyResult().apply { returnCode = OrderStatusCode.ORDER_ACCOUNT_AREA_NOT_SUPPORTED }))
    assertEquals("hms_environment_unavailable", assertFailsWith<HuaweiIapStackException> { platform.isAvailable() }.code)
    verify(client, never()).createPurchaseIntent(any(PurchaseIntentReq::class.java))
  }

  @Test fun resolvesSignInAndRechecksReadiness() = runTest {
    val status = mock(Status::class.java)
    `when`(status.statusCode).thenReturn(OrderStatusCode.ORDER_HWID_NOT_LOGIN)
    `when`(status.hasResolution()).thenReturn(true)
    val signInFailure = failure<IsEnvReadyResult>(IapApiException(status))
    `when`(client.isEnvReady).thenReturn(signInFailure, success(IsEnvReadyResult()))
    doAnswer {
      platform.onActivityResult(41380, Activity.RESULT_OK, Intent())
      null
    }.`when`(status).startResolutionForResult(any(Activity::class.java), anyInt())
    assertTrue(platform.resolveEnvironment())
    verify(client, times(2)).isEnvReady
  }

  @Test fun exposesSandboxAndLargeProductPrices() = runTest {
    `when`(client.isSandboxActivated(any(IsSandboxActivatedReq::class.java))).thenReturn(success(IsSandboxActivatedResult().apply {
      isSandboxUser = true; isSandboxApk = true; versionFrMarket = "market"; versionInApk = "apk"
    }))
    assertTrue(platform.sandboxStatus().isActive)
    val info = ProductInfo().apply {
      productId = "lifetime"; priceType = 1; productName = "Lifetime"; productDesc = "Access"
      price = "¥5,000"; microsPrice = 5_000_000_000L; currency = "JPY"
    }
    doAnswer {
      val req = it.getArgument<ProductInfoReq>(0)
      assertEquals(1, req.priceType)
      assertEquals(listOf("lifetime"), req.productIds)
      success(ProductInfoResult().apply { productInfoList = listOf(info) })
    }.`when`(client).obtainProductInfo(any(ProductInfoReq::class.java))
    assertEquals(5_000_000_000L, platform.queryProducts(listOf("lifetime"), kind).single().priceMicros)
  }

  @Test fun preservesSignedCheckoutPayloadAllTheWayToHttp() = runTest {
    checkout(PurchaseResultInfo().apply { inAppPurchaseData = payload; inAppDataSignature = "signature" })
    val signed = platform.purchase("lifetime", kind, "opaque-customer")
    assertEquals(payload, signed.purchaseData)
    MockWebServer().use { server ->
      server.enqueue(MockResponse().setResponseCode(200).setBody("""{"verified_at":"2026-09-29T00:00:00Z","customer_id":"customer","entitlements":[]}"""))
      val http = IapStackClient(IapStackConfig(URI(server.url("/").toString()), "app", "customer-token", allowInsecureHttp = true,
        retryPolicy = IapStackRetryPolicy(maxAttempts = 1)))
      try {
        withContext(Dispatchers.Default) {
          http.verifyPurchase(HuaweiPurchaseEvidence(signed, kind).toSubmission("opaque-customer", "lifetime"))
        }
        val body = JsonParser.parseString(server.takeRequest().body.readUtf8()).asJsonObject
        assertEquals(payload, body.getAsJsonObject("evidence").get("purchase_data").asString)
      } finally { http.close() }
    }
  }

  @Test fun checkoutCancellationDoesNotProduceEvidence() = runTest {
    checkout(PurchaseResultInfo().apply { returnCode = OrderStatusCode.ORDER_STATE_CANCEL })
    assertTrue(assertFailsWith<HuaweiIapStackException> { platform.purchase("lifetime", kind, "opaque-customer") }.userCancelled)
    assertFalse(platform.onActivityResult(10, Activity.RESULT_OK, Intent()))
  }

  @Test fun forwardsContinuationAndRejectsMismatchedSignatures() = runTest {
    doAnswer {
      val req = it.getArgument<OwnedPurchasesReq>(0)
      assertEquals("page-2", req.continuationToken)
      assertEquals(1, req.priceType)
      success(OwnedPurchasesResult().apply {
        inAppPurchaseDataList = listOf(payload); inAppSignature = listOf("signature"); continuationToken = ""
      })
    }.`when`(client).obtainOwnedPurchases(any(OwnedPurchasesReq::class.java))
    val page = platform.ownedPurchases(kind, "page-2")
    assertNull(page.continuationToken)
    assertEquals(payload, page.purchases.single().purchaseData)
    `when`(client.obtainOwnedPurchases(any(OwnedPurchasesReq::class.java))).thenReturn(success(OwnedPurchasesResult().apply {
      inAppPurchaseDataList = listOf(payload); inAppSignature = emptyList()
    }))
    assertEquals("invalid_plugin_response", assertFailsWith<HuaweiIapStackException> { platform.ownedPurchases(kind, "page-2") }.code)
  }

  @Test fun cancelledCoroutineKeepsUiSlotUntilItsResultArrives() = runTest {
    val status = mock(Status::class.java)
    `when`(status.hasResolution()).thenReturn(true)
    `when`(client.createPurchaseIntent(any(PurchaseIntentReq::class.java))).thenReturn(success(PurchaseIntentResult().apply { this.status = status }))
    val pending = async(start = CoroutineStart.UNDISPATCHED) { platform.purchase("lifetime", kind, "opaque-customer") }
    pending.cancelAndJoin()
    assertEquals("purchase_in_progress", assertFailsWith<HuaweiIapStackException> { platform.purchase("lifetime", kind, "opaque-customer") }.code)
    assertTrue(platform.onActivityResult(41380, Activity.RESULT_CANCELED, null))
    platform.close()
    assertEquals("platform_closed", assertFailsWith<HuaweiIapStackException> { platform.isAvailable() }.code)
  }

  private fun checkout(result: PurchaseResultInfo) {
    val status = mock(Status::class.java)
    `when`(status.hasResolution()).thenReturn(true)
    doAnswer {
      val req = it.getArgument<PurchaseIntentReq>(0)
      assertEquals("opaque-customer", req.developerPayload)
      assertEquals("lifetime", req.productId)
      assertEquals(1, req.priceType)
      success(PurchaseIntentResult().apply { this.status = status })
    }.`when`(client).createPurchaseIntent(any(PurchaseIntentReq::class.java))
    `when`(client.parsePurchaseResultInfoFromIntent(any(Intent::class.java))).thenReturn(result)
    doAnswer {
      platform.onActivityResult(41380, Activity.RESULT_OK, Intent())
      null
    }.`when`(status).startResolutionForResult(any(Activity::class.java), anyInt())
  }

  private fun <T> success(value: T): Task<T> = TaskCompletionSource<T>().apply { setResult(value) }.task
  private fun <T> failure(error: Exception): Task<T> = TaskCompletionSource<T>().apply { setException(error) }.task
}
