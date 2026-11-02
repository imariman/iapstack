package com.iapstack.core

import kotlin.time.Duration

sealed class IapStackException(message: String, cause: Throwable? = null) : Exception(message, cause) {
  /**
   * Verification rows already accepted when a later restore batch failed.
   * Empty when this failure happened before any batch succeeded.
   */
  var committedRestoreResults: List<VerificationResult> = emptyList()
    private set

  /**
   * Store rows that were not submitted, when this failure came from restore.
   */
  var restoreSkips: RestoreSkipCounts = RestoreSkipCounts()
    private set

  /**
   * True when Huawei restore had already hit its page cap before this failure.
   */
  var restorePageLimitReached: Boolean = false
    private set

  /**
   * Records restore progress on the failure that interrupted a later batch.
   * Store companions call this so callers can keep the original exception type.
   */
  fun attachPartialRestore(
    committedResults: List<VerificationResult>,
    skipped: RestoreSkipCounts,
    pageLimitReached: Boolean,
  ) {
    committedRestoreResults = committedResults
    restoreSkips = skipped
    restorePageLimitReached = pageLimitReached
  }
}

/**
 * A restore batch failed after an earlier batch committed, and the failure was
 * not already an [IapStackException]. [cause] is the batch error.
 */
class IapStackIncompleteRestoreException(
  committedResults: List<VerificationResult>,
  skipped: RestoreSkipCounts,
  pageLimitReached: Boolean,
  cause: Throwable,
) : IapStackException(
  cause.message ?: "IAPStack restore committed earlier batches and then failed",
  cause,
) {
  init {
    attachPartialRestore(committedResults, skipped, pageLimitReached)
  }
}

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
