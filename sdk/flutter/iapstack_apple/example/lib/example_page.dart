import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:iapstack_apple_example/example_config.dart';
import 'package:iapstack_apple_example/example_cubit.dart';
import 'package:iapstack_apple_example/product_card.dart';
import 'package:iapstack_apple_example/status_panel.dart';

/// Manual controls for the StoreKit 2 sandbox release gate.
class ExamplePage extends StatelessWidget {
  /// Creates the StoreKit testing controls page.
  const ExamplePage({required this.config, super.key});

  /// Safe display configuration; the customer session is never rendered.
  final ExampleConfig config;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('IAPStack Apple Test')),
    body: SafeArea(
      child: BlocBuilder<ExampleCubit, ExampleState>(
        builder: (context, state) {
          final loading = state.status == ExampleStatus.loading;
          return ListView(
            padding: const EdgeInsets.all(24),
            children: <Widget>[
              Text('Application: ${config.applicationId}'),
              const SizedBox(height: 8),
              Text('Customer UUID: ${config.externalCustomerId}'),
              const SizedBox(height: 24),
              Text('Products', style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 12),
              if (state.products.isEmpty)
                const Text('No StoreKit products are currently available.')
              else
                for (final product in state.products) ...<Widget>[
                  ProductCard(
                    key: ValueKey<String>(product.id),
                    product: product,
                    enabled: state.storeAvailable && !loading,
                    onPurchase: () =>
                        context.read<ExampleCubit>().purchase(product),
                  ),
                  const SizedBox(height: 12),
                ],
              OutlinedButton.icon(
                onPressed: state.storeAvailable && !loading
                    ? context.read<ExampleCubit>().restore
                    : null,
                icon: const Icon(Icons.restore_outlined),
                label: const Text('Restore App Store purchases'),
              ),
              const SizedBox(height: 12),
              OutlinedButton.icon(
                onPressed: state.storeAvailable && !loading
                    ? context.read<ExampleCubit>().refresh
                    : null,
                icon: const Icon(Icons.refresh_outlined),
                label: const Text('Refresh entitlements'),
              ),
              const SizedBox(height: 24),
              StatusPanel(state: state),
            ],
          );
        },
      ),
    ),
  );
}
