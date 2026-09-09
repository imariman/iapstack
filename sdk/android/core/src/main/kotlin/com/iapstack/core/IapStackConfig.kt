package com.iapstack.core

import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import java.net.URI
import kotlin.time.Duration
import kotlin.time.Duration.Companion.seconds

/**
 * Runtime configuration for one authenticated mobile customer session.
 */
data class IapStackConfig(
  val baseUri: URI,
  val applicationId: String,
  val customerToken: String,
  val timeout: Duration = 10.seconds,
  val retryPolicy: IapStackRetryPolicy = IapStackRetryPolicy(),
  val maxResponseBytes: Int = 1024 * 1024,
  val allowInsecureHttp: Boolean = false,
) {
  /**
   * Parsed destination URL used by the transport layer.
   */
  val baseUrl: HttpUrl = baseUri.toString().toHttpUrl()

  init {
    validate()
  }

  /**
   * Throws when the configuration is unsafe or incomplete.
   */
  fun validate() {
    val host = baseUri.host ?: baseUri.authority
    if (host.isNullOrBlank() ||
      baseUri.query != null ||
      baseUri.fragment != null ||
      baseUri.userInfo.isNotEmpty()
    ) {
      throw IllegalArgumentException(
        "baseUri must be an origin or API path prefix without query, fragment, or user info",
      )
    }
    val scheme = baseUri.scheme.lowercase()
    if (scheme != "https" && !(allowInsecureHttp && scheme == "http")) {
      throw IllegalArgumentException("baseUri must use HTTPS")
    }
    if (applicationId.isBlank() || applicationId.contains('/')) {
      throw IllegalArgumentException("applicationId must be one non-empty path segment")
    }
    if (customerToken.isBlank() || customerToken.any { it.isWhitespace() }) {
      throw IllegalArgumentException("customerToken must be a non-empty bearer token")
    }
    if (timeout <= Duration.ZERO) {
      throw IllegalArgumentException("timeout must be positive")
    }
    if (maxResponseBytes <= 0) {
      throw IllegalArgumentException("maxResponseBytes must be positive")
    }
    retryPolicy.validate()
  }
}
