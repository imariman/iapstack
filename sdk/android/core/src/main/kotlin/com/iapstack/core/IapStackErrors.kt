package com.iapstack.core

sealed class IapStackException(message: String, cause: Throwable? = null) : Exception(message, cause)

/**
 * Structured v1 API error returned by IAPStack.
 */
class IapStackApiException(
  val statusCode: Int,
  val code: String,
  val requestId: String? = null,
  val retryable: Boolean,
  message: String,
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
