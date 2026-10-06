package com.iapstack.core

import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import java.net.URI
import java.time.Instant
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse

class IapStackSessionLoaderTest {
  private lateinit var server: MockWebServer
  private lateinit var loader: IapStackSessionLoader

  @BeforeTest fun setUp() {
    val certificate = HeldCertificate.Builder().commonName("localhost").addSubjectAlternativeName("localhost").build()
    server = MockWebServer()
    server.useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
    server.start()
    val trust = HandshakeCertificates.Builder().addTrustedCertificate(certificate.certificate).build()
    // Deliberately redirect-enabled: the loader must override it for every request.
    loader = IapStackSessionLoader(OkHttpClient.Builder().sslSocketFactory(trust.sslSocketFactory(), trust.trustManager).build())
  }

  @AfterTest fun tearDown() {
    server.shutdown()
  }

  @Test fun requestUsesLoginBearerAndDecodesSession() = runBlocking {
    server.enqueue(MockResponse().setBody(session(expiry = "2099-01-01T00:00:00.123Z")))
    val result = loader.load(endpoint(), "temporary-login")
    assertEquals(URI("https://iap.example"), result.baseUri)
    assertEquals("app", result.applicationId)
    assertEquals("customer-1", result.externalCustomerId)
    assertEquals("short-lived", result.config.customerToken)
    assertEquals(Instant.parse("2099-01-01T00:00:00.123Z"), result.expiresAt)
    assertFalse("short-lived" in result.toString() || "customer-1" in result.toString())
    val request = server.takeRequest()
    assertEquals("POST", request.method)
    assertEquals("Bearer temporary-login", request.getHeader("Authorization"))
    assertEquals("{}", request.body.readUtf8())
  }

  @Test fun unsafeRequestsFailBeforeSendingTheLoginBearer() = runBlocking {
    val base = server.url("/session")
    val unsafe = listOf(
      base.newBuilder().scheme("http").build().toString(),
      base.newBuilder().username("name").password("pass").build().toString(),
      base.newBuilder().query("token=bad").build().toString(),
      base.newBuilder().fragment("fragment").build().toString(),
      "not a url",
    )
    for (endpoint in unsafe) assertCode("invalid_session_request") { loader.load(endpoint, "login") }
    for (token in listOf("", "two words", "line\nbreak")) assertCode("invalid_session_request") { loader.load(endpoint(), token) }
    assertEquals(0, server.requestCount)
  }

  @Test fun unusableSessionsAreRejected() = runBlocking {
    val cases = listOf(
      session(baseUrl = "http://insecure.example") to "invalid_session_response",
      session(customer = " customer-1") to "invalid_session_response",
      session(token = "") to "invalid_session_response",
      session(expiry = "tomorrow") to "invalid_session_response",
      """{"token":123}""" to "invalid_session_response",
      "[]" to "invalid_session_response",
      session(expiry = "2000-01-01T00:00:00Z") to "session_expired",
    )
    for ((body, code) in cases) {
      server.enqueue(MockResponse().setBody(body))
      assertCode(code) { loader.load(endpoint(), "login") }
    }
  }

  @Test fun oversizedAndUnsuccessfulResponsesAreRejected() = runBlocking {
    server.enqueue(MockResponse().setChunkedBody(" ".repeat(IapStackSessionLoader.MAX_RESPONSE_BYTES + 1), 2048))
    assertCode("invalid_session_response") { loader.load(endpoint(), "login") }
    server.enqueue(MockResponse().setResponseCode(401).setBody("{}"))
    assertCode("session_unavailable") { loader.load(endpoint(), "login") }
  }

  @Test fun redirectIsNotFollowedWithTheLoginBearer() = runBlocking {
    server.enqueue(MockResponse().setResponseCode(307).setHeader("Location", server.url("/redirected")))
    server.enqueue(MockResponse().setBody(session()))
    assertCode("session_unavailable") { loader.load(endpoint(), "login") }
    assertEquals(1, server.requestCount)
  }

  private fun endpoint() = server.url("/session").toString()

  private suspend fun assertCode(code: String, action: suspend () -> IapStackCustomerSession) {
    // Never print the raw response: it would contain a bearer in real use.
    val error = assertFailsWith<IapStackSessionException> { action() }
    assertEquals(code, error.code)
  }

  private fun session(
    baseUrl: String = "https://iap.example",
    customer: String = "customer-1",
    token: String = "short-lived",
    expiry: String = "2099-01-01T00:00:00Z",
  ) = """{"base_url":"$baseUrl","application_id":"app","external_customer_id":"$customer","token":"$token","expires_at":"$expiry"}"""
}
