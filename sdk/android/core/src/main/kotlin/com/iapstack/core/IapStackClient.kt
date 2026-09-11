package com.iapstack.core

import com.google.gson.Gson
import com.google.gson.reflect.TypeToken
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withTimeout
import okhttp3.Call
import okhttp3.Callback
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okio.BufferedSink
import okhttp3.Response
import okhttp3.ResponseBody
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InterruptedIOException
import java.net.SocketTimeoutException
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlin.math.abs
import kotlin.random.Random
import kotlin.time.Duration

private const val SDK_VERSION = "0.1.0-dev.1"
private val JSON_MEDIA_TYPE = "application/json".toMediaType()

/**
 * Provider-neutral SDK transport for IAPStack v1.
 */
class IapStackClient(
  private val config: IapStackConfig,
  httpClient: OkHttpClient? = null,
) {
  private val gson = Gson()
  private val random = Random(System.nanoTime())
  private val closeClient: Boolean
  private val client: OkHttpClient
  private val mapType = object : TypeToken<Map<String, Any?>>() {}.type

  init {
    config.validate()
    closeClient = httpClient == null
    val timeoutMillis = config.timeout.inWholeMilliseconds
    client = (httpClient ?: OkHttpClient()).newBuilder()
      .callTimeout(timeoutMillis, TimeUnit.MILLISECONDS)
      .connectTimeout(timeoutMillis, TimeUnit.MILLISECONDS)
      .readTimeout(timeoutMillis, TimeUnit.MILLISECONDS)
      .writeTimeout(timeoutMillis, TimeUnit.MILLISECONDS)
      .build()
  }

  /**
   * Sends one purchase verification request and returns the entitlement projection.
   */
  suspend fun verifyPurchase(
    purchase: PurchaseSubmission,
    requestId: String? = null,
  ): VerificationResult {
    validatePurchase(purchase)
    val json = request(
      method = "POST",
      path = listOf("v1", "applications", config.applicationId, "purchases:verify"),
      body = purchase.toJson(),
      requestId = requestId,
    )
    return decode { VerificationResult.fromJson(json) }
  }

  /**
   * Verifies a bounded set of purchased rows.
   */
  suspend fun restorePurchases(
    purchases: List<PurchaseSubmission>,
    requestId: String? = null,
  ): RestoreResult {
    if (purchases.isEmpty() || purchases.size > 100) {
      throw IllegalArgumentException("purchases must contain between 1 and 100 items")
    }
    purchases.forEach(::validatePurchase)
    val json = request(
      method = "POST",
      path = listOf("v1", "applications", config.applicationId, "purchases:restore"),
      body = mapOf("purchases" to purchases.map(PurchaseSubmission::toJson)),
      requestId = requestId,
    )
    return decode { RestoreResult.fromJson(json) }
  }

  /**
   * Loads the current entitlement snapshot without provider interaction.
   */
  suspend fun getEntitlements(
    externalCustomerId: String,
    requestId: String? = null,
  ): EntitlementSnapshot {
    if (externalCustomerId.isBlank()) {
      throw IllegalArgumentException("externalCustomerId must not be empty")
    }
    val json = request(
      method = "GET",
      path = listOf(
        "v1",
        "applications",
        config.applicationId,
        "customers",
        externalCustomerId,
        "entitlements",
      ),
      requestId = requestId,
    )
    return decode { EntitlementSnapshot.fromJson(json) }
  }

  /**
   * Releases a custom HTTP transport when the SDK created it.
   */
  fun close() {
    if (closeClient) {
      client.connectionPool.evictAll()
      client.dispatcher.executorService.shutdown()
    }
  }

  private fun <T> decode(block: () -> T): T =
    try {
      block()
    } catch (error: IapStackProtocolException) {
      throw error
    } catch (error: RuntimeException) {
      throw IapStackProtocolException("IAPStack response did not match the v1 contract", error)
    }

  private suspend fun request(
    method: String,
    path: List<String>,
    body: Map<String, Any?>? = null,
    requestId: String? = null,
  ): Map<String, Any?> {
    val normalizedRequestId = requestId?.trim()?.ifEmpty { null }
    if (config.timeout <= Duration.ZERO) {
      throw IllegalArgumentException("timeout must be positive")
    }
    if (body != null && body.isEmpty()) {
      throw IllegalArgumentException("body must not be empty")
    }

    for (attempt in 1..config.retryPolicy.maxAttempts) {
      val request = buildRequest(method, path, body, normalizedRequestId)
      try {
        val bodyText = withTimeout(config.timeout) {
          execute(request)
        }
        return decodeObject(bodyText)
      } catch (error: IapStackApiException) {
        if (attempt >= config.retryPolicy.maxAttempts || !error.retryable) {
          throw error
        }
        delay(config.retryPolicy.delayAfter(attempt, jitter()))
      } catch (error: TimeoutCancellationException) {
        if (attempt >= config.retryPolicy.maxAttempts) {
          throw IapStackTimeoutException("IAPStack request timed out", error)
        }
        delay(config.retryPolicy.delayAfter(attempt, jitter()))
      } catch (error: CancellationException) {
        throw error
      } catch (error: SocketTimeoutException) {
        if (attempt >= config.retryPolicy.maxAttempts) {
          throw IapStackTimeoutException("IAPStack request timed out", error)
        }
        delay(config.retryPolicy.delayAfter(attempt, jitter()))
      } catch (error: InterruptedIOException) {
        if (attempt >= config.retryPolicy.maxAttempts) {
          throw IapStackTimeoutException("IAPStack request timed out", error)
        }
        delay(config.retryPolicy.delayAfter(attempt, jitter()))
      } catch (error: IOException) {
        if (attempt >= config.retryPolicy.maxAttempts) {
          throw IapStackTransportException(
            "IAPStack request failed before a response was received",
            error,
          )
        }
        delay(config.retryPolicy.delayAfter(attempt, jitter()))
      } catch (error: IapStackProtocolException) {
        throw error
      }
    }
    throw IapStackTransportException("IAPStack request exhausted the retry policy")
  }

  private suspend fun execute(request: Request): String {
    val call = client.newCall(request)
    val response = suspendCancellableCoroutine<Response> { cont ->
      cont.invokeOnCancellation { call.cancel() }
      call.enqueue(object : Callback {
        override fun onFailure(call: Call, e: IOException) {
          if (cont.isActive) {
            cont.resumeWithException(e)
          }
        }

        override fun onResponse(call: Call, response: Response) {
          if (!cont.isActive) {
            response.close()
            return
          }
          cont.resume(response)
        }
      })
    }
    return response.use { resp ->
      val responseBody = resp.body
        ?: throw IapStackProtocolException("IAPStack response was empty")
      val rawBody = collectResponseBody(responseBody)
      if (resp.isSuccessful) {
        rawBody
      } else {
        throw apiErrorFromResponse(resp.code, rawBody)
      }
    }
  }

  private fun jitter(): Double = abs(random.nextDouble())

  private fun buildRequest(
    method: String,
    path: List<String>,
    body: Map<String, Any?>?,
    requestId: String?,
  ): Request {
    val urlBuilder = config.baseUri.toString().toHttpUrl().newBuilder()
    for (segment in path) {
      urlBuilder.addPathSegment(segment)
    }
    val requestBuilder = Request.Builder()
      .url(urlBuilder.build())
      .addHeader("Accept", "application/json")
      .addHeader("Authorization", "Bearer ${config.customerToken}")
      .addHeader("X-IAPStack-SDK", "android-kotlin/$SDK_VERSION")

    if (requestId != null && requestId.isBlank()) {
      throw IllegalArgumentException("requestId cannot be blank")
    }
    requestId?.let {
      requestBuilder.addHeader("X-Request-ID", it)
    }

    val requestBody = body?.let {
      val rawBody = gson.toJson(it)
      requestBuilder.addHeader("Content-Type", "application/json")
      jsonRequestBody(rawBody)
    }

    return requestBuilder
      .method(method, requestBody)
      .build()
  }

  private fun jsonRequestBody(rawBody: String): RequestBody =
    object : RequestBody() {
      override fun contentType() = JSON_MEDIA_TYPE

      override fun contentLength() = rawBody.toByteArray(Charsets.UTF_8).size.toLong()

      override fun writeTo(sink: BufferedSink) {
        sink.writeString(rawBody, Charsets.UTF_8)
      }
    }

  private fun collectResponseBody(responseBody: ResponseBody): String {
    val raw = ByteArrayOutputStream()
    responseBody.byteStream().use { stream ->
      val buffer = ByteArray(4096)
      var total = 0
      while (true) {
        val read = stream.read(buffer)
        if (read < 0) {
          break
        }
        total += read
        if (total > config.maxResponseBytes) {
          throw IapStackProtocolException("IAPStack response exceeded the configured size limit")
        }
        raw.write(buffer, 0, read)
      }
    }
    return raw.toString(Charsets.UTF_8)
  }

  private fun decodeObject(value: String): Map<String, Any?> =
    try {
      asObjectMap(gson.fromJson(value, mapType))
        ?: throw IapStackProtocolException("IAPStack response was not a JSON object")
    } catch (error: IapStackProtocolException) {
      throw error
    } catch (error: RuntimeException) {
      throw IapStackProtocolException("IAPStack returned an invalid JSON object", error)
    }

  private fun apiErrorFromResponse(
    statusCode: Int,
    responseBody: String,
  ): IapStackApiException {
    val parsed = try {
      decodeObject(responseBody)
    } catch (_: Exception) {
      return IapStackApiException(
        statusCode = statusCode,
        code = "http_error",
        requestId = null,
        retryable = statusCode == 429 || statusCode >= 500,
        message = "IAPStack returned an unsuccessful response",
      )
    }
    val errorObject = parsed["error"] as? Map<*, *>
    val code = (errorObject?.get("code") as? String)?.ifBlank { "http_error" } ?: "http_error"
    val message = (errorObject?.get("message") as? String)?.ifBlank {
      "IAPStack returned an unsuccessful response"
    } ?: "IAPStack returned an unsuccessful response"
    val requestId = errorObject?.get("request_id") as? String
    return IapStackApiException(
      statusCode = statusCode,
      code = code,
      requestId = requestId,
      retryable = statusCode == 429 || statusCode >= 500,
      message = message,
    )
  }

  private fun validatePurchase(purchase: PurchaseSubmission) {
    if (purchase.externalCustomerId.isBlank()) {
      throw IllegalArgumentException("externalCustomerId must not be empty")
    }
    if (purchase.claimedProducts.isEmpty() || purchase.evidence.isEmpty()) {
      throw IllegalArgumentException("purchase must contain claimed products and evidence")
    }
    if (purchase.claimedProducts.any(String::isBlank)) {
      throw IllegalArgumentException("claimed products must not be empty")
    }
    if (purchase.claimedProducts.size != purchase.claimedProducts.toSet().size) {
      throw IllegalArgumentException("claimed products must be unique")
    }
  }
}
