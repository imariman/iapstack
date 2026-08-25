import 'package:flutter/material.dart';

/// Theme definitions for the StoreKit 2 testing harness.
abstract final class AppTheme {
  /// Material 3 light theme used by the example.
  static ThemeData get light => ThemeData(
    colorScheme: ColorScheme.fromSeed(seedColor: const Color(0xFF007AFF)),
    useMaterial3: true,
  );
}
