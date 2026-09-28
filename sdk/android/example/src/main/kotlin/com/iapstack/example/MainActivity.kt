package com.iapstack.example

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.WindowManager
import android.widget.*
import com.google.gson.JsonParser
import com.iapstack.core.*
import com.iapstack.googleplay.*
import com.iapstack.huawei.*
import java.net.URI
import kotlinx.coroutines.*
import okhttp3.*
import okhttp3.RequestBody.Companion.toRequestBody

/** Manual store harness: runtime credentials are never saved, bundled, or logged. */
class MainActivity : Activity() {
  private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
  private var client: IapStackClient? = null
  private var playPlatform: BillingClientGooglePlayPlatform? = null
  private var hmsPlatform: HmsHuaweiIapPlatform? = null
  private lateinit var status: TextView
  private lateinit var products: LinearLayout
  private lateinit var controls: LinearLayout
  private var play: GooglePlayIapStack? = null
  private var huawei: HuaweiIapStack? = null
  private var customer = ""
  private var productId = ""

  override fun onCreate(savedInstanceState: Bundle?) {
    super.onCreate(savedInstanceState)
    window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
    val layout = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setPadding(24, 24, 24, 24) }
    setContentView(ScrollView(this).apply { addView(layout) })
    fun input(hint: String, secret: Boolean = false) = EditText(this).apply {
      this.hint = hint
      isSaveEnabled = false
      inputType = if (secret) InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD else InputType.TYPE_CLASS_TEXT
      importantForAutofill = android.view.View.IMPORTANT_FOR_AUTOFILL_NO
      layout.addView(this)
    }
    val host = input("Trusted host HTTPS session URL")
    val login = input("Host login token (not the IAPStack application bearer)", true)
    val id = input("Store product ID")
    val subscription = CheckBox(this).apply { text = "Subscription"; layout.addView(this) }
    val provider = Spinner(this).apply {
      adapter = ArrayAdapter(this@MainActivity, android.R.layout.simple_spinner_dropdown_item, listOf("Google Play", "Huawei"))
      layout.addView(this)
    }
    status = TextView(this).apply { layout.addView(this) }
    val connect = Button(this).apply { text = "Start customer session"; layout.addView(this) }
    controls = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; layout.addView(this) }
    products = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; layout.addView(this) }
    connect.setOnClickListener {
      val endpoint = host.text.toString()
      val hostToken = login.text.toString()
      val usePlay = provider.selectedItemPosition == 0
      val isSubscription = subscription.isChecked
      productId = id.text.toString().trim()
      login.text.clear()
      connect.isEnabled = false
      runAction {
        require(productId.isNotEmpty())
        val session = mintSession(endpoint, hostToken)
        customer = session.get("external_customer_id").asString
        client = IapStackClient(IapStackConfig(URI(session.get("base_url").asString),
          session.get("application_id").asString, session.get("token").asString))
        if (usePlay) {
          val catalog = mapOf(productId to if (isSubscription) GooglePlayProductKind.SUBSCRIPTION else GooglePlayProductKind.NON_CONSUMABLE)
          playPlatform = BillingClientGooglePlayPlatform(applicationContext, catalog) { this@MainActivity }
          play = GooglePlayIapStack(client!!, catalog, playPlatform!!)
          scope.launch {
            play!!.purchaseUpdates.collect { purchase ->
              runAction {
                when (purchase.status) {
                  GooglePlayPurchaseStatus.PURCHASED, GooglePlayPurchaseStatus.RESTORED -> {
                    play!!.verifyPurchase(customer, purchase)
                    refreshEntitlements()
                  }
                  GooglePlayPurchaseStatus.PENDING -> status.text = "Payment pending. Access waits for server verification."
                  GooglePlayPurchaseStatus.CANCELLED -> status.text = "Purchase cancelled."
                  GooglePlayPurchaseStatus.FAILED -> status.text = "Store purchase failed. Query or restore to retry."
                }
              }
            }
          }
        } else {
          hmsPlatform = HmsHuaweiIapPlatform(this@MainActivity)
          val catalog = mapOf(productId to if (isSubscription) HuaweiProductKind.SUBSCRIPTION else HuaweiProductKind.NON_CONSUMABLE)
          huawei = HuaweiIapStack(client!!, catalog, hmsPlatform!!)
          button("Resolve Huawei sign-in") { hmsPlatform!!.resolveEnvironment(); status.text = "Huawei environment ready." }
          button("Check Huawei sandbox") {
            status.text = if (huawei!!.sandboxStatus().isActive) "Huawei sandbox active." else "Huawei sandbox is not active."
          }
        }
        button("Query products") { queryProducts() }
        button("Restore purchases") { restore() }
        button("Read entitlements") { refreshEntitlements() }
        // Recovers purchases completed while the app was stopped or checkout was interrupted.
        restore()
        queryProducts()
      }
    }
  }

  private fun button(label: String, action: suspend () -> Unit) {
    controls.addView(Button(this).apply { text = label; setOnClickListener { runAction(action) } })
  }

  private suspend fun queryProducts() {
    products.removeAllViews()
    if (play != null) {
      for (product in play!!.queryProducts(setOf(productId)).products) {
        products.addView(Button(this).apply {
          text = "${product.title} — ${product.price} ${product.basePlanId.orEmpty()} ${product.offerId.orEmpty()}"
          setOnClickListener { runAction { play!!.launchPurchase(customer, product) } }
        })
      }
    } else {
      check(huawei!!.isAvailable())
      for (product in huawei!!.queryProducts(setOf(productId)).products) {
        products.addView(Button(this).apply {
          text = "${product.title} — ${product.price}"
          setOnClickListener { runAction { huawei!!.purchaseAndVerify(customer, product); refreshEntitlements() } }
        })
      }
    }
    if (products.childCount == 0) status.text = "Product unavailable for this store account."
  }

  private suspend fun restore() {
    if (play != null) play!!.restorePurchases(customer) else huawei!!.restorePurchases(customer)
    refreshEntitlements()
  }

  private suspend fun refreshEntitlements() {
    val snapshot = client!!.getEntitlements(customer)
    status.text = snapshot.entitlements.joinToString("\n") { "${it.key}: ${if (it.grantsAccess) "active" else "inactive"}" }
      .ifEmpty { "No active entitlements." }
  }

  private fun runAction(action: suspend () -> Unit) {
    scope.launch {
      try { action() }
      catch (cancelled: CancellationException) { throw cancelled }
      catch (error: Exception) {
        // Display only classified error codes, never exception bodies, tokens, or signed evidence.
        status.text = when (error) {
          is GooglePlayIapStackException -> error.code
          is HuaweiIapStackException -> error.code
          else -> "Request failed. Check configuration or restart to obtain a fresh session."
        }
      }
    }
  }

  private suspend fun mintSession(endpoint: String, login: String) = withContext(Dispatchers.IO) {
    val uri = URI(endpoint)
    require(uri.scheme == "https" && uri.host != null && uri.userInfo == null && uri.fragment == null)
    require(login.isNotBlank())
    val http = OkHttpClient.Builder().followRedirects(false).followSslRedirects(false)
      .callTimeout(20, java.util.concurrent.TimeUnit.SECONDS).build()
    try {
      http.newCall(Request.Builder().url(endpoint).header("Authorization", "Bearer $login")
        .post(ByteArray(0).toRequestBody()).build()).execute().use { response ->
        check(response.isSuccessful)
        val bytes = response.peekBody(65537).bytes()
        check(bytes.size <= 65536)
        JsonParser.parseString(bytes.toString(Charsets.UTF_8)).asJsonObject
      }
    } finally {
      http.connectionPool.evictAll()
      http.dispatcher.executorService.shutdown()
    }
  }

  @Deprecated("Activity result forwarding for HMS")
  override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
    if (hmsPlatform?.onActivityResult(requestCode, resultCode, data) != true) super.onActivityResult(requestCode, resultCode, data)
  }

  override fun onDestroy() {
    scope.cancel()
    playPlatform?.close()
    hmsPlatform?.close()
    client?.close()
    super.onDestroy()
  }
}
