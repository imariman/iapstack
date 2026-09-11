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
   */
  fun delayAfter(
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
}
