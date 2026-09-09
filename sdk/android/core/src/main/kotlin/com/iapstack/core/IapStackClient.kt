package com.iapstack.core

import com.google.gson.Gson
import com.google.gson.reflect.TypeToken
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.ResponseBody
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InterruptedIOException
import java.util.concurrent.TimeUnit
import java.net.SocketTimeoutException
import kotlin.math.abs
import kotlin.random.Random

private const val SDK_VERSION = "0.1.0-dev.1"
private val JSON_MEDIA_TYPE = "application/json".toMediaType()

/**
 * Provider-neutral SDK transport for IAPStack v1.
 */
class IapStackClient(
  private val config: IapStackConfig,
  private val httpClient: OkHttpClient? = null,
) {
  private val gson = Gson()
  private val random = Random(System.nanoTime())
  private val closeClient: Boolean
  private val client: OkHttpClient
  private val mapType = object : TypeToken<Map<String, Any?>>() {}.type

  init {
    config.validate()
    closeClient = httpClient == null
    client = httpClient ?: OkHttpClient()
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
    return VerificationResult.fromJson(json)
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
    return RestoreResult.fromJson(json)
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
    return EntitlementSnapshot.fromJson(json)
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

  private suspend fun request(
    method: String,
    path: List<String>,
    body: Map<String, Any?>? = null,
    requestId: String? = null,
  ): Map<String, Any?> {
    val normalizedRequestId = requestId?.trim()?.ifEmpty { null }
    val normalizedTimeoutMillis = config.timeout.inWholeMilliseconds
    if (normalizedTimeoutMillis <= 0L) {
      throw IllegalArgumentException("timeout must be positive")
    }
    if (body != null && body.isEmpty()) {
      throw IllegalArgumentException("body must not be empty")
    }

    for (attempt in 1..config.retryPolicy.maxAttempts) {
      val request = buildRequest(method, path, body, normalizedRequestId)
      val timeoutClient = client.newBuilder()
        .callTimeout(normalizedTimeoutMillis, TimeUnit.MILLISECONDS)
        .connectTimeout(normalizedTimeoutMillis, TimeUnit.MILLISECONDS)
        .readTimeout(normalizedTimeoutMillis, TimeUnit.MILLISECONDS)
        .writeTimeout(normalizedTimeoutMillis, TimeUnit.MILLISECONDS)
        .build()

      try {
        val bodyText = withContext(Dispatchers.IO) {
          timeoutClient.newCall(request).execute().use { response ->
            val responseBody = response.body ?: throw IapStackProtocolException("IAPStack response was empty")
            val rawBody = collectResponseBody(responseBody)
            if (response.isSuccessful) {
              return@use rawBody
            }
            val statusCode = response.code
            throw apiErrorFromResponse(statusCode, rawBody)
          }
        }
        return decodeObject(bodyText)
      } catch (error: IapStackApiException) {
        if (attempt >= config.retryPolicy.maxAttempts || !error.retryable) {
          throw error
        }
        delay(config.retryPolicy.delayAfter(attempt, jitter()))
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

  private fun jitter(): Double = abs(random.nextDouble())

  private fun buildRequest(
    method: String,
    path: List<String>,
    body: Map<String, Any?>?,
    requestId: String?,
  ): Request {
    val urlBuilder = config.baseUrl.newBuilder()
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
      rawBody.toRequestBody(JSON_MEDIA_TYPE)
    }

    return requestBuilder
      .method(method, requestBody)
      .build()
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
      gson.fromJson(value, mapType) as? Map<String, Any?> ?: throw IapStackProtocolException(
        "IAPStack response was not a JSON object",
      )
    } catch (error: Exception) {
      if (error is IapStackProtocolException) {
        throw error
      }
      throw IapStackProtocolException("IAPStack returned an invalid JSON object", error)
    }

  private fun apiErrorFromResponse(
    statusCode: Int,
    responseBody: String,
  ): IapStackApiException {
    val parsed = try {
      decodeObject(responseBody)
    } catch (error: Exception) {
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
