import Foundation

/// Retry settings for idempotent IAPStack operations.
public struct IAPStackRetryPolicy: Sendable {
  /// Creates a bounded full-jitter exponential backoff policy.
  public init(
    maxAttempts: Int = 3,
    baseDelay: TimeInterval = 0.25,
    maxDelay: TimeInterval = 2.0,
  ) {
    self.maxAttempts = maxAttempts
    self.baseDelay = baseDelay
    self.maxDelay = maxDelay
  }

  /// Total attempts, including the initial request.
  public let maxAttempts: Int

  /// Upper delay bound before the second attempt.
  public let baseDelay: TimeInterval

  /// Upper delay bound.
  public let maxDelay: TimeInterval

  /// Returns a full-jitter delay after a failed one-based [attempt].
  public func delayAfter(attempt: Int, randomValue: Double) -> TimeInterval {
    precondition(attempt > 0, "attempt must be positive")
    precondition(randomValue >= 0.0 && randomValue <= 1.0, "randomValue must be in [0, 1]")
    guard baseDelay > 0 else { return 0 }
    let exponent = min(max(attempt - 1, 0), 20)
    let multiplier = 1 << exponent
    let capped = min(baseDelay * Double(multiplier), maxDelay)
    return capped * randomValue
  }

  /// Throws `IAPStackSDKError.configurationError` when bounds are unsafe.
  public func validate() throws {
    if maxAttempts < 1 || maxAttempts > 5 {
      throw IAPStackSDKError.configurationError(
        message: "maxAttempts must be between 1 and 5",
      )
    }
    if baseDelay < 0 || maxDelay < 0 || baseDelay > maxDelay {
      throw IAPStackSDKError.configurationError(
        message: "retry delays must be non-negative and ordered",
      )
    }
  }
}
