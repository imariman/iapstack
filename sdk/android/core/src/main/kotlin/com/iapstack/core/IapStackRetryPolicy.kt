package com.iapstack.core

import kotlin.time.Duration
import kotlin.time.Duration.Companion.microseconds
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.seconds

/**
 * Full-jitter exponential backoff policy for idempotent read-write operations.
 */
data class IapStackRetryPolicy(
  val maxAttempts: Int = 3,
  val baseDelay: Duration = 250.milliseconds,
  val maxDelay: Duration = 2.seconds,
) {
  fun validate() {
    if (maxAttempts < 1 || maxAttempts > 5) {
      throw IllegalArgumentException("maxAttempts must be between 1 and 5")
    }
    if (baseDelay.isNegative() || maxDelay.isNegative() || baseDelay > maxDelay) {
      throw IllegalArgumentException("retry delays must be non-negative and ordered")
    }
  }

  /**
   * Returns a bounded full-jitter delay after a one-based failed attempt.
   *
   * When the failed response carried a `Retry-After` cooldown, pass it as
   * [retryAfter]: the result is then the larger of the jitter delay and the
   * cooldown, capped at [MAX_RETRY_AFTER], so a retry never lands inside a
   * cooldown the server asked for.
   */
  fun delayAfter(
    attempt: Int,
    randomValue: Double,
    retryAfter: Duration? = null,
  ): Duration {
    val jitter = jitterDelay(attempt, randomValue)
    if (retryAfter == null || retryAfter <= Duration.ZERO) {
      return jitter
    }
    return maxOf(jitter, retryAfter).coerceAtMost(MAX_RETRY_AFTER)
  }

  private fun jitterDelay(
    attempt: Int,
    randomValue: Double,
  ): Duration {
    if (randomValue !in 0.0..1.0) {
      throw IllegalArgumentException("randomValue must be between 0 and 1")
    }
    if (baseDelay == Duration.ZERO) {
      return Duration.ZERO
    }
    val exponent = (attempt - 1).coerceAtLeast(0).coerceAtMost(20)
    val multiplier = 1L shl exponent
    val capMicros =
      (baseDelay.inWholeMicroseconds * multiplier).coerceAtMost(maxDelay.inWholeMicroseconds)
    return (capMicros * randomValue).toLong().microseconds
  }

  companion object {
    /**
     * Upper bound for a server-requested `Retry-After` cooldown, so one
     * response cannot stall a caller indefinitely. Every IAPStack SDK uses
     * 30 seconds.
     */
    val MAX_RETRY_AFTER: Duration = 30.seconds
  }
}
