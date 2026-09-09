package com.iapstack.core

sealed class IapStackException(message: String, cause: Throwable? = null) : Exception(message, cause)

/**
 * Structured v1 API error returned by IAPStack.
 */
data class IapStackApiException(
  val statusCode: Int,
  val code: String,
  val requestId: String? = null,
  val retryable: Boolean,
  val message: String,
) : IapStackException(message)

/**
 * Network-level transport error before a full API response exists.
 */
data class IapStackTransportException(
  val messageText: String,
  val causeError: Throwable? = null,
) : IapStackException(messageText, causeError)

/**
 * Abortable or oversized request-response mismatch.
 */
data class IapStackTimeoutException(
  val messageText: String,
  val causeError: Throwable? = null,
) : IapStackException(messageText, causeError)

/**
 * Response did not match the contract shape.
 */
data class IapStackProtocolException(
  val messageText: String,
  val causeError: Throwable? = null,
) : IapStackException(messageText, causeError)
