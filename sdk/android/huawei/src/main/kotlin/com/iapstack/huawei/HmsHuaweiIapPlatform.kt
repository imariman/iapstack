package com.iapstack.huawei

import android.app.Activity
import android.content.Intent
import com.huawei.hmf.tasks.Task
import com.huawei.hms.iap.Iap
import com.huawei.hms.iap.IapApiException
import com.huawei.hms.iap.IapClient
import com.huawei.hms.iap.entity.*
import com.huawei.hms.support.api.client.Status
import java.io.Closeable
import java.util.concurrent.Executor
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/**
 * HMS IAP adapter scoped to an Activity. Forward [onActivityResult] and call [close] on destroy.
 * Call [resolveEnvironment] from a user action if readiness reports hms_sign_in_required.
 * A cancelled coroutine does not dismiss store UI; another resolution is blocked until its
 * Activity result arrives. Restore on the next session to recover interrupted checkouts.
 */
class HmsHuaweiIapPlatform internal constructor(
  private val activity: Activity,
  private val client: IapClient,
  private val requestCode: Int,
) : HuaweiIapPlatform, Closeable {
  constructor(activity: Activity, requestCode: Int = 41380) :
    this(activity, Iap.getIapClient(activity), requestCode)

  init { require(requestCode in 0..65535) }
  private class Resolution {
    val result = CompletableDeferred<Intent?>()
    var launched = false
  }
  private var resolution: Resolution? = null
  private var closed = false

  override suspend fun isAvailable(): Boolean = withContext(Dispatchers.Main.immediate) {
    checkOpen()
    try {
      checkCode(client.isEnvReady.awaitHms().returnCode)
      true
    } catch (error: HuaweiIapStackException) {
      if (error.code == "hms_environment_unavailable") false else throw error
    }
  }

  /** Launches HMS's own sign-in/region resolution, when available, then rechecks readiness. */
  suspend fun resolveEnvironment(): Boolean = withContext(Dispatchers.Main.immediate) {
    withResolution { slot ->
      try {
        checkCode(client.isEnvReady.awaitRaw().returnCode)
      } catch (error: IapApiException) {
        val status = error.status
        if (status == null || !status.hasResolution()) {
          if (error.statusCode == OrderStatusCode.ORDER_ACCOUNT_AREA_NOT_SUPPORTED) return@withResolution false
          throw hmsError(error.statusCode)
        }
        val data = resolve(status, slot)
        if (data == null) throw hmsError(OrderStatusCode.ORDER_STATE_CANCEL)
        return@withResolution isAvailable()
      } catch (error: HuaweiIapStackException) {
        if (error.code == "hms_environment_unavailable") return@withResolution false
        throw error
      }
      true
    }
  }

  override suspend fun sandboxStatus(): HuaweiSandboxStatus = withContext(Dispatchers.Main.immediate) {
    checkOpen()
    val result = client.isSandboxActivated(IsSandboxActivatedReq()).awaitHms()
    checkCode(result.returnCode)
    HuaweiSandboxStatus(result.isSandboxUser == true, result.isSandboxApk == true,
      result.versionFrMarket, result.versionInApk)
  }

  override suspend fun queryProducts(productIds: List<String>, productKind: HuaweiProductKind): List<HuaweiProduct> =
    withContext(Dispatchers.Main.immediate) {
      checkOpen()
      require(productIds.isNotEmpty() && productIds.all { it.isNotBlank() })
      productIds.chunked(20).flatMap { ids ->
        val result = client.obtainProductInfo(ProductInfoReq().apply {
          priceType = productKind.priceType
          this.productIds = ids
        }).awaitHms()
        checkCode(result.returnCode)
        result.productInfoList.orEmpty().map { info ->
          val id = info?.productId
          val price = info?.price
          val currency = info?.currency
          if (info == null || id.isNullOrBlank() || id !in ids || info.priceType != productKind.priceType ||
            price.isNullOrBlank() || info.microsPrice < 0 || currency == null ||
            !Regex("^[A-Z]{3}$").matches(currency)) {
            throw HuaweiIapStackException("invalid_plugin_response", "Huawei returned incomplete product data")
          }
          // HMS exposes micros/status as primitives: zero is valid, not evidence of a missing field.
          HuaweiProduct(id, productKind, info.productName?.takeIf { it.isNotBlank() } ?: id,
            info.productDesc.orEmpty(), price, info.microsPrice, currency, info.status,
            info.originalLocalPrice, info.originalMicroPrice, info.subSpecialPrice,
            info.subSpecialPriceMicros, info.subPeriod, info.subFreeTrialPeriod)
        }
      }
    }

  override suspend fun purchase(productId: String, productKind: HuaweiProductKind, developerPayload: String): HuaweiSignedPurchase =
    withContext(Dispatchers.Main.immediate) {
      checkOpen()
      require(productId.isNotBlank() && developerPayload.isNotBlank())
      withResolution { slot ->
        if (!isAvailable()) throw hmsError(OrderStatusCode.ORDER_ACCOUNT_AREA_NOT_SUPPORTED)
        val result = client.createPurchaseIntent(PurchaseIntentReq().apply {
          this.productId = productId
          priceType = productKind.priceType
          this.developerPayload = developerPayload
        }).awaitHms()
        checkCode(result.returnCode)
        val status = result.status ?: throw HuaweiIapStackException("missing_resolution", "Huawei checkout is unavailable")
        val data = resolve(status, slot) ?: throw hmsError(OrderStatusCode.ORDER_STATE_CANCEL)
        val purchase = client.parsePurchaseResultInfoFromIntent(data)
          ?: throw HuaweiIapStackException("invalid_plugin_response", "Huawei returned no purchase result")
        checkCode(purchase.returnCode)
        signedPurchase(purchase.inAppPurchaseData, purchase.inAppDataSignature)
      }
    }

  override suspend fun ownedPurchases(productKind: HuaweiProductKind, continuationToken: String?): HuaweiOwnedPurchasesPage =
    withContext(Dispatchers.Main.immediate) {
      checkOpen()
      val result = client.obtainOwnedPurchases(OwnedPurchasesReq().apply {
        priceType = productKind.priceType
        this.continuationToken = continuationToken
      }).awaitHms()
      checkCode(result.returnCode)
      val data = result.inAppPurchaseDataList.orEmpty()
      val signatures = result.inAppSignature.orEmpty()
      if (data.size != signatures.size) throw HuaweiIapStackException(
        "invalid_plugin_response", "Huawei returned mismatched purchase data and signatures")
      HuaweiOwnedPurchasesPage(data.zip(signatures) { payload, signature -> signedPurchase(payload, signature) },
        result.continuationToken?.takeIf { it.isNotEmpty() })
    }

  /** Return true when this adapter consumed the result; forward other request codes normally. */
  fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?): Boolean {
    if (requestCode != this.requestCode || resolution?.launched != true) return false
    val waiting = resolution!!.result
    resolution = null
    // HMS encodes its authoritative returnCode in data, even when Android's resultCode is cancelled.
    if (data != null) waiting.complete(data)
    else if (resultCode == Activity.RESULT_CANCELED) waiting.complete(null)
    else waiting.completeExceptionally(HuaweiIapStackException("invalid_plugin_response", "Huawei returned no Activity result"))
    return true
  }

  private suspend fun resolve(status: Status, slot: Resolution): Intent? {
    checkOpen()
    if (!status.hasResolution()) throw HuaweiIapStackException("missing_resolution", "Huawei resolution is unavailable")
    if (activity.isFinishing || activity.isDestroyed) throw HuaweiIapStackException("activity_unavailable", "A foreground Activity is required")
    slot.launched = true
    try {
      status.startResolutionForResult(activity, requestCode)
    } catch (_: Exception) {
      slot.launched = false
      throw HuaweiIapStackException("resolution_failed", "Could not open Huawei UI")
    }
    return slot.result.await()
  }

  // Called on Main: reserve before readiness/intent requests can suspend.
  private suspend fun <T> withResolution(block: suspend (Resolution) -> T): T {
    checkOpen()
    if (resolution != null) throw HuaweiIapStackException("purchase_in_progress", "Huawei checkout or resolution is already in progress")
    val slot = Resolution()
    resolution = slot
    try {
      return block(slot)
    } finally {
      if (!slot.launched && resolution === slot) {
        resolution = null
        slot.result.cancel()
      }
    }
  }

  private fun checkOpen() {
    if (closed) throw HuaweiIapStackException("platform_closed", "Huawei adapter is closed")
  }

  override fun close() {
    closed = true
    resolution?.result?.cancel()
    resolution = null
  }
}

