import 'package:flutter/material.dart';
import 'package:iapstack_huawei/iapstack_huawei.dart';

/// Displays the AppGallery product selected by the sandbox harness.
class HuaweiProductCard extends StatelessWidget {
  /// Creates the product summary card.
  const HuaweiProductCard({required this.product, super.key});

  /// Product loaded from AppGallery, or null while unavailable.
  final HuaweiProduct? product;

  @override
  Widget build(BuildContext context) {
    final value = product;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: value == null
            ? const Text('AppGallery product has not been loaded.')
            : Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: <Widget>[
                  Text(value.title,
                      style: Theme.of(context).textTheme.titleMedium),
                  const SizedBox(height: 8),
                  Text(value.description),
                  const SizedBox(height: 12),
                  Wrap(
                    spacing: 12,
                    runSpacing: 8,
                    children: <Widget>[
                      Text(value.price,
                          style: Theme.of(context).textTheme.titleLarge),
                      if (value.subscriptionPeriod != null)
                        Chip(label: Text(value.subscriptionPeriod!)),
                      Chip(
                        label: Text(value.isPurchasable
                            ? 'Purchasable'
                            : 'Unavailable (${value.status})'),
                      ),
                    ],
                  ),
                  if (value.freeTrialPeriod != null) ...<Widget>[
                    const SizedBox(height: 8),
                    Text('Free trial: ${value.freeTrialPeriod}'),
                  ],
                ],
              ),
      ),
    );
  }
}
