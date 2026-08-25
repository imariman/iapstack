import 'package:flutter/material.dart';
import 'package:iapstack_apple/iapstack_apple.dart';

/// One localized StoreKit product and its purchase action.
class ProductCard extends StatelessWidget {
  /// Creates a product card.
  const ProductCard({
    required this.product,
    required this.enabled,
    required this.onPurchase,
    super.key,
  });

  /// Display-safe StoreKit product.
  final AppleProduct product;

  /// Whether a purchase action can start.
  final bool enabled;

  /// Purchase callback owned by the Cubit.
  final VoidCallback onPurchase;

  @override
  Widget build(BuildContext context) => Card(
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Text(product.title, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 6),
          Text(product.description),
          const SizedBox(height: 10),
          Text('${product.price} · ${product.kind.name}'),
          const SizedBox(height: 12),
          FilledButton(
            onPressed: enabled ? onPurchase : null,
            child: const Text('Purchase with StoreKit'),
          ),
        ],
      ),
    ),
  );
}
