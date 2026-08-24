import 'package:iapstack/src/retry_policy.dart';

/// Runtime-only configuration for one application-scoped IAPStack client.
final class IapStackConfig {
  /// Creates an immutable client configuration.
  const IapStackConfig({
    required this.baseUri,
    required this.applicationId,
    required this.applicationKey,
    this.timeout = const Duration(seconds: 10),
    this.retryPolicy = const IapStackRetryPolicy(),
    this.maxResponseBytes = 1024 * 1024,
    this.allowInsecureHttp = false,
  });

  /// IAPStack origin, optionally including a reverse-proxy path prefix.
  final Uri baseUri;

  /// Application scope encoded in every public API path.
  final String applicationId;

  /// Application bearer retained only in memory by this SDK.
  final String applicationKey;

  /// Maximum duration of one HTTP attempt, including response streaming.
  final Duration timeout;

  /// Bounded retry policy for idempotent IAPStack operations.
  final IapStackRetryPolicy retryPolicy;

  /// Maximum accepted JSON response size.
  final int maxResponseBytes;

  /// Allows plain HTTP for explicit local development environments.
  final bool allowInsecureHttp;

  /// Throws [ArgumentError] when the configuration is unsafe or incomplete.
  void validate() {
    if (baseUri.host.trim().isEmpty ||
        baseUri.hasQuery ||
        baseUri.hasFragment ||
        baseUri.userInfo.isNotEmpty) {
      throw ArgumentError.value(
          baseUri, 'baseUri', 'must be an origin or path prefix');
    }
    if (baseUri.scheme != 'https' &&
        !(allowInsecureHttp && baseUri.scheme == 'http')) {
      throw ArgumentError.value(baseUri, 'baseUri', 'must use HTTPS');
    }
    if (applicationId.trim().isEmpty || applicationId.contains('/')) {
      throw ArgumentError.value(
          applicationId, 'applicationId', 'must be one non-empty path segment');
    }
    if (applicationKey.trim().isEmpty ||
        applicationKey.contains(RegExp(r'\s'))) {
      throw ArgumentError.value(
          '<redacted>', 'applicationKey', 'must be a non-empty bearer token');
    }
    if (timeout <= Duration.zero) {
      throw ArgumentError.value(timeout, 'timeout', 'must be positive');
    }
    if (maxResponseBytes <= 0) {
      throw ArgumentError.value(
          maxResponseBytes, 'maxResponseBytes', 'must be positive');
    }
    retryPolicy.validate();
  }
}
