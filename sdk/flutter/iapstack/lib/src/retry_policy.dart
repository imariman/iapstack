/// Retry settings for idempotent verification, restore, and entitlement calls.
final class IapStackRetryPolicy {
  /// Upper bound for a server-requested `Retry-After` cooldown, so one
  /// response cannot stall a caller indefinitely. Every IAPStack SDK uses
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
  /// [retryAfter]: the result is then the larger of the jitter delay and the
  /// cooldown, capped at [maxRetryAfter], so a retry never lands inside a
  /// cooldown the server asked for.
  Duration delayAfter(
    int attempt, {
    required double randomValue,
    Duration? retryAfter,
  }) {
    final jitter = _jitterDelay(attempt, randomValue);
    if (retryAfter == null || retryAfter <= Duration.zero) {
      return jitter;
    }
    final raised = retryAfter > jitter ? retryAfter : jitter;
    return raised > maxRetryAfter ? maxRetryAfter : raised;
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
