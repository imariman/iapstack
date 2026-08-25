import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_huawei/iapstack_huawei.dart';
import 'package:iapstack_huawei_example/example_cubit.dart';

void main() {
  test('requires active sandbox eligibility and records request identity',
      () async {
    String? observedRequestId;
    final backend = IapStackClient(
      IapStackConfig(
        baseUri: Uri.parse('https://iap.example'),
        applicationId: 'application-1',
        customerToken: 'customer-token',
      ),
      httpClient: MockClient((request) async {
        observedRequestId = request.headers['X-Request-ID'];
        return http.Response(jsonEncode(_verificationJson), 200);
      }),
    );
    final cubit = ExampleCubit(
      backend: backend,
      huawei: HuaweiIapStack(
        client: backend,
        platform: _FakePlatform(),
      ),
      externalCustomerId: 'customer-1',
      productId: 'premium-monthly',
      productKind: HuaweiProductKind.subscription,
      requestIdFactory: (operation) => 'sandbox-$operation-test',
    );
    addTearDown(cubit.close);

    await cubit.checkSandbox();
    expect(cubit.state.sandboxStatus?.isActive, isTrue);
    expect(cubit.state.status, ExampleStatus.success);

    await cubit.purchase();
    expect(observedRequestId, 'sandbox-purchase-test');
    expect(cubit.state.requestId, 'sandbox-purchase-test');
    expect(cubit.state.entitlements.single.grantsAccess, isTrue);
  });

  test('blocks purchase when the account or APK is not sandbox eligible',
      () async {
    var backendCalled = false;
    final backend = IapStackClient(
      IapStackConfig(
        baseUri: Uri.parse('https://iap.example'),
        applicationId: 'application-1',
        customerToken: 'customer-token',
      ),
      httpClient: MockClient((request) async {
        backendCalled = true;
        return http.Response(jsonEncode(_verificationJson), 200);
      }),
    );
    final platform = _FakePlatform(
      sandboxResult: const HuaweiSandboxStatus(
        isSandboxUser: true,
        isSandboxApk: false,
      ),
    );
    final cubit = ExampleCubit(
      backend: backend,
      huawei: HuaweiIapStack(client: backend, platform: platform),
      externalCustomerId: 'customer-1',
      productId: 'premium-monthly',
      productKind: HuaweiProductKind.subscription,
    );
    addTearDown(cubit.close);

    await cubit.checkSandbox();
    await cubit.purchase();

    expect(cubit.state.status, ExampleStatus.failure);
    expect(cubit.state.message, contains('sandbox eligibility'));
    expect(platform.purchaseCalls, 0);
    expect(backendCalled, isFalse);
  });
}

final Map<String, Object?> _verificationJson = <String, Object?>{
  'verified_at': '2026-08-25T20:00:00Z',
  'customer_id': 'customer-internal',
  'entitlements': <Object?>[
    <String, Object?>{
      'key': 'premium',
      'access': 'allowed',
      'reason': 'purchase_valid',
      'version': 1,
    },
  ],
};

final class _FakePlatform implements HuaweiIapPlatform {
  _FakePlatform({
    this.sandboxResult = const HuaweiSandboxStatus(
      isSandboxUser: true,
      isSandboxApk: true,
      marketVersion: '42',
      apkVersion: '43',
    ),
  });

  final HuaweiSandboxStatus sandboxResult;
  int purchaseCalls = 0;

  @override
  Future<HuaweiOwnedPurchasesPage> ownedPurchases({
    required HuaweiProductKind productKind,
    String? continuationToken,
  }) async =>
      HuaweiOwnedPurchasesPage(purchases: const <HuaweiSignedPurchase>[]);

  @override
  Future<HuaweiSignedPurchase> purchase({
    required String productId,
    required HuaweiProductKind productKind,
    required String developerPayload,
  }) async {
    purchaseCalls++;
    return HuaweiSignedPurchase(
      purchaseData: jsonEncode(<String, Object?>{
        'productId': productId,
        'developerPayload': developerPayload,
        'purchaseToken': 'purchase-token',
      }),
      signature: 'signature',
    );
  }

  @override
  Future<HuaweiSandboxStatus> sandboxStatus() async => sandboxResult;
}
