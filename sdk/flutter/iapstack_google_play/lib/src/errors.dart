/// Safe Google Play device bridge, catalog, or evidence failure.
final class GooglePlayIapStackException implements Exception {
  /// Creates a redacted Google Play companion failure.
  const GooglePlayIapStackException({
    required this.code,
    required this.message,
    this.userCancelled = false,
    this.cause,
  });

  /// Stable companion, plugin, or Play Billing result code.
  final String code;

  /// Safe human-readable summary that never includes a purchase token.
  final String message;

  /// Whether Google Play purchase UI was explicitly cancelled by the user.
  final bool userCancelled;

  /// Optional underlying error retained for diagnostics without being rendered.
  final Object? cause;

  @override
  String toString() => 'GooglePlayIapStackException($code): $message';
}
