import 'package:iapstack/src/models.dart';

/// Base class for safe, structured IAPStack SDK failures.
sealed class IapStackException implements Exception {
  /// Creates an SDK failure without retaining request payloads or credentials.
  const IapStackException(
    this.message, {
    this.cause,
    this.committedRestoreResults = const <VerificationResult>[],
    this.restoreSkips = const RestoreSkipCounts(),
    this.restorePageLimitReached = false,
  });

  /// Safe human-readable summary.
  final String message;

  /// Optional underlying non-secret error.
  final Object? cause;

  /// Verification rows already accepted when a later restore batch failed.
  ///
  /// Empty when this failure happened before any batch succeeded, and on
  /// errors that did not come from restore.
  final List<VerificationResult> committedRestoreResults;

  /// Store rows that were not submitted, when this failure came from restore.
  final RestoreSkipCounts restoreSkips;

  /// True when Huawei restore had already hit its page cap before this failure.
  final bool restorePageLimitReached;

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
    this.retryAfter,
    List<VerificationResult> committedRestoreResults =
        const <VerificationResult>[],
    RestoreSkipCounts restoreSkips = const RestoreSkipCounts(),
    bool restorePageLimitReached = false,
  }) : super(
          message,
          committedRestoreResults: committedRestoreResults,
          restoreSkips: restoreSkips,
          restorePageLimitReached: restorePageLimitReached,
        );

  /// HTTP response status.
  final int statusCode;

  /// Stable v1 machine-readable error code.
  final String code;

  /// Request ID that operators can correlate with server logs.
  final String? requestId;

  /// Whether a later idempotent retry may succeed.
  final bool retryable;

  /// Cooldown the server requested through `Retry-After`: [Duration.zero]
  /// when its HTTP-date had already elapsed, null when the header was absent
  /// or malformed. The client waits `max(jitter, min(retryAfter,
  /// IapStackRetryPolicy.maxRetryAfter))` before a retry.
  final Duration? retryAfter;
}

/// Network failure before a complete HTTP response was available.
final class IapStackTransportException extends IapStackException {
  /// Creates a redacted transport failure.
  const IapStackTransportException(
    super.message, {
    super.cause,
    super.committedRestoreResults,
    super.restoreSkips,
    super.restorePageLimitReached,
  });
}

/// One bounded HTTP attempt exceeded its configured deadline.
final class IapStackTimeoutException extends IapStackException {
  /// Creates a timeout failure.
  const IapStackTimeoutException(
    super.message, {
    super.cause,
    super.committedRestoreResults,
    super.restoreSkips,
    super.restorePageLimitReached,
  });
}

/// The server response did not match the versioned JSON contract.
final class IapStackProtocolException extends IapStackException {
  /// Creates a redacted protocol failure.
  const IapStackProtocolException(
    super.message, {
    super.cause,
    super.committedRestoreResults,
    super.restoreSkips,
    super.restorePageLimitReached,
  });
}

/// A restore batch failed after earlier progress, and the failure was not
/// already an [IapStackException]. [cause] is the batch error.
final class IapStackIncompleteRestoreException extends IapStackException {
  /// Creates a partial restore failure around [cause].
  const IapStackIncompleteRestoreException(
    super.message, {
    required super.cause,
    super.committedRestoreResults,
    super.restoreSkips,
    super.restorePageLimitReached,
  });
}
