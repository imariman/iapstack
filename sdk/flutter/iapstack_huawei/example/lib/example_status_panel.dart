import 'package:flutter/material.dart';
import 'package:iapstack_huawei_example/entitlement_card.dart';
import 'package:iapstack_huawei_example/example_cubit.dart';

/// Renders loading, failure, empty, and entitlement data states.
class ExampleStatusPanel extends StatelessWidget {
  /// Creates a status panel.
  const ExampleStatusPanel({required this.state, super.key});

  /// Current asynchronous example state.
  final ExampleState state;

  @override
  Widget build(BuildContext context) {
    if (state.status == ExampleStatus.loading) {
      return const Center(child: CircularProgressIndicator());
    }
    final errorColor = Theme.of(context).colorScheme.error;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: <Widget>[
        Text(
          state.message,
          style: state.status == ExampleStatus.failure
              ? TextStyle(color: errorColor)
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
            itemBuilder: (context, index) {
              final entitlement = state.entitlements[index];
              return EntitlementCard(
                key: ValueKey<String>(entitlement.key),
                entitlement: entitlement,
              );
            },
          ),
      ],
    );
  }
}
