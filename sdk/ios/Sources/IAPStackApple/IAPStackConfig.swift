import Foundation

/// Runtime configuration for one authenticated customer session.
public struct IAPStackConfig: Sendable {
  /// Creates an immutable SDK configuration.
  public init(
    baseUri: URL,
    applicationId: String,
    customerToken: String,
    timeout: TimeInterval = 10,
    retryPolicy: IAPStackRetryPolicy = .init(),
    maxResponseBytes: Int = 1_048_576,
    allowInsecureHttp: Bool = false,
  ) {
    self.baseUri = baseUri
    self.applicationId = applicationId
    self.customerToken = customerToken
    self.timeout = timeout
    self.retryPolicy = retryPolicy
    self.maxResponseBytes = maxResponseBytes
    self.allowInsecureHttp = allowInsecureHttp
  }

  /// IAPStack origin, optionally including a path prefix.
  public let baseUri: URL

  /// Application scope encoded in every public API path.
  public let applicationId: String

  /// Short-lived customer token retained only in-memory.
  public let customerToken: String

  /// Max duration for one network attempt.
  public let timeout: TimeInterval

  /// Bounded retry policy for idempotent operations.
  public let retryPolicy: IAPStackRetryPolicy

  /// Maximum accepted response size.
  public let maxResponseBytes: Int

  /// Allows plain HTTP for local development only.
  public let allowInsecureHttp: Bool

  /// Throws an `IAPStackSDKError.configurationError` on unsafe configuration.
  public func validate() throws {
    guard let host = baseUri.host, !host.isEmpty else {
      throw IAPStackSDKError.configurationError(message: "baseUri must include a valid host")
    }
    if baseUri.query != nil || baseUri.fragment != nil || baseUri.user != nil || baseUri.password != nil {
      throw IAPStackSDKError.configurationError(
        message: "baseUri must be an origin or path prefix without query, fragment, or credentials",
      )
    }
    let scheme = baseUri.scheme?.lowercased() ?? ""
    if scheme != "https" && !(allowInsecureHttp && scheme == "http") {
      throw IAPStackSDKError.configurationError(message: "baseUri must use HTTPS")
    }
    if applicationId.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ||
      applicationId.contains("/") {
      throw IAPStackSDKError.configurationError(
        message: "applicationId must be one non-empty path segment",
      )
    }
    if customerToken.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ||
      customerToken.contains(where: { $0.isWhitespace }) {
      throw IAPStackSDKError.configurationError(
        message: "customerToken must be a non-empty bearer token",
      )
    }
    if timeout <= 0 {
      throw IAPStackSDKError.configurationError(message: "timeout must be positive")
    }
    if maxResponseBytes <= 0 {
      throw IAPStackSDKError.configurationError(
        message: "maxResponseBytes must be positive",
      )
    }
    try retryPolicy.validate()
  }
}
