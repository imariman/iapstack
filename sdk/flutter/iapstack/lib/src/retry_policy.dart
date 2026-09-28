/// Retry settings for idempotent verification, restore, and entitlement calls.
final class IapStackRetryPolicy {
  /// Client budget for a server-requested `Retry-After` cooldown. A longer
  /// cooldown is cut to this value, so the retry may still land inside it; the
  /// budget never reduces the configured jitter. Every IAPStack SDK uses
  /// 30 seconds.
  static const Duration maxRetryAfter = Duration(seconds: 30);

  /// Creates a bounded full-jitter exponential backoff policy.
  const IapStackRetryPolicy({
    this.maxAttempts = 3,
    this.baseDelay = const Duration(milliseconds: 250),
    this.maxDelay = const Duration(seconds: 2),
  });

  /// Total attempts, including the initial request.
  final int maxAttempts;

  /// Upper delay bound before the second attempt.
  final Duration baseDelay;

  /// Upper delay bound.
  final Duration maxDelay;

  /// Returns a full-jitter delay after a failed one-based [attempt].
  ///
  /// When the failed response carried a `Retry-After` cooldown, pass it as
  /// [retryAfter]: the result is then `max(jitter, min(retryAfter,
  /// maxRetryAfter))`. The cooldown raises the wait up to the client budget
  /// and never shortens the jitter delay.
  Duration delayAfter(
    int attempt, {
    required double randomValue,
    Duration? retryAfter,
  }) {
    final jitter = _jitterDelay(attempt, randomValue);
    if (retryAfter == null || retryAfter <= Duration.zero) {
      return jitter;
    }
    final budgeted = retryAfter > maxRetryAfter ? maxRetryAfter : retryAfter;
    return budgeted > jitter ? budgeted : jitter;
  }

  Duration _jitterDelay(int attempt, double randomValue) {
    if (randomValue < 0 || randomValue > 1) {
      throw ArgumentError.value(
          randomValue, 'randomValue', 'must be between 0 and 1');
    }
    if (baseDelay == Duration.zero) {
      return Duration.zero;
    }
    final exponent = attempt - 1;
    final multiplier = 1 << exponent.clamp(0, 20);
    final microseconds = baseDelay.inMicroseconds * multiplier;
    final capped = microseconds.clamp(0, maxDelay.inMicroseconds);
    return Duration(microseconds: (capped * randomValue).round());
  }

  /// Throws [ArgumentError] when bounds are invalid.
  void validate() {
    if (maxAttempts < 1 || maxAttempts > 5) {
      throw ArgumentError.value(
          maxAttempts, 'maxAttempts', 'must be between 1 and 5');
    }
    if (baseDelay.isNegative || maxDelay.isNegative || baseDelay > maxDelay) {
      throw ArgumentError('retry delays must be non-negative and ordered');
    }
  }
}
