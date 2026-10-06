package com.iapstack.core

import com.google.gson.JsonParser
import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.Call
import okhttp3.Callback
import okhttp3.CookieJar
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InterruptedIOException
import java.net.URI
import java.time.Instant
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/** Short-lived customer credentials minted by the app's authenticated trusted host. */
class IapStackCustomerSession(
  val baseUri: URI,
  val applicationId: String,
  val externalCustomerId: String,
  val customerToken: String,
  val expiresAt: Instant,
) {
  /** Client configuration with the SDK defaults for timeout, retries and response size. */
  val config: IapStackConfig
    get() = IapStackConfig(baseUri = baseUri, applicationId = applicationId, customerToken = customerToken)

  /** Omits the bearer and customer identity so sessions never leak through logs. */
  override fun toString() = "IapStackCustomerSession(applicationId=$applicationId, expiresAt=$expiresAt)"
}

/**
 * A trusted-host session request failed. [code] is stable and never contains response data:
 * `invalid_session_request` (nothing was sent), `session_unavailable`, `invalid_session_response`,
 * `session_expired`, `timeout` or `transport_error`.
 */
class IapStackSessionException(val code: String, cause: Throwable? = null) :
  Exception("Customer session request failed ($code)", cause)

/**
 * Exchanges the app's own login token for an IAPStack customer session at a trusted host.
 *
 * The host contract is `POST <endpoint>` with `Authorization: Bearer <login token>` and body
 * `{}`, answered by `base_url`, `application_id`, `external_customer_id`, `token` and
 * `expires_at`. The login bearer is sent only to the HTTPS endpoint: redirects, cookies and
 * caches are disabled, the response is capped at [MAX_RESPONSE_BYTES], and the request at
 * 15 seconds without progress and 20 seconds overall. Never pass the durable application bearer.
 * An injected [httpClient] keeps its connection pool and dispatcher; [close] releases only a
 * transport the loader created.
 */
class IapStackSessionLoader(httpClient: OkHttpClient? = null) {
  private val closeClient = httpClient == null
  private val client = (httpClient ?: OkHttpClient()).newBuilder()
    .followRedirects(false)
    .followSslRedirects(false)
    .cache(null)
    .cookieJar(CookieJar.NO_COOKIES)
    .connectTimeout(15, TimeUnit.SECONDS)
    .readTimeout(15, TimeUnit.SECONDS)
    .writeTimeout(15, TimeUnit.SECONDS)
    .callTimeout(20, TimeUnit.SECONDS)
    .build()

  /** Wall clock used to reject expired sessions; only tests replace it. */
  internal var clock: () -> Instant = Instant::now

  /** Requests a session; throws [IapStackSessionException] or `CancellationException`. */
  suspend fun load(endpoint: String, loginToken: String): IapStackCustomerSession {
    val url = endpoint.toHttpUrlOrNull()
    if (url == null || !url.isHttps || url.username.isNotEmpty() || url.password.isNotEmpty() ||
      url.query != null || url.fragment != null || loginToken.isEmpty() || loginToken.any(Char::isWhitespace)
    ) {
      throw IapStackSessionException("invalid_session_request")
    }
    val request = Request.Builder()
      .url(url)
      .header("Authorization", "Bearer $loginToken")
      .header("Accept", "application/json")
      .post("{}".toRequestBody("application/json".toMediaType()))
      .build()
    val body = try {
      fetch(request)
    } catch (error: IapStackSessionException) {
      throw error
    } catch (error: InterruptedIOException) {
      throw IapStackSessionException("timeout", error)
    } catch (error: IOException) {
      throw IapStackSessionException("transport_error", error)
    }
    return decode(body)
  }

  /** Releases the transport when the loader created it. */
  fun close() {
    if (closeClient) {
      client.connectionPool.evictAll()
      client.dispatcher.executorService.shutdown()
    }
  }

  private suspend fun fetch(request: Request): ByteArray = suspendCancellableCoroutine { continuation ->
    val call = client.newCall(request)
    continuation.invokeOnCancellation { call.cancel() }
    call.enqueue(object : Callback {
      override fun onFailure(call: Call, e: IOException) {
        if (continuation.isActive) continuation.resumeWithException(e)
      }

      override fun onResponse(call: Call, response: Response) {
        try {
          val bytes = response.use(::readBounded)
          if (continuation.isActive) continuation.resume(bytes)
        } catch (error: Exception) {
          if (continuation.isActive) continuation.resumeWithException(error)
        }
      }
    })
  }

  private fun readBounded(response: Response): ByteArray {
    if (response.code != 200) throw IapStackSessionException("session_unavailable")
    val body = response.body ?: throw IapStackSessionException("invalid_session_response")
    if (body.contentLength() > MAX_RESPONSE_BYTES) throw IapStackSessionException("invalid_session_response")
    val bytes = ByteArrayOutputStream()
    body.byteStream().use { stream ->
      val chunk = ByteArray(4096)
      while (true) {
        val count = stream.read(chunk, 0, minOf(chunk.size, MAX_RESPONSE_BYTES + 1 - bytes.size()))
        if (count < 0) break
        bytes.write(chunk, 0, count)
        if (bytes.size() > MAX_RESPONSE_BYTES) throw IapStackSessionException("invalid_session_response")
      }
    }
    return bytes.toByteArray()
  }

  private fun decode(body: ByteArray): IapStackCustomerSession {
    val session = try {
      val json = JsonParser.parseString(body.toString(Charsets.UTF_8)).asJsonObject
      fun field(key: String): String {
        val value = json[key]
        require(value != null && value.isJsonPrimitive && value.asJsonPrimitive.isString && value.asString.isNotEmpty())
        return value.asString
      }
      IapStackCustomerSession(
        baseUri = URI(field("base_url")),
        applicationId = field("application_id"),
        externalCustomerId = field("external_customer_id").also { require(it.trim() == it) },
        customerToken = field("token"),
        expiresAt = Instant.parse(field("expires_at")),
      ).also { it.config.validate() }
    } catch (error: Exception) {
      // Parser messages can quote response text, so the cause is deliberately dropped.
      throw IapStackSessionException("invalid_session_response")
    }
    if (!session.expiresAt.isAfter(clock())) throw IapStackSessionException("session_expired")
    return session
  }

  companion object {
    /** Largest accepted session response. */
    const val MAX_RESPONSE_BYTES = 16_384
  }
}
