import Foundation

/// Structured IAPStack errors returned or produced by this SDK.
public enum IAPStackSDKError: Error, LocalizedError, Sendable {
  /// Structured API error returned by IAPStack with a machine-readable status.
  case apiError(statusCode: Int, code: String, message: String, requestId: String?, retryable: Bool)

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
    case let .apiError(statusCode: statusCode, code: code, message: message, requestId: requestId, retryable: _):
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
    case let .apiError(_, _, _, _, retryable):
      return retryable
    case .protocolError, .configurationError:
      return false
    case .transportError, .timeoutError:
      return true
    }
  }
}
