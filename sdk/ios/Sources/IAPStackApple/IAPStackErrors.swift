import Foundation

/// Structured IAPStack errors returned or produced by this SDK.
public enum IAPStackSDKError: Error, LocalizedError, Sendable {
  /// Structured API error returned by IAPStack with a machine-readable status.
  ///
  /// `retryAfter` is the cooldown in seconds the server requested through
  /// `Retry-After`: 0 when its HTTP-date had already elapsed, nil when the
  /// header was absent or malformed. The client waits `max(jitter,
  /// min(retryAfter, IAPStackRetryPolicy.maxRetryAfter))` before a retry.
  case apiError(
    statusCode: Int,
    code: String,
    message: String,
    requestId: String?,
    retryable: Bool,
    retryAfter: TimeInterval? = nil,
  )

  /// Network-level transport error before an HTTP payload is decoded.
  case transportError(message: String, cause: (any Error)? = nil)

  /// Per-attempt request timeout.
  case timeoutError(message: String, cause: (any Error)? = nil)

  /// Contract, JSON, or response-shape mismatch.
  case protocolError(message: String, cause: (any Error)? = nil)

  /// Invalid user-provided configuration.
  case configurationError(message: String)

  public var errorDescription: String? {
    switch self {
    case let .apiError(statusCode: statusCode, code: code, message: message, requestId: requestId, retryable: _, retryAfter: _):
      return "IAPStack API error (status: \(statusCode), code: \(code), requestId: \(requestId ?? "nil"), message: \(message))"
    case let .transportError(message: message, cause: cause):
      return "IAPStack transport error: \(message)\(cause.flatMap({ ": \($0.localizedDescription)" }) ?? "")"
    case let .timeoutError(message: message, cause: cause):
      return "IAPStack request timeout: \(message)\(cause.flatMap({ ": \($0.localizedDescription)" }) ?? "")"
    case let .protocolError(message: message, cause: cause):
      return "IAPStack protocol error: \(message)\(cause.flatMap({ ": \($0.localizedDescription)" }) ?? "")"
    case let .configurationError(message):
      return "IAPStack configuration error: \(message)"
    }
  }

  public var isRetryable: Bool {
    switch self {
    case let .apiError(_, _, _, _, retryable, _):
      return retryable
    case .protocolError, .configurationError:
      return false
    case let .transportError(_, cause):
      // A task its URLSession cancelled fails the same way on every attempt.
      return !isCancellation(cause)
    case .timeoutError:
      return true
    }
  }
}

/// Reports cancellation from Swift concurrency or from a cancelled `URLSessionTask`.
func isCancellation(_ error: (any Error)?) -> Bool {
  error is CancellationError || (error as? URLError)?.code == .cancelled
}
