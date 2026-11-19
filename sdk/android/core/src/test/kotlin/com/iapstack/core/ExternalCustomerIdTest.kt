package com.iapstack.core

import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockWebServer
import java.net.URI
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

@OptIn(InternalIapStackApi::class)
class ExternalCustomerIdTest {
  @Test
  fun followsTheSharedServerIdentifierVectors() {
    val vectors = customerIdVectors()
    assertTrue(vectors.any { it.accepted } && vectors.any { !it.accepted })
    val mismatches = vectors.filter { vector ->
      val accepted = runCatching { requireExternalCustomerId(vector.value) }.isSuccess
      accepted != vector.accepted
    }
    assertTrue(mismatches.isEmpty(), "Mismatched vectors: ${mismatches.joinToString { it.name }}")
  }

  @Test
  fun rejectsIdsTheServerRejectsBeforeSending() = runBlocking {
    MockWebServer().use { server ->
      val client = IapStackClient(
        IapStackConfig(baseUri = URI(server.url("/").toString()), applicationId = "app", customerToken = "token", allowInsecureHttp = true),
      )
      for (vector in customerIdVectors().filterNot { it.accepted }) {
        val error = assertFailsWith<IllegalArgumentException>(vector.name) { client.getEntitlements(vector.value) }
        assertEquals(REJECTION_MESSAGE, error.message, vector.name)
        assertFailsWith<IllegalArgumentException>(vector.name) {
          client.verifyPurchase(PurchaseSubmission(vector.value, listOf("premium"), mapOf("purchase_token" to "token")))
        }
      }
      assertEquals(0, server.requestCount)
      client.close()
    }
  }

  private companion object {
    const val REJECTION_MESSAGE =
      "externalCustomerId must be non-empty valid Unicode without surrounding whitespace or control characters"
  }
}
