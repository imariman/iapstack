package com.iapstack.core

import com.google.gson.Gson
import com.google.gson.reflect.TypeToken
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.coroutines.withContext
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
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlin.random.Random
import kotlin.time.Duration
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.seconds
import java.time.DateTimeException
import java.time.Instant
import java.time.LocalDateTime
import java.time.ZoneOffset

private const val SDK_VERSION = "0.1.0-dev.1"
private val JSON_MEDIA_TYPE = "application/json".toMediaType()
private val DELAY_SECONDS = Regex("^\\d{1,9}$")
private val IMF_FIXDATE = Regex(
  "^(Mon|Tue|Wed|Thu|Fri|Sat|Sun), (\\d{2}) (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) (\\d{4}) (\\d{2}):(\\d{2}):(\\d{2}) GMT$",
)
private val WEEKDAYS = listOf("Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun")
private val MONTHS = listOf("Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec")

/**
 * Parses a `Retry-After` header with the grammar every IAPStack SDK shares:
 * delay-seconds of one to nine ASCII digits, or an IMF-fixdate HTTP-date
 * (RFC 9110 section 5.6.7) whose fields, including the weekday, round-trip
 * through the calendar. `DateTimeFormatter.RFC_1123_DATE_TIME` is not used
 * because it also accepts an omitted weekday and numeric offsets.
 *
 * Returns the cooldown, [Duration.ZERO] for a date that elapsed before [now],
 * and null for an absent or malformed header.
 */
internal fun parseRetryAfter(header: String?, now: Instant): Duration? {
  val value = header?.trim().orEmpty()
  if (value.isEmpty()) {
    return null
  }
  if (DELAY_SECONDS.matches(value)) {
    return value.toLong().seconds
  }
  val match = IMF_FIXDATE.matchEntire(value) ?: return null
  val (weekday, dayText, monthText, yearText, hourText, minuteText, secondText) = match.destructured
  val hour = hourText.toInt()
  val minute = minuteText.toInt()
  val second = secondText.toInt()
  if (hour > 23 || minute > 59 || second > 59) {
    return null
  }
  val at = try {
    LocalDateTime.of(yearText.toInt(), MONTHS.indexOf(monthText) + 1, dayText.toInt(), hour, minute, second)
  } catch (_: DateTimeException) {
    return null
  }
  if (WEEKDAYS[at.dayOfWeek.value - 1] != weekday) {
    return null
  }
  val instant = at.toInstant(ZoneOffset.UTC)
  return (instant.toEpochMilli() - now.toEpochMilli()).coerceAtLeast(0).milliseconds
}

/**
 * Provider-neutral SDK transport for IAPStack v1.
 *
 * Calls are safe from any dispatcher, including `Dispatchers.Main`: blocking
 * response-body reads run on [ioDispatcher].
 */
class IapStackClient(
  private val config: IapStackConfig,
  httpClient: OkHttpClient? = null,
  private val ioDispatcher: CoroutineDispatcher = Dispatchers.IO,
) {
  /** Wall clock used to resolve HTTP-date `Retry-After` values; only tests replace it. */
  internal var clock: () -> Instant = Instant::now

  /** Suspends between attempts; only tests replace it. */
  internal var sleep: suspend (Duration) -> Unit = { delay(it) }

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
    requireExternalCustomerId(externalCustomerId)
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
    if (body != null && body.isEmpty()) {
      throw IllegalArgumentException("body must not be empty")
    }

    for (attempt in 1..config.retryPolicy.maxAttempts) {
      val request = buildRequest(method, path, body, normalizedRequestId)
      try {
        val bodyText = withTimeout(config.timeout) {
          // OkHttp delivers headers on its own thread, but the body is read by a
          // blocking stream; never do that on the caller's (possibly main) thread.
          withContext(ioDispatcher) { execute(request) }
        }
        return decodeObject(bodyText)
      } catch (error: IapStackApiException) {
        if (attempt >= config.retryPolicy.maxAttempts || !error.retryable) {
          throw error
        }
        sleep(config.retryPolicy.delayAfter(attempt, jitter(), error.retryAfter))
      } catch (error: TimeoutCancellationException) {
        if (attempt >= config.retryPolicy.maxAttempts) {
          throw IapStackTimeoutException("IAPStack request timed out", error)
        }
        sleep(config.retryPolicy.delayAfter(attempt, jitter()))
      } catch (error: CancellationException) {
        throw error
      } catch (error: InterruptedIOException) {
        if (attempt >= config.retryPolicy.maxAttempts) {
          throw IapStackTimeoutException("IAPStack request timed out", error)
        }
        sleep(config.retryPolicy.delayAfter(attempt, jitter()))
      } catch (error: IOException) {
        if (attempt >= config.retryPolicy.maxAttempts) {
          throw IapStackTransportException(
            "IAPStack request failed before a response was received",
            error,
          )
        }
        sleep(config.retryPolicy.delayAfter(attempt, jitter()))
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
        throw apiErrorFromResponse(
          resp.code,
          rawBody,
          resp.header("X-Request-ID"),
          resp.header("Retry-After"),
        )
      }
    }
  }

  private fun jitter(): Double = random.nextDouble()

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
    requestIdHeader: String?,
    retryAfterHeader: String?,
  ): IapStackApiException {
    val headerRequestId = requestIdHeader?.trim()?.ifEmpty { null }
    val retryAfter = parseRetryAfter(retryAfterHeader, clock())
    val parsed = try {
      decodeObject(responseBody)
    } catch (_: Exception) {
      return IapStackApiException(
        statusCode = statusCode,
        code = "http_error",
        requestId = headerRequestId,
        retryable = statusCode == 429 || statusCode >= 500,
        message = "IAPStack returned an unsuccessful response",
        retryAfter = retryAfter,
      )
    }
    val errorObject = parsed["error"] as? Map<*, *>
    val code = (errorObject?.get("code") as? String)?.ifBlank { "http_error" } ?: "http_error"
    val message = (errorObject?.get("message") as? String)?.ifBlank {
      "IAPStack returned an unsuccessful response"
    } ?: "IAPStack returned an unsuccessful response"
    val requestId = (errorObject?.get("request_id") as? String)?.ifBlank { null } ?: headerRequestId
    return IapStackApiException(
      statusCode = statusCode,
      code = code,
      requestId = requestId,
      retryable = statusCode == 429 || statusCode >= 500,
      message = message,
      retryAfter = retryAfter,
    )
  }

  private fun validatePurchase(purchase: PurchaseSubmission) {
    requireExternalCustomerId(purchase.externalCustomerId)
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

/**
 * Requires one non-empty customer identity without surrounding whitespace; a
 * padded ID would silently address a different customer server-side.
 */
private fun requireExternalCustomerId(value: String) {
  if (value.isEmpty() || value.trim() != value) {
    throw IllegalArgumentException("externalCustomerId must be non-empty without surrounding whitespace")
  }
}
