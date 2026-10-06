package com.iapstack.reactnative

import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.tls.HandshakeCertificates
import okhttp3.tls.HeldCertificate
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test

class CustomerSessionRequestTest {
  private lateinit var server: MockWebServer
  private lateinit var transport: OkHttpClient
  private val session = """{"base_url":"https://iap.test","application_id":"app","external_customer_id":"customer","token":"customer-token","expires_at":"2999-01-01T00:00:00Z"}"""

  @Before fun setUp() {
    val certificate = HeldCertificate.Builder().commonName("localhost").addSubjectAlternativeName("localhost").build()
    server = MockWebServer()
    server.useHttps(HandshakeCertificates.Builder().heldCertificate(certificate).build().sslSocketFactory(), false)
    server.start()
    val trust = HandshakeCertificates.Builder().addTrustedCertificate(certificate.certificate).build()
    // Deliberately redirects-enabled: the bootstrap must override it for every request.
    transport = OkHttpClient.Builder().sslSocketFactory(trust.sslSocketFactory(), trust.trustManager).build()
  }
  @After fun tearDown() { server.shutdown() }

  @Test fun successfulSessionUsesBearerAndPost() = runBlocking {
    server.enqueue(MockResponse().setBody(session))
    val result = requestCustomerSession(server.url("/session").toString(), "tester-token", transport)
    assertEquals("customer-token", result["token"])
    val request = server.takeRequest()
    assertEquals("POST", request.method)
    assertEquals("Bearer tester-token", request.getHeader("Authorization"))
    assertEquals("{}", request.body.readUtf8())
  }

  @Test fun rejectsRedirectWithoutForwardingLoginBearer() = runBlocking {
    server.enqueue(MockResponse().setResponseCode(307).setHeader("Location", server.url("/redirected")))
    server.enqueue(MockResponse().setBody(session))
    expectFailure { requestCustomerSession(server.url("/session").toString(), "tester-token", transport) }
    assertEquals(1, server.requestCount)
  }

  @Test fun capsChunkedResponseBeforeDecoding() = runBlocking {
    server.enqueue(MockResponse().setChunkedBody(" ".repeat(16_385), 2048))
    expectFailure { requestCustomerSession(server.url("/session").toString(), "tester-token", transport) }
  }

  @Test fun rejectsMalformedResponseAndUnsafeRequest() = runBlocking {
    server.enqueue(MockResponse().setBody("""{"token":123}"""))
    expectFailure { requestCustomerSession(server.url("/session").toString(), "tester-token", transport) }
    expectFailure { requestCustomerSession(server.url("/session").newBuilder().scheme("http").build().toString(), "tester-token", transport) }
    assertEquals(1, server.requestCount)
  }

  private suspend fun expectFailure(action: suspend () -> Any?) {
    try { action(); fail("Expected request rejection") } catch (expected: Exception) { /* expected */ }
  }
}
