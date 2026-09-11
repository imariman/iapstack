import Foundation

/// Stable companion errors for StoreKit integration.
public struct AppleIAPStackError: Error, LocalizedError {
  /// Creates one redacted companion error.
  public init(code: String, message: String, userCancelled: Bool = false, cause: (any Error)? = nil) {
    self.code = code
    self.message = message
    self.userCancelled = userCancelled
    self.cause = cause
  }

  /// Stable machine-readable error code.
  public let code: String

  /// Safe user-facing summary.
  public let message: String

  /// Whether StoreKit UI was explicitly cancelled.
  public let userCancelled: Bool

  /// Optional underlying error.
  public let cause: (any Error)?

  /// Returns a redacted error summary.
  public var errorDescription: String? {
    "Apple StoreKit companion error (\(code)): \(message)"
  }
}
