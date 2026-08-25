import 'package:flutter/material.dart';
import 'package:iapstack_huawei/iapstack_huawei.dart';

/// Summarizes account and APK sandbox eligibility without exposing credentials.
class SandboxStatusCard extends StatelessWidget {
  /// Creates one responsive sandbox status card.
  const SandboxStatusCard({required this.status, super.key});

  /// Latest Huawei sandbox activation result, or null before the first check.
  final HuaweiSandboxStatus? status;

  @override
  Widget build(BuildContext context) {
    final value = status;
    final colorScheme = Theme.of(context).colorScheme;
    final active = value?.isActive == true;
    final color = value == null
        ? colorScheme.secondary
        : active
            ? colorScheme.primary
            : colorScheme.error;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            Row(
              children: <Widget>[
                Icon(
                  active ? Icons.verified_outlined : Icons.shield_outlined,
                  color: color,
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Text(
                    value == null
                        ? 'Sandbox eligibility not checked'
                        : active
                            ? 'Huawei sandbox active'
                            : 'Huawei sandbox blocked',
                    style: Theme.of(context)
                        .textTheme
                        .titleMedium
                        ?.copyWith(color: color),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            _statusLine('Test account', value?.isSandboxUser),
            const SizedBox(height: 8),
            _statusLine('Sandbox APK', value?.isSandboxApk),
            if (value?.apkVersion != null) ...<Widget>[
              const SizedBox(height: 8),
              Text('Installed APK version: ${value?.apkVersion}'),
            ],
            if (value?.marketVersion != null) ...<Widget>[
              const SizedBox(height: 4),
              Text('AppGallery version: ${value?.marketVersion}'),
            ],
          ],
        ),
      ),
    );
  }

  /// Builds one compact eligibility row using theme-owned status colors.
  Widget _statusLine(String label, bool? eligible) {
    final text = switch (eligible) {
      true => 'Eligible',
      false => 'Not eligible',
      null => 'Unknown',
    };
    return Row(
      children: <Widget>[
        Expanded(child: Text(label)),
        Text(text),
      ],
    );
  }
}
