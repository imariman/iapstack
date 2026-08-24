import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_huawei/iapstack_huawei.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('IapClient');

  tearDown(() async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, null);
  });

  test('preserves exact purchase data from the official plugin raw response',
      () async {
    const purchaseData =
        '{"productId":"premium","developerPayload":"customer-1","escaped":"a\\/b"}';
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'createPurchaseIntent');
      expect((call.arguments as Map<Object?, Object?>)['developerPayload'],
          'customer-1');
      return jsonEncode(<String, Object?>{
        'returnCode': '0',
        'inAppPurchaseData': purchaseData,
        'inAppDataSignature': 'exact-signature',
      });
    });

    final purchase = await const HuaweiPluginPlatform().purchase(
      productId: 'premium',
      productKind: HuaweiProductKind.nonConsumable,
      developerPayload: 'customer-1',
    );

    expect(purchase.purchaseData, purchaseData);
    expect(purchase.signature, 'exact-signature');
  });

  test('preserves aligned signed restore pages and continuation tokens',
      () async {
    const purchaseData =
        '{"productId":"premium","developerPayload":"customer-1"}';
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'obtainOwnedPurchases');
      return jsonEncode(<String, Object?>{
        'returnCode': '0',
        'inAppPurchaseDataList': <String>[purchaseData],
        'inAppSignature': <String>['exact-signature'],
        'continuationToken': 'next-page',
      });
    });

    final page = await const HuaweiPluginPlatform().ownedPurchases(
      productKind: HuaweiProductKind.subscription,
    );

    expect(page.purchases.single.purchaseData, purchaseData);
    expect(page.purchases.single.signature, 'exact-signature');
    expect(page.continuationToken, 'next-page');
  });

  test('classifies Huawei user cancellation without leaking payloads',
      () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      return jsonEncode(<String, Object?>{
        'returnCode': '60000',
        'errMsg': 'The user cancels the payment.',
      });
    });

    await expectLater(
      const HuaweiPluginPlatform().purchase(
        productId: 'premium',
        productKind: HuaweiProductKind.nonConsumable,
        developerPayload: 'customer-1',
      ),
      throwsA(
        isA<HuaweiIapStackException>()
            .having((error) => error.code, 'code', '60000')
            .having((error) => error.userCancelled, 'userCancelled', isTrue),
      ),
    );
  });

  test('rejects misaligned restore signatures', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      return jsonEncode(<String, Object?>{
        'returnCode': '0',
        'inAppPurchaseDataList': <String>['{"productId":"premium"}'],
        'inAppSignature': <String>[],
      });
    });

    await expectLater(
      const HuaweiPluginPlatform()
          .ownedPurchases(productKind: HuaweiProductKind.nonConsumable),
      throwsA(
        isA<HuaweiIapStackException>()
            .having((error) => error.code, 'code', 'invalid_plugin_response'),
      ),
    );
  });
}
