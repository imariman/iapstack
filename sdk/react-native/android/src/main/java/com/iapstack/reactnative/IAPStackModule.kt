package com.iapstack.reactnative

import android.app.Activity
import android.content.Intent
import com.facebook.react.bridge.*
import com.facebook.react.modules.core.DeviceEventManagerModule
import com.iapstack.core.*
import com.iapstack.googleplay.*
import com.iapstack.huawei.*
import kotlinx.coroutines.*
import okhttp3.OkHttpClient
import java.net.URI
import kotlin.time.Duration.Companion.milliseconds

/** Thin native orchestration. Companion SDKs retain exact evidence and own verification/restore. */
class IAPStackModule(private val context: ReactApplicationContext) :
  ReactContextBaseJavaModule(context), ActivityEventListener, LifecycleEventListener {
  private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
  private var session: Session? = null
  private var listeners = 0
  // JS-driven HTTP calls share one pool and dispatcher instead of leaking one per call.
  private val transport = lazy { OkHttpClient() }

  private class Session(val customer: String, val client: IapStackClient) {
    var playPlatform: BillingClientGooglePlayPlatform? = null
    var huaweiPlatform: HmsHuaweiIapPlatform? = null
    var play: GooglePlayIapStack? = null
    var huawei: HuaweiIapStack? = null
    val products = mutableMapOf<String, Any>()
    val jobs = mutableSetOf<Job>()
    fun close() {
      jobs.toList().forEach { it.cancel() }
      playPlatform?.close()
      huaweiPlatform?.close()
      products.clear()
      client.close()
    }
  }

  init { context.addActivityEventListener(this); context.addLifecycleEventListener(this) }
  override fun getName() = "IAPStackStore"

  @ReactMethod fun requestSession(endpoint: String, loginToken: String, promise: Promise) {
    scope.launch {
      try {
        val session = IapStackSessionLoader(transport.value).load(endpoint, loginToken)
        promise.resolve(Arguments.makeNativeMap(mapOf(
          "base_url" to session.baseUri.toString(), "application_id" to session.applicationId,
          "external_customer_id" to session.externalCustomerId, "token" to session.customerToken,
          "expires_at" to session.expiresAt.toString(),
        )))
      } catch (error: CancellationException) { promise.reject("session_disposed", "Request cancelled")
      } catch (error: Exception) { reject(promise, error) }
    }
  }

  @ReactMethod fun httpRequest(config: ReadableMap, operation: String, customer: String,
                               payload: ReadableMap, requestId: String?, promise: Promise) {
    scope.launch {
      try {
        val retry = requireNotNull(config.getMap("retryPolicy"))
        val client = IapStackClient(httpClient = transport.value, config = IapStackConfig(
          baseUri = URI(requireNotNull(config.getString("baseUri"))),
          applicationId = requireNotNull(config.getString("applicationId")),
          customerToken = requireNotNull(config.getString("customerToken")),
          timeout = config.getDouble("timeoutMs").milliseconds,
          maxResponseBytes = config.getInt("maxResponseBytes"),
          allowInsecureHttp = config.getBoolean("allowInsecureHttp"),
          retryPolicy = IapStackRetryPolicy(retry.getInt("maxAttempts"),
            retry.getDouble("baseDelayMs").milliseconds, retry.getDouble("maxDelayMs").milliseconds),
        )).apply { sdkName = SDK_NAME }
        val result = when (operation) {
          "purchases:verify" -> verification(client.verifyPurchase(submission(payload.toHashMap()), requestId))
          "purchases:restore" -> mapOf("results" to client.restorePurchases(
            requireNotNull(payload.getArray("purchases")).toArrayList().map {
              @Suppress("UNCHECKED_CAST")
              submission(it as Map<String, Any?>)
            }, requestId).results.map(::verification))
          "entitlements" -> client.getEntitlements(customer, requestId).let {
            mapOf("customer_id" to it.customerId, "entitlements" to it.entitlements.map(::entitlement))
          }
          else -> error("Unsupported HTTP operation")
        }
        promise.resolve(Arguments.makeNativeMap(result))
      } catch (error: CancellationException) { promise.reject("session_disposed", "Request cancelled")
      } catch (error: Exception) { reject(promise, error) }
    }
  }

  @ReactMethod fun configure(config: ReadableMap, promise: Promise) {
    scope.launch {
      try {
        check(session == null) { "Dispose the current session before configuring another" }
        val storefront = config.getString("storefront")
        require(storefront == "google_play" || storefront == "huawei") { "Unsupported storefront on Android" }
        val customer = requireNotNull(config.getString("externalCustomerId"))
        require(customer.isNotBlank() && customer.trim() == customer)
        val kinds = requireNotNull(config.getMap("productKinds")).toHashMap().mapValues { (_, value) ->
          when (value) { "non_consumable" -> false; "subscription" -> true; else -> error("Unsupported product kind") }
        }
        require(kinds.isNotEmpty() && kinds.keys.all { it.isNotBlank() && it.trim() == it })
        val next = Session(customer, IapStackClient(IapStackConfig(
          baseUri = URI(requireNotNull(config.getString("baseUri"))),
          applicationId = requireNotNull(config.getString("applicationId")),
          customerToken = requireNotNull(config.getString("customerToken")),
          allowInsecureHttp = config.hasKey("allowInsecureHttp") && config.getBoolean("allowInsecureHttp"),
        )).apply { sdkName = SDK_NAME })
        try { install(next, storefront, customer, kinds) } catch (error: Exception) { next.close(); throw error }
        promise.resolve(null)
      } catch (error: Exception) { reject(promise, error) }
    }
  }

  private fun install(next: Session, storefront: String, customer: String, kinds: Map<String, Boolean>) {
    if (storefront == "google_play") {
      require(customer.length <= 64) { "Google Play customer ID exceeds 64 characters" }
      val catalog = kinds.mapValues { if (it.value) GooglePlayProductKind.SUBSCRIPTION else GooglePlayProductKind.NON_CONSUMABLE }
      val platform = BillingClientGooglePlayPlatform(context, catalog) { context.currentActivity }
      next.playPlatform = platform
      val companion = GooglePlayIapStack(next.client, catalog, platform)
      next.play = companion
      session = next
      val job = scope.launch {
        companion.purchaseUpdates.collect { purchase ->
          try {
            val result = companion.verifyPurchase(next.customer, purchase)
            emit(next, mapOf("type" to "verified", "result" to verification(result)))
          } catch (error: CancellationException) { throw error
          } catch (error: Exception) { emit(next, errorEvent(error)) }
        }
      }
      next.jobs.add(job)
    } else {
      val activity = context.currentActivity ?: error("A foreground Activity is required for Huawei")
      val platform = HmsHuaweiIapPlatform(activity)
      next.huaweiPlatform = platform
      next.huawei = HuaweiIapStack(next.client, kinds.mapValues {
        if (it.value) HuaweiProductKind.SUBSCRIPTION else HuaweiProductKind.NON_CONSUMABLE
      }, platform)
      session = next
    }
  }

  @ReactMethod fun isAvailable(promise: Promise) = operation(promise) { s ->
    s.play?.isAvailable() ?: s.huawei!!.isAvailable()
  }
  @ReactMethod fun resolveHuaweiEnvironment(promise: Promise) = operation(promise) { s ->
    (s.huaweiPlatform ?: error("Huawei is not configured")).resolveEnvironment()
  }
  @ReactMethod fun queryProducts(ids: ReadableArray, promise: Promise) = operation(promise) { s ->
    val wanted = ids.toArrayList().map { it as String }.toSet()
    s.products.entries.removeAll { (_, value) -> when (value) {
      is GooglePlayProduct -> value.id in wanted
      is HuaweiProduct -> value.id in wanted
      else -> true
    } }
    val products: List<Map<String, Any?>>
    val missing: Set<String>
    if (s.play != null) {
      val query = s.play!!.queryProducts(wanted)
      products = query.products.map { p ->
        s.products[p.selectionKey] = p
        mapOf("id" to p.id, "selectionKey" to p.selectionKey, "kind" to kind(p.kind == GooglePlayProductKind.SUBSCRIPTION),
          "title" to p.title, "description" to p.description, "price" to p.price, "currencyCode" to p.currencyCode,
          "basePlanId" to p.basePlanId, "offerId" to p.offerId, "offerTags" to p.offerTags,
          "pricingPhases" to p.pricingPhases.map { phase -> mapOf("billingCycleCount" to phase.billingCycleCount,
            "billingPeriod" to phase.billingPeriod, "formattedPrice" to phase.formattedPrice,
            "priceMicros" to phase.priceMicros.toString(), "currencyCode" to phase.currencyCode,
            "recurrence" to phase.recurrence.name.lowercase()) })
      }
      missing = query.notFoundProductIds
    } else {
      val query = s.huawei!!.queryProducts(wanted)
      products = query.products.map { p ->
        s.products[p.id] = p
        mapOf("id" to p.id, "selectionKey" to p.id, "kind" to kind(p.kind == HuaweiProductKind.SUBSCRIPTION),
          "title" to p.title, "description" to p.description, "price" to p.price, "currencyCode" to p.currency,
          "subscriptionPeriod" to p.subscriptionPeriod, "freeTrialPeriod" to p.freeTrialPeriod)
      }
      missing = query.notFoundProductIds
    }
    Arguments.makeNativeMap(mapOf("products" to products, "notFoundProductIds" to missing.toList()))
  }
  @ReactMethod fun purchase(selectionKey: String, requestId: String?, promise: Promise) = operation(promise) { s ->
    when (val product = s.products[selectionKey] ?: throw BridgeException("product_not_queried")) {
      is GooglePlayProduct -> { s.play!!.launchPurchase(s.customer, product); null }
      is HuaweiProduct -> Arguments.makeNativeMap(verification(s.huawei!!.purchaseAndVerify(s.customer, product, requestId)))
      else -> error("Unsupported product")
    }
  }
  @ReactMethod fun restore(requestId: String?, promise: Promise) = operation(promise) { s ->
    val result = s.play?.restorePurchases(s.customer, requestId) ?: s.huawei!!.restorePurchases(s.customer, requestId = requestId)
    Arguments.makeNativeMap(mapOf("results" to result.results.map(::verification)))
  }
  @ReactMethod fun getEntitlements(requestId: String?, promise: Promise) = operation(promise) { s ->
    val result = s.client.getEntitlements(s.customer, requestId)
    Arguments.makeNativeMap(mapOf("customer_id" to result.customerId, "entitlements" to result.entitlements.map(::entitlement)))
  }
  @ReactMethod fun dispose(promise: Promise) { scope.launch { clearSession(); promise.resolve(null) } }
  @ReactMethod fun addListener(eventName: String) { listeners++ }
  @ReactMethod fun removeListeners(count: Double) { listeners = maxOf(0, listeners - count.toInt()) }
  override fun onActivityResult(activity: Activity, requestCode: Int, resultCode: Int, data: Intent?) {
    session?.huaweiPlatform?.onActivityResult(requestCode, resultCode, data)
  }
  override fun onNewIntent(intent: Intent) = Unit
  override fun onHostResume() = Unit
  override fun onHostPause() = Unit
  override fun onHostDestroy() {
    // HMS retains an Activity. Never use that instance after rotation or destruction.
    session?.takeIf { it.huaweiPlatform != null }?.let {
      emit(it, mapOf("type" to "error", "code" to "session_invalidated", "message" to "Activity destroyed; configure a new customer session"))
      clearSession()
    }
  }
  override fun invalidate() {
    clearSession(); scope.cancel()
    if (transport.isInitialized()) transport.value.run { dispatcher.executorService.shutdown(); connectionPool.evictAll() }
    context.removeActivityEventListener(this); context.removeLifecycleEventListener(this)
    super.invalidate()
  }
  private fun clearSession() { val old = session; session = null; old?.close() }
  private fun operation(promise: Promise, action: suspend (Session) -> Any?) {
    scope.launch {
      val captured = session
      if (captured == null) { promise.reject("not_configured", "Configure a customer session first"); return@launch }
      val job = currentCoroutineContext().job
      captured.jobs.add(job)
      try {
        val result = action(captured)
        ensureActive()
        check(session === captured) { "Session changed" }
        promise.resolve(result)
      } catch (error: CancellationException) {
        promise.reject("session_disposed", "Customer session was disposed")
      } catch (error: Exception) { reject(promise, error)
      } finally { captured.jobs.remove(job) }
    }
  }
  private fun emit(captured: Session, event: Map<String, Any?>) {
    if (session === captured && listeners > 0 && context.hasActiveReactInstance()) {
      context.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter::class.java)
        .emit("IAPStackStoreUpdate", Arguments.makeNativeMap(event))
    }
  }
  private fun reject(promise: Promise, error: Exception) {
    val event = errorEvent(error)
    val info = if (error is IapStackApiException) Arguments.makeNativeMap(mapOf(
      "statusCode" to error.statusCode, "requestId" to error.requestId,
      "retryable" to error.retryable, "retryAfterMs" to error.retryAfter?.inWholeMilliseconds?.toDouble(),
    )) else Arguments.createMap()
    promise.reject(event["code"] as String, event["message"] as String, info)
  }
}

