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

private const val SDK_VERSION = "0.1.0-sdk.3"
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

  /** Product name in `X-IAPStack-SDK`; only in-module wrappers such as the React Native bridge replace it. */
  internal var sdkName = "android-kotlin"

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
      // Keep customer credentials and signed evidence at the configured endpoint.
      .followRedirects(false)
      .followSslRedirects(false)
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
    val expectedResults = purchases.size
    val json = request(
      method = "POST",
      path = listOf("v1", "applications", config.applicationId, "purchases:restore"),
      body = mapOf("purchases" to purchases.map(PurchaseSubmission::toJson)),
      requestId = requestId,
    )
    val result = decode { RestoreResult.fromJson(json) }
    if (result.results.size != expectedResults) {
      throw IapStackProtocolException("IAPStack restore response did not contain one result per purchase")
    }
    return result
  }

  /**
   * Verifies any number of purchases in order, in batches of 100.
   *
   * An empty list does not contact IAPStack. Each batch after the first suffixes
   * [requestId] with `-2`, `-3`, and so on. When a later batch fails after an
   * earlier one succeeded, those results stay on
   * [IapStackException.committedRestoreResults]. A failure before any batch
   * succeeds is the original error, including when [skipped] is non-empty.
   * Cancellation is not rewritten.
   */
  suspend fun restorePurchasesInBatches(
    purchases: List<PurchaseSubmission>,
    requestId: String? = null,
    skipped: RestoreSkipCounts = RestoreSkipCounts(),
    pageLimitReached: Boolean = false,
  ): RestoreResult {
    val results = mutableListOf<VerificationResult>()
    var start = 0
    var batchIndex = 0
    while (start < purchases.size) {
      val end = minOf(start + 100, purchases.size)
      try {
        val batch = restorePurchases(
          purchases = purchases.subList(start, end),
          requestId = batchRequestId(requestId, batchIndex),
        )
        results.addAll(batch.results)
      } catch (cancelled: CancellationException) {
        throw cancelled
      } catch (error: IapStackException) {
        // Attach progress only after a batch has committed. A first-batch
        // failure stays the original error so callers still match it.
        if (results.isNotEmpty()) {
          error.attachPartialRestore(results.toList(), skipped, pageLimitReached)
        }
        throw error
      } catch (error: Exception) {
        if (results.isEmpty()) {
          throw error
        }
        throw IapStackIncompleteRestoreException(
          committedResults = results.toList(),
          skipped = skipped,
          pageLimitReached = pageLimitReached,
          cause = error,
        )
      }
      start = end
      batchIndex++
    }
    return RestoreResult(
      results = results.toList(),
      skipped = skipped,
      pageLimitReached = pageLimitReached,
    )
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
      .addHeader("X-IAPStack-SDK", "$sdkName/$SDK_VERSION")

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
 * Requires one non-empty customer identity in valid Unicode without
 * surrounding whitespace or control characters.
 *
 * This mirrors the server's identifier rule (`validateIdentifier` in
 * `internal/core/identifiers.go`), so an ID the server would reject fails
 * before any request is sent: valid UTF-8, which for a Kotlin string means no
 * unpaired surrogate (UTF-8 encoding would replace it), no C0
 * (U+0000-U+001F), DEL, or C1 (U+0080-U+009F) control character (the
 * [Char.isISOControl] set), and no leading or trailing whitespace as Go's
 * `strings.TrimSpace` defines it. The shared cases in
 * `contracts/test-vectors/external-customer-ids.json` pin this copy to the
 * server. The React Native Android bridge compiles these sources into its own
 * module and calls this function too.
 */
internal fun requireExternalCustomerId(value: String) {
  if (value.isEmpty() ||
    isGoSpace(value.first()) ||
    isGoSpace(value.last()) ||
    value.any { it.isISOControl() } ||
    hasUnpairedSurrogate(value)
  ) {
    throw IllegalArgumentException(
      "externalCustomerId must be non-empty valid Unicode without surrounding whitespace or control characters",
    )
  }
}

/**
 * Go's `unicode.IsSpace`, which the server's `strings.TrimSpace` strips. An
 * explicit set keeps the edge check independent of the runtime's Unicode
 * tables, which [String.trim] depends on. Every member is one UTF-16 unit.
 */
private fun isGoSpace(char: Char): Boolean = when (char.code) {
  in 0x09..0x0d, 0x20, 0x85, 0xa0, 0x1680, in 0x2000..0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000 -> true
  else -> false
}

private fun hasUnpairedSurrogate(value: String): Boolean {
  var index = 0
  while (index < value.length) {
    val char = value[index]
    if (char.isHighSurrogate() && index + 1 < value.length && value[index + 1].isLowSurrogate()) {
      index += 2
      continue
    }
    if (char.isSurrogate()) return true
    index++
  }
  return false
}

private fun batchRequestId(requestId: String?, batchIndex: Int): String? {
  if (requestId == null || requestId.trim().isEmpty()) {
    return null
  }
  val normalized = requestId.trim()
  return if (batchIndex == 0) normalized else "$normalized-${batchIndex + 1}"
}
