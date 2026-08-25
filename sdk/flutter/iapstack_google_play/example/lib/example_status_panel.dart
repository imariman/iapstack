import 'package:flutter/material.dart';
import 'package:iapstack_google_play_example/entitlement_card.dart';
import 'package:iapstack_google_play_example/example_cubit.dart';

/// Renders operation progress, failures, and entitlement projections.
class ExampleStatusPanel extends StatelessWidget {
  /// Creates the status panel.
  const ExampleStatusPanel({required this.state, super.key});

  /// Current asynchronous example state.
  final ExampleState state;

  @override
  Widget build(BuildContext context) {
    final isFailure = state.status == ExampleStatus.failure;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: <Widget>[
        if (state.status == ExampleStatus.loading) ...<Widget>[
          const LinearProgressIndicator(),
          const SizedBox(height: 12),
        ],
        Text(
          state.message,
          style: isFailure
              ? TextStyle(color: Theme.of(context).colorScheme.error)
              : null,
        ),
        if (state.requestId != null) ...<Widget>[
          const SizedBox(height: 8),
          SelectableText('Request ID: ${state.requestId}'),
        ],
        const SizedBox(height: 16),
        if (state.entitlements.isEmpty)
          const Text('No entitlement projection returned yet.')
        else
          ListView.separated(
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            itemCount: state.entitlements.length,
            separatorBuilder: (context, index) => const SizedBox(height: 8),
            itemBuilder: (context, index) => EntitlementCard(
              key: ValueKey<String>(state.entitlements[index].key),
              entitlement: state.entitlements[index],
            ),
          ),
      ],
    );
  }
}
