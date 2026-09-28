package com.iapstack.core

import kotlin.time.Duration

sealed class IapStackException(message: String, cause: Throwable? = null) : Exception(message, cause)

/**
 * Structured v1 API error returned by IAPStack.
 *
 * [retryAfter] is the cooldown the server requested through `Retry-After`:
 * [Duration.ZERO] when its HTTP-date had already elapsed, null when the header
 * was absent or malformed. The client waits `max(jitter, min(retryAfter,
 * IapStackRetryPolicy.MAX_RETRY_AFTER))` before a retry.
 */
class IapStackApiException(
  val statusCode: Int,
  val code: String,
  val requestId: String? = null,
  val retryable: Boolean,
  message: String,
  val retryAfter: Duration? = null,
) : IapStackException(message)

/**
 * Network-level transport error before a full API response exists.
 */
class IapStackTransportException(
  message: String,
  cause: Throwable? = null,
) : IapStackException(message, cause)

/**
 * One bounded HTTP attempt exceeded its deadline or was aborted.
 */
class IapStackTimeoutException(
  message: String,
  cause: Throwable? = null,
) : IapStackException(message, cause)

/**
 * Response did not match the contract shape.
 */
class IapStackProtocolException(
  message: String,
  cause: Throwable? = null,
) : IapStackException(message, cause)
