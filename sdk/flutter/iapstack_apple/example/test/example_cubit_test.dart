import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_apple/iapstack_apple.dart';
import 'package:iapstack_apple_example/example_cubit.dart';
import 'package:iapstack_apple_example/sandbox_tools.dart';

const _customerId = '018f59d0-a200-7000-8000-000000000001';

void main() {
  test('loads StoreKit products and reports missing identifiers', () async {
    final product = _monthlyProduct;
    final platform = _FakePlatform(
      query: AppleProductQuery(
        products: <AppleProduct>[product],
        notFoundProductIds: const <String>{'missing'},
      ),
    );
    final cubit = _cubit(
      backend: _backend((request) async => http.Response('{}', 500)),
      platform: platform,
    );
    addTearDown(cubit.close);

    await cubit.initialize();

    expect(cubit.state.status, ExampleStatus.ready);
    expect(cubit.state.storeAvailable, isTrue);
    expect(cubit.state.products, <AppleProduct>[product]);
    expect(cubit.state.message, contains('missing'));
    expect(platform.queriedProductIds, const <String>{'premium_monthly'});
  });

  test('stops initialization when StoreKit is unavailable', () async {
    final cubit = _cubit(
      backend: _backend((request) async => http.Response('{}', 500)),
      platform: _FakePlatform(available: false),
    );
    addTearDown(cubit.close);

    await cubit.initialize();

    expect(cubit.state.status, ExampleStatus.failure);
    expect(cubit.state.storeAvailable, isFalse);
    expect(cubit.state.message, contains('unavailable'));
  });

  test('launches checkout with the configured customer binding', () async {
    final platform = _FakePlatform(
      query: AppleProductQuery(
        products: const <AppleProduct>[_monthlyProduct],
        notFoundProductIds: const <String>{},
      ),
    );
    final cubit = _cubit(
      backend: _backend((request) async => http.Response('{}', 500)),
      platform: platform,
    );
    addTearDown(cubit.close);
    await cubit.initialize();

    await cubit.purchase(_monthlyProduct);

    expect(platform.launchedProduct, _monthlyProduct);
    expect(platform.launchedToken, _customerId);
    expect(cubit.state.status, ExampleStatus.ready);
    expect(cubit.state.message, contains('Waiting for a StoreKit update'));
  });

  test('verifies purchase stream updates and publishes entitlements', () async {
    final updates = StreamController<ApplePurchase>.broadcast();
    final verified = Completer<void>();
    final platform = _FakePlatform(updates: updates.stream);
    final cubit = _cubit(
      backend: _backend((request) async {
        verified.complete();
        return http.Response(jsonEncode(_verificationJson), 200);
      }),
      platform: platform,
    );
    addTearDown(() async {
      await cubit.close();
      await updates.close();
    });

    updates.add(_purchase());
    await verified.future;
    await _flushAsync();

    expect(cubit.state.status, ExampleStatus.ready);
    expect(cubit.state.message, contains('transaction finished'));
    expect(cubit.state.entitlements.single.grantsAccess, isTrue);
    expect(cubit.state.requestId, 'apple-verify-test');
    expect(platform.completedPurchases, hasLength(1));
  });

  test(
    'handles terminal purchase updates without contacting backend',
    () async {
      final updates = StreamController<ApplePurchase>.broadcast();
      var backendCalled = false;
      final cubit = _cubit(
        backend: _backend((request) async {
          backendCalled = true;
          return http.Response('{}', 500);
        }),
        platform: _FakePlatform(updates: updates.stream),
      );
      addTearDown(() async {
        await cubit.close();
        await updates.close();
      });

      updates.add(_purchase(status: ApplePurchaseStatus.pending));
      await _flushAsync();
      expect(cubit.state.message, contains('pending'));

      updates.add(_purchase(status: ApplePurchaseStatus.cancelled));
      await _flushAsync();
      expect(cubit.state.message, 'Purchase cancelled.');

      updates.add(
        _purchase(
          status: ApplePurchaseStatus.failed,
          errorCode: 'payment_failed',
        ),
      );
      await _flushAsync();
      expect(cubit.state.status, ExampleStatus.failure);
      expect(cubit.state.message, contains('payment_failed'));

      updates.addError(StateError('sensitive native detail'));
      await _flushAsync();
      expect(cubit.state.message, isNot(contains('sensitive native detail')));
      expect(backendCalled, isFalse);
    },
  );

  test('restores transactions before refreshing entitlements', () async {
    final requests = <String>[];
    final platform = _FakePlatform(restored: <ApplePurchase>[_purchase()]);
    final cubit = _cubit(
      backend: _backend((request) async {
        requests.add(request.url.path);
        if (request.url.path.endsWith('purchases:restore')) {
          return http.Response(
            jsonEncode(<String, Object?>{
              'results': <Object?>[_verificationJson],
            }),
            200,
          );
        }
        return http.Response(jsonEncode(_snapshotJson), 200);
      }),
      platform: platform,
    );
    addTearDown(cubit.close);
    await cubit.initialize();

    await cubit.restore();

    expect(requests, hasLength(2));
    expect(requests.first, endsWith('purchases:restore'));
    expect(requests.last, endsWith('/entitlements'));
    expect(cubit.state.status, ExampleStatus.ready);
    expect(cubit.state.entitlements.single.grantsAccess, isTrue);
    expect(cubit.state.requestId, 'apple-restore-test');
  });

  test('surfaces redacted checkout and refresh failures', () async {
    final platform = _FakePlatform(
      query: AppleProductQuery(
        products: const <AppleProduct>[_monthlyProduct],
        notFoundProductIds: const <String>{},
      ),
      launchError: const AppleIapStackException(
        code: 'store_unavailable',
        message: 'redacted',
      ),
    );
    final cubit = _cubit(
      backend: _backend(
        (request) async => http.Response(
          jsonEncode(<String, Object?>{
            'error': <String, Object?>{
              'code': 'provider_unavailable',
              'message': 'safe message',
              'request_id': 'server-request-id',
            },
          }),
          503,
        ),
      ),
      platform: platform,
    );
    addTearDown(cubit.close);
    await cubit.initialize();

    await cubit.purchase(_monthlyProduct);
    expect(cubit.state.status, ExampleStatus.failure);
    expect(cubit.state.message, contains('store_unavailable'));

    await cubit.refresh();
    expect(cubit.state.status, ExampleStatus.failure);
    expect(cubit.state.message, contains('provider_unavailable'));
    expect(cubit.state.requestId, 'server-request-id');
  });
}

