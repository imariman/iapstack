import 'package:flutter/material.dart';
import 'package:iapstack_google_play_example/example_cubit.dart';

/// Shows current Google Play Billing connection state.
class BillingStatusCard extends StatelessWidget {
  /// Creates the Billing status card.
  const BillingStatusCard({required this.state, super.key});

  /// Current example state.
  final ExampleState state;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final available = state.billingAvailable;
    final statusColor = available ? colors.primary : colors.error;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Row(
          children: <Widget>[
            Icon(
              available ? Icons.cloud_done_outlined : Icons.cloud_off_outlined,
              color: statusColor,
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: <Widget>[
                  Text(
                    available
                        ? 'Play Billing connected'
                        : 'Play Billing unavailable',
                    style: Theme.of(
                      context,
                    ).textTheme.titleMedium?.copyWith(color: statusColor),
                  ),
                  const SizedBox(height: 4),
                  Text('${state.products.length} product offers loaded'),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
