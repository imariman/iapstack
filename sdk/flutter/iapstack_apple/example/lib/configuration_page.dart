import 'package:flutter/material.dart';

/// Safe setup guidance shown when runtime defines are incomplete.
class ConfigurationPage extends StatelessWidget {
  /// Creates the setup page from missing variable names.
  const ConfigurationPage({required this.missingValues, super.key});

  /// Safe environment variable names that require values.
  final List<String> missingValues;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('IAPStack Apple Test')),
    body: SafeArea(
      child: ListView(
        padding: const EdgeInsets.all(24),
        children: <Widget>[
          Text(
            'Configuration required',
            style: Theme.of(context).textTheme.headlineSmall,
          ),
          const SizedBox(height: 12),
          const Text(
            'Supply non-secret values with --dart-define and launch with the Apple sandbox helper. The customer session remains in process memory and is never displayed or persisted.',
          ),
          const SizedBox(height: 16),
          for (final value in missingValues) ...<Widget>[
            SelectableText(value),
            const SizedBox(height: 8),
          ],
        ],
      ),
    ),
  );
}
