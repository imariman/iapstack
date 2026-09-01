import 'package:flutter/services.dart';

/// Real-device StoreKit controls used only by the Apple sandbox harness.
final class SandboxTools {
  /// Creates the method-channel bridge to the iOS test host.
  const SandboxTools();

  static const MethodChannel _channel = MethodChannel(
    'com.imariman.iapstack/apple-sandbox-tools',
  );

  /// Opens Apple's subscription management sheet so auto-renew can be disabled.
  Future<void> showManageSubscriptions() async {
    await _channel.invokeMethod<void>('showManageSubscriptions');
  }

  /// Opens Apple's refund request sheet for the latest transaction of a product.
  Future<String> beginRefundRequest(String productId) async {
    final status = await _channel.invokeMethod<String>(
      'beginRefundRequest',
      <String, Object?>{'productId': productId},
    );
    return status ?? 'unknown';
  }
}