// Devices identify React Native traffic even though the canonical native clients send it.
private const val SDK_NAME = "react-native"
private class BridgeException(val code: String) : Exception(code)
private fun kind(subscription: Boolean) = if (subscription) "subscription" else "non_consumable"
private fun errorEvent(error: Exception): Map<String, Any?> {
  val code = when (error) {
    is BridgeException -> error.code
    is IapStackSessionException -> error.code
    is GooglePlayIapStackException -> error.code
    is HuaweiIapStackException -> error.code
    is IapStackApiException -> error.code
    is IapStackTimeoutException -> "timeout"
    is IapStackTransportException -> "transport_error"
    is IapStackProtocolException -> "protocol_error"
    else -> "invalid_state"
  }
  // Avoid forwarding provider/network exception text, which may contain purchase evidence or URLs.
  return mapOf("type" to "error", "code" to code, "message" to "IAPStack operation failed ($code)")
}
private fun verification(result: VerificationResult): Map<String, Any?> = mapOf(
  "verified_at" to result.verifiedAt.toString(), "customer_id" to result.customerId,
  "entitlements" to result.entitlements.map(::entitlement),
)
private fun entitlement(value: Entitlement): Map<String, Any?> = mapOf(
  "key" to value.key, "access" to value.access, "reason" to value.reason, "version" to value.version,
  "effective_starts_at" to value.effectiveStartsAt?.toString(), "effective_ends_at" to value.effectiveEndsAt?.toString(),
)

@Suppress("UNCHECKED_CAST")
private fun submission(value: Map<String, Any?>): PurchaseSubmission = PurchaseSubmission(
  externalCustomerId = value["external_customer_id"] as String,
  claimedProducts = value["claimed_products"] as List<String>,
  evidence = value["evidence"] as Map<String, Any?>,
  customerBindings = (value["customer_bindings"] as? List<Map<String, Any?>>).orEmpty().map {
    CustomerBinding(it["kind"] as String, it["value"] as String)
  },
)
