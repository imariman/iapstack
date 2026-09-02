import 'package:flutter/material.dart';
import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:iapstack_google_play_example/billing_status_card.dart';
import 'package:iapstack_google_play_example/example_config.dart';
import 'package:iapstack_google_play_example/example_cubit.dart';
import 'package:iapstack_google_play_example/example_status_panel.dart';
import 'package:iapstack_google_play_example/product_offer_card.dart';

/// Manual controls for the Google Play internal-testing release gate.
class ExamplePage extends StatelessWidget {
  /// Creates the internal-testing controls page.
  const ExamplePage({required this.config, super.key});

  /// Safe display configuration; the customer session is never rendered.
  final ExampleConfig config;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('IAPStack Google Play Test')),
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
              const SizedBox(height: 24),
              BillingStatusCard(state: state),
              const SizedBox(height: 24),
              Text('Products', style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 12),
              if (state.products.isEmpty)
                const Text('No Google Play products are currently available.')
              else
                for (final product in state.products) ...<Widget>[
                  ProductOfferCard(
                    key: ValueKey<String>(product.selectionKey),
                    product: product,
                    enabled:
                        state.billingAvailable &&
                        state.catalogReady &&
                        !loading,
                    onPurchase: () =>
                        context.read<ExampleCubit>().purchase(product),
                  ),
                  const SizedBox(height: 12),
                ],
              const SizedBox(height: 12),
              OutlinedButton.icon(
                onPressed: state.billingAvailable && !loading
                    ? context.read<ExampleCubit>().restore
                    : null,
                icon: const Icon(Icons.restore_outlined),
                label: const Text('Restore owned purchases'),
              ),
              const SizedBox(height: 12),
              OutlinedButton.icon(
                onPressed: !loading
                    ? context.read<ExampleCubit>().refresh
                    : null,
                icon: const Icon(Icons.refresh_outlined),
                label: const Text('Refresh entitlements'),
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
