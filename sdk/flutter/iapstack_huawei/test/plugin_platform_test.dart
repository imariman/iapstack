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

  test('reports Huawei IAP environment readiness', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'isEnvReady');
      return jsonEncode(<String, Object?>{'returnCode': '0'});
    });

    expect(await const HuaweiPluginPlatform().isAvailable(), isTrue);
  });

  test('maps localized Huawei products without losing price precision',
      () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'obtainProductInfo');
      expect((call.arguments as Map<Object?, Object?>)['priceType'], 2);
      expect((call.arguments as Map<Object?, Object?>)['skuIds'],
          <String>['premium_monthly']);
      return jsonEncode(<String, Object?>{
        'returnCode': '0',
        'productInfoList': <Object?>[
          <String, Object?>{
            'productId': 'premium_monthly',
            'priceType': 2,
            'productName': 'Premium Monthly',
            'productDesc': 'Monthly access',
            'price': r'$4.99',
            'microsPrice': 4990000,
            'currency': 'USD',
            'status': 0,
            'subPeriod': 'P1M',
            'subFreeTrialPeriod': 'P7D',
            'subSpecialPrice': r'$1.99',
            'subSpecialPriceMicros': 1990000,
          },
        ],
      });
    });

    final products = await const HuaweiPluginPlatform().queryProducts(
      productIds: const <String>['premium_monthly'],
      productKind: HuaweiProductKind.subscription,
    );

    final product = products.single;
    expect(product.id, 'premium_monthly');
    expect(product.priceMicros, 4990000);
    expect(product.currency, 'USD');
    expect(product.subscriptionPeriod, 'P1M');
    expect(product.freeTrialPeriod, 'P7D');
    expect(product.promotionalPriceMicros, 1990000);
    expect(product.isPurchasable, isTrue);
  });

  test('rejects incomplete product data', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      return jsonEncode(<String, Object?>{
        'returnCode': '0',
        'productInfoList': <Object?>[
          <String, Object?>{
            'productId': 'premium_monthly',
            'priceType': 2,
            'price': r'$4.99',
            'microsPrice': 4990000,
            'status': 0,
          },
        ],
      });
    });

    await expectLater(
      const HuaweiPluginPlatform().queryProducts(
        productIds: const <String>['premium_monthly'],
        productKind: HuaweiProductKind.subscription,
      ),
      throwsA(
        isA<HuaweiIapStackException>()
            .having((error) => error.code, 'code', 'invalid_plugin_response'),
      ),
    );
  });

  test('reports both sandbox account and APK eligibility', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'isSandboxActivated');
      return jsonEncode(<String, Object?>{
        'returnCode': '0',
        'isSandboxUser': true,
        'isSandboxApk': true,
        'versionFrMarket': '42',
        'versionInApk': '43',
      });
    });

    final status = await const HuaweiPluginPlatform().sandboxStatus();

    expect(status.isActive, isTrue);
    expect(status.isSandboxUser, isTrue);
    expect(status.isSandboxApk, isTrue);
    expect(status.marketVersion, '42');
    expect(status.apkVersion, '43');
  });

  test('keeps an ineligible sandbox result observable', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      return jsonEncode(<String, Object?>{
        'returnCode': '0',
        'isSandboxUser': true,
        'isSandboxApk': false,
      });
    });

    final status = await const HuaweiPluginPlatform().sandboxStatus();

    expect(status.isActive, isFalse);
    expect(status.isSandboxUser, isTrue);
    expect(status.isSandboxApk, isFalse);
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
