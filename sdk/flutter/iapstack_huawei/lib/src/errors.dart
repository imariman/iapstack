/// Safe Huawei device SDK or evidence failure.
final class HuaweiIapStackException implements Exception {
  /// Creates a redacted Huawei bridge failure.
  const HuaweiIapStackException({
    required this.code,
    required this.message,
    this.userCancelled = false,
    this.cause,
  });

  /// Stable bridge or Huawei result code.
  final String code;

  /// Safe human-readable summary.
  final String message;

  /// Whether the purchase UI was explicitly cancelled by the user.
  final bool userCancelled;

  /// Optional underlying non-secret error.
  final Object? cause;

  @override
  String toString() => 'HuaweiIapStackException($code): $message';
}