ExampleCubit _cubit({
  required IapStackClient backend,
  required AppleIapPlatform platform,
}) => ExampleCubit(
  backend: backend,
  apple: AppleIapStack(
    client: backend,
    productKinds: const <String, AppleProductKind>{
      'premium_monthly': AppleProductKind.subscription,
    },
    platform: platform,
  ),
  sandboxTools: const SandboxTools(),
  externalCustomerId: _customerId,
  subscriptionProductId: 'premium_monthly',
  productIds: const <String>{'premium_monthly'},
  requestIdFactory: (operation) => 'apple-$operation-test',
);

IapStackClient _backend(Future<http.Response> Function(http.Request) handler) =>
    IapStackClient(
      IapStackConfig(
        baseUri: Uri.parse('https://iap.example'),
        applicationId: 'application-1',
        customerToken: 'customer-token',
        retryPolicy: const IapStackRetryPolicy(maxAttempts: 1),
      ),
      httpClient: MockClient(handler),
    );

ApplePurchase _purchase({
  ApplePurchaseStatus status = ApplePurchaseStatus.purchased,
  String? errorCode,
}) => ApplePurchase(
  transactionId: 'transaction-100',
  productId: 'premium_monthly',
  signedTransaction: 'header.payload.signature',
  appAccountToken: _customerId,
  status: status,
  pendingCompletion: status == ApplePurchaseStatus.purchased,
  errorCode: errorCode,
);

const AppleProduct _monthlyProduct = AppleProduct(
  id: 'premium_monthly',
  kind: AppleProductKind.subscription,
  title: 'Premium Monthly',
  description: 'Monthly access',
  price: r'$4.99',
  rawPrice: 4.99,
  currencyCode: 'USD',
);

final Map<String, Object?> _verificationJson = <String, Object?>{
  'verified_at': '2026-08-26T10:00:00Z',
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

final Map<String, Object?> _snapshotJson = <String, Object?>{
  'customer_id': 'customer-internal',
  'entitlements': _verificationJson['entitlements'],
};

Future<void> _flushAsync() async {
  await Future<void>.delayed(Duration.zero);
  await Future<void>.delayed(Duration.zero);
}

final class _FakePlatform implements AppleIapPlatform {
  _FakePlatform({
    AppleProductQuery? query,
    Stream<ApplePurchase>? updates,
    List<ApplePurchase> restored = const <ApplePurchase>[],
    this.available = true,
    this.launchError,
  }) : query =
           query ??
           AppleProductQuery(
             products: const <AppleProduct>[],
             notFoundProductIds: const <String>{},
           ),
       updates = updates ?? const Stream<ApplePurchase>.empty(),
       restored = List<ApplePurchase>.of(restored);

  final AppleProductQuery query;
  final Stream<ApplePurchase> updates;
  final List<ApplePurchase> restored;
  final bool available;
  final AppleIapStackException? launchError;
  final List<ApplePurchase> completedPurchases = <ApplePurchase>[];
  Set<String>? queriedProductIds;
  AppleProduct? launchedProduct;
  String? launchedToken;

  @override
  Stream<ApplePurchase> get purchaseUpdates => updates;

  @override
  Future<bool> isAvailable() async => available;

  @override
  Future<AppleProductQuery> queryProducts(Set<String> productIds) async {
    queriedProductIds = Set<String>.of(productIds);
    return query;
  }

  @override
  Future<void> launchPurchase({
    required AppleProduct product,
    required String appAccountToken,
  }) async {
    final error = launchError;
    if (error != null) {
      throw error;
    }
    launchedProduct = product;
    launchedToken = appAccountToken;
  }

  @override
  Future<List<ApplePurchase>> restorePurchases() async => restored;

  @override
  Future<void> completePurchase(ApplePurchase purchase) async {
    completedPurchases.add(purchase);
  }
}
