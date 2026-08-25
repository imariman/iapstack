import 'package:flutter/material.dart';
import 'package:iapstack_apple_example/example_cubit.dart';

/// Current safe StoreKit and IAPStack operation status.
class StatusPanel extends StatelessWidget {
  /// Creates one status panel.
  const StatusPanel({required this.state, super.key});

  /// Current Cubit state.
  final ExampleState state;

  @override
  Widget build(BuildContext context) => Card(
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Text('Status', style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          Text(state.message),
          if (state.requestId != null) ...<Widget>[
            const SizedBox(height: 8),
            SelectableText('Request ID: ${state.requestId}'),
          ],
          if (state.entitlements.isNotEmpty) ...<Widget>[
            const SizedBox(height: 12),
            for (final entitlement in state.entitlements)
              Text('${entitlement.key}: ${entitlement.access}'),
          ],
        ],
      ),
    ),
  );
}
