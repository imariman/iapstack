import 'package:flutter/material.dart';
import 'package:iapstack/iapstack.dart';

/// Displays one authoritative entitlement projection.
class EntitlementCard extends StatelessWidget {
  /// Creates an entitlement card.
  const EntitlementCard({required this.entitlement, super.key});

  /// Projection to display.
  final Entitlement entitlement;

  @override
  Widget build(BuildContext context) => Card(
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: <Widget>[
          Text(entitlement.key, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 8),
          Text('${entitlement.access} · ${entitlement.reason}'),
          const SizedBox(height: 4),
          Text('Projection version ${entitlement.version}'),
        ],
      ),
    ),
  );
}
