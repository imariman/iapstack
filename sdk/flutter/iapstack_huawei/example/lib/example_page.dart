import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:iapstack_huawei_example/example_config.dart';
import 'package:iapstack_huawei_example/example_cubit.dart';
import 'package:iapstack_huawei_example/example_status_panel.dart';
import 'package:iapstack_huawei_example/sandbox_status_card.dart';

/// Manual controls used by the Huawei sandbox release gate.
class ExamplePage extends StatelessWidget {
  /// Creates the sandbox controls page.
  const ExamplePage({required this.config, super.key});

  /// Safe display configuration; the customer session is never rendered.
  final ExampleConfig config;

  @override
  Widget build(BuildContext context) => Scaffold(
        appBar: AppBar(title: const Text('IAPStack Huawei Sandbox')),
        body: SafeArea(
          child: BlocBuilder<ExampleCubit, ExampleState>(
            builder: (context, state) {
              final loading = state.status == ExampleStatus.loading;
              return ListView(
                padding: const EdgeInsets.all(24),
                children: <Widget>[
                  Text('Application: ${config.applicationId}'),
                  const SizedBox(height: 8),
                  Text('Customer: ${config.externalCustomerId}'),
                  const SizedBox(height: 8),
                  Text(
                      'Product: ${config.productId} (${config.productKind.evidenceValue})'),
                  const SizedBox(height: 24),
                  SandboxStatusCard(status: state.sandboxStatus),
                  const SizedBox(height: 12),
                  OutlinedButton.icon(
                    onPressed: loading
                        ? null
                        : context.read<ExampleCubit>().checkSandbox,
                    icon: const Icon(Icons.verified_user_outlined),
                    label: const Text('Recheck sandbox'),
                  ),
                  const SizedBox(height: 24),
                  FilledButton(
                    onPressed: loading || state.sandboxStatus?.isActive != true
                        ? null
                        : context.read<ExampleCubit>().purchase,
                    child: const Text('Purchase and verify'),
                  ),
                  const SizedBox(height: 12),
                  OutlinedButton(
                    onPressed: loading || state.sandboxStatus?.isActive != true
                        ? null
                        : context.read<ExampleCubit>().restore,
                    child: const Text('Restore purchases'),
                  ),
                  const SizedBox(height: 12),
                  OutlinedButton(
                    onPressed:
                        loading ? null : context.read<ExampleCubit>().refresh,
                    child: const Text('Refresh entitlements'),
                  ),
                  const SizedBox(height: 24),
                  ExampleStatusPanel(state: state),
                ],
              );
            },
          ),
        ),
      );
}
