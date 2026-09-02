import 'package:flutter/material.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';

/// Displays one localized Google Play product offer.
class ProductOfferCard extends StatelessWidget {
  /// Creates one product offer card.
  const ProductOfferCard({
    required this.product,
    required this.enabled,
    required this.onPurchase,
    super.key,
  });

  /// Localized provider product details.
  final GooglePlayProduct product;

  /// Whether purchase UI can currently be opened.
  final bool enabled;

  /// Invoked when the user chooses this product or subscription offer.
  final VoidCallback onPurchase;

  @override
  Widget build(BuildContext context) => Card(
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: <Widget>[
          Text(product.title, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 4),
          Text(product.description),
          const SizedBox(height: 12),
          Text(
            '${product.price} · ${product.kind.evidenceValue}',
            style: Theme.of(context).textTheme.labelLarge,
          ),
          if (product.basePlanId != null) ...<Widget>[
            const SizedBox(height: 8),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: <Widget>[
                Chip(label: Text('Base plan: ${product.basePlanId}')),
                if (product.offerId != null)
                  Chip(label: Text('Offer: ${product.offerId}')),
                for (final tag in product.offerTags)
                  Chip(label: Text('Tag: $tag')),
              ],
            ),
          ],
          if (product.pricingPhases.isNotEmpty) ...<Widget>[
            const SizedBox(height: 8),
            for (final phase in product.pricingPhases)
              Padding(
                padding: const EdgeInsets.only(bottom: 4),
                child: Text(
                  '${phase.formattedPrice} / ${phase.billingPeriod} · ${phase.recurrence.name}',
                ),
              ),
          ],
          const SizedBox(height: 12),
          FilledButton(
            onPressed: enabled ? onPurchase : null,
            child: const Text('Purchase with Google Play'),
          ),
        ],
      ),
    ),
  );
}
