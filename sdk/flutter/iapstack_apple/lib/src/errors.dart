/// Safe StoreKit, catalog, or evidence failure.
final class AppleIapStackException implements Exception {
  /// Creates a redacted Apple companion failure.
  const AppleIapStackException({
    required this.code,
    required this.message,
    this.userCancelled = false,
    this.cause,
  });

  /// Stable companion or StoreKit result code.
  final String code;

  /// Safe human-readable summary that never includes signed transaction data.
  final String message;

  /// Whether StoreKit purchase UI was explicitly cancelled by the customer.
  final bool userCancelled;

  /// Optional underlying error retained for diagnostics without being rendered.
  final Object? cause;

  @override
  String toString() => 'AppleIapStackException($code): $message';
}
