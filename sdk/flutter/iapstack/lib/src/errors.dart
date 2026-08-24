/// Base class for safe, structured IAPStack SDK failures.
sealed class IapStackException implements Exception {
  /// Creates an SDK failure without retaining request payloads or credentials.
  const IapStackException(this.message, {this.cause});

  /// Safe human-readable summary.
  final String message;

  /// Optional underlying non-secret error.
  final Object? cause;

  @override
  String toString() => '$runtimeType: $message';
}

/// Stable non-success response returned by the IAPStack API.
final class IapStackApiException extends IapStackException {
  /// Creates a structured API exception from the v1 error envelope.
  const IapStackApiException({
    required this.statusCode,
    required this.code,
    required String message,
    required this.retryable,
    this.requestId,
  }) : super(message);

  /// HTTP response status.
  final int statusCode;

  /// Stable v1 machine-readable error code.
  final String code;

  /// Request ID that operators can correlate with server logs.
  final String? requestId;

  /// Whether a later idempotent retry may succeed.
  final bool retryable;
}

/// Network failure before a complete HTTP response was available.
final class IapStackTransportException extends IapStackException {
  /// Creates a redacted transport failure.
  const IapStackTransportException(super.message, {super.cause});
}

/// One bounded HTTP attempt exceeded its configured deadline.
final class IapStackTimeoutException extends IapStackException {
  /// Creates a timeout failure.
  const IapStackTimeoutException(super.message, {super.cause});
}

/// The server response did not match the versioned JSON contract.
final class IapStackProtocolException extends IapStackException {
  /// Creates a redacted protocol failure.
  const IapStackProtocolException(super.message, {super.cause});
}
