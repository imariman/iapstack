import 'package:flutter/material.dart';

/// Explains missing runtime values without rendering their contents.
class ConfigurationPage extends StatelessWidget {
  /// Creates the configuration help page.
  const ConfigurationPage({required this.missingValues, super.key});

  /// Safe environment variable names that must be supplied.
  final List<String> missingValues;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('IAPStack Google Play Test')),
    body: SafeArea(
      child: ListView(
        padding: const EdgeInsets.all(24),
        children: <Widget>[
          Text(
            'Runtime configuration required',
            style: Theme.of(context).textTheme.headlineSmall,
          ),
          const SizedBox(height: 16),
          const Text(
            'Pass these values with --dart-define. The application bearer and purchase tokens are never displayed or persisted.',
          ),
          const SizedBox(height: 16),
          for (final value in missingValues)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: SelectableText(value),
            ),
        ],
      ),
    ),
  );
}
