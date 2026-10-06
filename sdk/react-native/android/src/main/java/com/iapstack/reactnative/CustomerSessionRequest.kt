package com.iapstack.reactnative

import com.google.gson.JsonParser
import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.Call
import okhttp3.Callback
import okhttp3.CookieJar
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

/** Optional bootstrap for the example trusted host, with credentials confined to one HTTPS origin. */
internal suspend fun requestCustomerSession(endpoint: String, loginToken: String,
                                           transport: OkHttpClient = OkHttpClient()): Map<String, String> {
  val url = endpoint.toHttpUrl()
  require(url.isHttps && url.username.isEmpty() && url.password.isEmpty() && url.query == null && url.fragment == null)
  require(loginToken.isNotEmpty() && loginToken.none { it.isWhitespace() })
  val client = transport.newBuilder().followRedirects(false).followSslRedirects(false)
    .cache(null).cookieJar(CookieJar.NO_COOKIES)
    .callTimeout(15, TimeUnit.SECONDS).connectTimeout(15, TimeUnit.SECONDS)
    .readTimeout(15, TimeUnit.SECONDS).build()
  val request = Request.Builder().url(url).header("Authorization", "Bearer $loginToken")
    .header("Accept", "application/json").post("{}".toRequestBody("application/json".toMediaType())).build()
  return suspendCancellableCoroutine { continuation ->
    val call = client.newCall(request)
    continuation.invokeOnCancellation { call.cancel() }
    call.enqueue(object : Callback {
      override fun onFailure(call: Call, error: IOException) {
        if (continuation.isActive) continuation.resumeWithException(error)
      }
      override fun onResponse(call: Call, response: Response) {
        try {
          val session = response.use {
            check(it.code == 200) { "Session request failed" }
            val body = requireNotNull(it.body)
            check(body.contentLength() <= 16_384) { "Session response too large" }
            val bytes = ByteArrayOutputStream()
            body.byteStream().use { stream ->
              val chunk = ByteArray(4096)
              while (true) {
                val count = stream.read(chunk, 0, minOf(chunk.size, 16_385 - bytes.size()))
                if (count < 0) break
                bytes.write(chunk, 0, count)
                check(bytes.size() <= 16_384) { "Session response too large" }
              }
            }
            val json = JsonParser.parseString(bytes.toString(Charsets.UTF_8.name())).asJsonObject
            listOf("base_url", "application_id", "external_customer_id", "token", "expires_at").associateWith { key ->
              val value = requireNotNull(json[key])
              require(value.isJsonPrimitive && value.asJsonPrimitive.isString && value.asString.isNotEmpty())
              value.asString
            }
          }
          if (continuation.isActive) continuation.resume(session)
        } catch (error: Exception) {
          if (continuation.isActive) continuation.resumeWithException(error)
        }
      }
    })
  }
}
