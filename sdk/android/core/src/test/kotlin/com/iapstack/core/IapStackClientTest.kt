package com.iapstack.core

import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import java.net.URI
import java.time.Instant
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.time.Duration
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.seconds

class IapStackClientTest {
  @Test
  fun verifyPurchaseSendsContractAndDecodesProjections() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(jsonResponse(VERIFICATION_JSON))
      val client = client(server)

      val result = client.verifyPurchase(purchase(), requestId = "request-client-1")
      val recorded = server.takeRequest()

      assertEquals("POST", recorded.method)
      assertEquals(
        "/proxy/v1/applications/application-1/purchases:verify",
        recorded.path,
      )
      assertEquals("Bearer customer-token", recorded.getHeader("Authorization"))
      assertEquals("application/json", recorded.getHeader("Content-Type"))
      assertEquals("request-client-1", recorded.getHeader("X-Request-ID"))
      assertTrue(recorded.getHeader("X-IAPStack-SDK")!!.startsWith("android-kotlin/"))
      assertEquals("customer-internal", result.customerId)
      assertEquals(Instant.parse("2026-08-24T20:00:00Z"), result.verifiedAt)
      assertTrue(result.entitlements.single().grantsAccess)
      client.close()
    }
  }

  @Test
  fun restorePurchasesPreservesResultOrder() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(
        jsonResponse("""{"results":[$VERIFICATION_JSON,$VERIFICATION_JSON]}"""),
      )
      val client = client(server)

      val result = client.restorePurchases(listOf(purchase(), purchase("pro_monthly")))

      assertEquals(2, result.results.size)
      assertEquals("customer-internal", result.results[0].customerId)
      client.close()
    }
  }

  @Test
  fun failsClosedWhenAllowedEntitlementReachesEffectiveEnd() {
    val endsAt = Instant.parse("2026-08-26T12:00:00Z")
    val entitlement = Entitlement(
      key = "premium",
      access = "allowed",
      reason = "canceled_at_period_end",
      version = 7,
      effectiveStartsAt = endsAt.minusSeconds(30 * 24 * 60 * 60),
      effectiveEndsAt = endsAt,
    )

    assertTrue(entitlement.grantsAccessAt(endsAt.minusNanos(1_000)))
    assertFalse(entitlement.grantsAccessAt(endsAt))
    assertFalse(entitlement.grantsAccessAt(endsAt.plusSeconds(3600)))
  }

  @Test
  fun escapesCustomerIdentifiersAsOnePathSegment() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(
        jsonResponse("""{"customer_id":"customer-internal","entitlements":[]}"""),
      )
      val client = client(server)

      client.getEntitlements("customer/with space")
      val recorded = server.takeRequest()

      assertTrue(recorded.path!!.contains("customer%2Fwith%20space"))
      client.close()
    }
  }

  @Test
  fun retriesTransientResponses() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(
        jsonResponse(
          """{"error":{"code":"provider_unavailable","message":"provider is unavailable","request_id":"request-server-1"}}""",
          503,
        ),
      )
      server.enqueue(jsonResponse(VERIFICATION_JSON))
      val client = client(
        server,
        IapStackRetryPolicy(maxAttempts = 2, baseDelay = Duration.ZERO, maxDelay = Duration.ZERO),
      )

      val result = client.verifyPurchase(purchase())

      assertEquals(2, server.requestCount)
      assertEquals("customer-internal", result.customerId)
      client.close()
    }
  }

  @Test
  fun exposesStableApiErrorsWithoutResponsePayloads() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(
        jsonResponse(
          """{"error":{"code":"invalid_purchase","message":"purchase evidence could not be verified","request_id":"request-server-2"},"secret":"must-not-escape"}""",
          422,
        ),
      )
      val client = client(server)

      val error = assertFailsWith<IapStackApiException> {
        client.verifyPurchase(purchase())
      }
      assertEquals(422, error.statusCode)
      assertEquals("invalid_purchase", error.code)
      assertEquals("request-server-2", error.requestId)
      assertFalse(error.retryable)
      assertFalse(error.toString().contains("must-not-escape"))
      client.close()
    }
  }

  @Test
  fun rejectsOversizedAndInvalidJsonResponses() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(MockResponse().setBody("""{"value":"too-large"}""").setResponseCode(200))
      val oversized = client(server, maxResponseBytes = 8)
      assertFailsWith<IapStackProtocolException> {
        oversized.verifyPurchase(purchase())
      }
      oversized.close()
    }
    MockWebServer().use { server ->
      server.enqueue(MockResponse().setBody("<html>proxy error</html>").setResponseCode(200))
      val invalid = client(server)
      assertFailsWith<IapStackProtocolException> {
        invalid.verifyPurchase(purchase())
      }
      invalid.close()
    }
  }

  @Test
  fun wrapsContractMismatchesAsProtocolErrors() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(
        jsonResponse("""{"verified_at":"not-a-timestamp","customer_id":"customer-internal","entitlements":[]}"""),
      )
      val client = client(server)
      assertFailsWith<IapStackProtocolException> {
        client.verifyPurchase(purchase())
      }
      client.close()
    }
  }

  @Test
  fun turnsABoundedAttemptDeadlineIntoATimeoutException() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(
        MockResponse()
          .setBodyDelay(2, TimeUnit.SECONDS)
          .setBody(VERIFICATION_JSON)
          .setResponseCode(200),
      )
      val client = client(
        server,
        timeout = 50.milliseconds,
        retryPolicy = IapStackRetryPolicy(maxAttempts = 1),
      )
      assertFailsWith<IapStackTimeoutException> {
        client.verifyPurchase(purchase())
      }
      client.close()
    }
  }

  @Test
  fun abortsTheUnderlyingHttpRequestWhenAnAttemptTimesOut() = runBlocking {
    MockWebServer().use { server ->
      server.enqueue(
        MockResponse()
          .setHeadersDelay(5, TimeUnit.SECONDS)
          .setBody(VERIFICATION_JSON)
          .setResponseCode(200),
      )
      val client = client(
        server,
        timeout = 50.milliseconds,
        retryPolicy = IapStackRetryPolicy(maxAttempts = 1),
      )
      val started = System.nanoTime()
      assertFailsWith<IapStackTimeoutException> {
        client.verifyPurchase(purchase())
      }
      val elapsedMs = (System.nanoTime() - started) / 1_000_000
      assertTrue(elapsedMs < 2_000, "attempt should abort well before the delayed response")
      assertTrue(server.takeRequest(500, TimeUnit.MILLISECONDS) != null)
      client.close()
    }
  }

  @Test
  fun requiresHttpsUnlessInsecureHttpIsExplicit() {
    assertFailsWith<IllegalArgumentException> {
      IapStackConfig(
        baseUri = URI.create("http://iap.example"),
        applicationId = "application-1",
        customerToken = "customer-token",
      )
    }
  }

  @Test
  fun acceptsOrdinaryHttpsOriginsWithoutUserInfo() {
    IapStackConfig(
      baseUri = URI.create("https://iap.example/proxy"),
      applicationId = "application-1",
      customerToken = "customer-token",
    )
  }

  @Test
  fun redactsInvalidBearerValuesFromValidationErrors() {
    val error = assertFailsWith<IllegalArgumentException> {
      IapStackConfig(
        baseUri = URI.create("https://iap.example"),
        applicationId = "application-1",
        customerToken = "secret with spaces",
      )
    }
    assertFalse(error.message!!.contains("secret with spaces"))
  }

  @Test
  fun retryPolicyAppliesFullJitterWithinTheExponentialCap() {
    val policy = IapStackRetryPolicy(
      baseDelay = 100.milliseconds,
      maxDelay = 250.milliseconds,
    )
    assertEquals(Duration.ZERO, policy.delayAfter(1, 0.0))
    assertEquals(50.milliseconds, policy.delayAfter(1, 0.5))
    assertEquals(250.milliseconds, policy.delayAfter(4, 1.0))
    assertFailsWith<IllegalArgumentException> {
      policy.delayAfter(1, 1.1)
    }
  }

  private fun client(
    server: MockWebServer,
    retryPolicy: IapStackRetryPolicy = IapStackRetryPolicy(maxAttempts = 1),
    timeout: Duration = 1.seconds,
    maxResponseBytes: Int = 1024 * 1024,
  ): IapStackClient {
    val base = server.url("/proxy").toString().trimEnd('/')
    return IapStackClient(
      IapStackConfig(
        baseUri = URI.create(base),
        applicationId = "application-1",
        customerToken = "customer-token",
        timeout = timeout,
        retryPolicy = retryPolicy,
        maxResponseBytes = maxResponseBytes,
        allowInsecureHttp = true,
      ),
      httpClient = OkHttpClient(),
    )
  }

  private fun purchase(productId: String = "premium_lifetime") = PurchaseSubmission(
    externalCustomerId = "customer-external",
    claimedProducts = listOf(productId),
    evidence = mapOf(
      "purchase_data" to """{"productId":"$productId"}""",
      "signature" to "signed",
      "product_kind" to "non_consumable",
    ),
  )

  private fun jsonResponse(body: String, code: Int = 200) =
    MockResponse().setBody(body).setResponseCode(code).setHeader("Content-Type", "application/json")

  companion object {
    private const val VERIFICATION_JSON =
      """{"verified_at":"2026-08-24T20:00:00Z","customer_id":"customer-internal","entitlements":[{"key":"premium","access":"allowed","reason":"purchase_valid","effective_starts_at":"2026-08-24T19:00:00Z","version":1}]}"""
  }
}