private suspend fun <T : Any> Task<T>.awaitRaw(): T = withTimeoutOrNull(30_000) {
  suspendCancellableCoroutine { continuation ->
    val direct = Executor { it.run() }
    addOnSuccessListener(direct) { if (continuation.isActive) continuation.resume(it) }
    addOnFailureListener(direct) { if (continuation.isActive) continuation.resumeWithException(it) }
    addOnCanceledListener(direct) { continuation.cancel() }
  }
} ?: throw HuaweiIapStackException("hms_timeout", "Huawei IAP request timed out")

private suspend fun <T : Any> Task<T>.awaitHms(): T = try {
  awaitRaw()
} catch (error: IapApiException) {
  throw hmsError(error.statusCode)
} catch (error: HuaweiIapStackException) {
  throw error
} catch (error: kotlinx.coroutines.CancellationException) {
  throw error
} catch (_: Exception) {
  throw HuaweiIapStackException("hms_unavailable", "Huawei IAP request failed")
}

private fun checkCode(code: Int) {
  if (code != OrderStatusCode.ORDER_STATE_SUCCESS) throw hmsError(code)
}

private fun hmsError(code: Int) = HuaweiIapStackException(
  code = when (code) {
    OrderStatusCode.ORDER_STATE_CANCEL -> "purchase_cancelled"
    OrderStatusCode.ORDER_HWID_NOT_LOGIN -> "hms_sign_in_required"
    OrderStatusCode.ORDER_ACCOUNT_AREA_NOT_SUPPORTED -> "hms_environment_unavailable"
    else -> "hms_error_$code"
  },
  message = "Huawei IAP request failed (code $code)",
  userCancelled = code == OrderStatusCode.ORDER_STATE_CANCEL,
)

private fun signedPurchase(data: String?, signature: String?): HuaweiSignedPurchase {
  if (data.isNullOrBlank() || signature.isNullOrBlank()) throw HuaweiIapStackException(
    "invalid_plugin_response", "Huawei returned incomplete signed purchase evidence")
  // Deliberately no parsing, normalization, trimming, or JSON serialization here.
  return HuaweiSignedPurchase(data, signature)
}
