import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';
import 'package:iapstack_google_play_example/example_cubit.dart';

void main() {
  test('requires complete Billing catalog readiness before checkout', () async {
    final backend = _backend((request) async => http.Response('{}', 500));
    final product = _subscriptionProduct();
    final platform = _FakePlatform(
      query: GooglePlayProductQuery(
        products: <GooglePlayProduct>[product],
        notFoundProductIds: const <String>{},
      ),
    );
    final cubit = _cubit(backend: backend, platform: platform);
    addTearDown(cubit.close);

    await cubit.initialize();
    await cubit.purchase(cubit.state.products.single);

    expect(cubit.state.catalogReady, isTrue);
    expect(cubit.state.status, ExampleStatus.ready);
    expect(platform.launchedProduct, product);
    expect(platform.launchedAccountId, 'customer-1');
  });

  test('blocks checkout when a configured product is missing', () async {
    final backend = _backend((request) async => http.Response('{}', 500));
    final platform = _FakePlatform(
      query: GooglePlayProductQuery(
        products: <GooglePlayProduct>[],
        notFoundProductIds: const <String>{'premium-monthly'},
      ),
    );
    final cubit = _cubit(backend: backend, platform: platform);
    addTearDown(cubit.close);

    await cubit.initialize();

    expect(cubit.state.catalogReady, isFalse);
    expect(cubit.state.status, ExampleStatus.failure);
    expect(cubit.state.message, contains('premium-monthly'));
    expect(platform.launchedProduct, isNull);
  });

  test(
    'allows backend entitlement refresh when Billing is unavailable',
    () async {
      final backend = _backend(
        (request) async => http.Response(jsonEncode(_snapshotJson), 200),
      );
      final cubit = _cubit(
        backend: backend,
        platform: _FakePlatform(available: false),
      );
      addTearDown(cubit.close);

      await cubit.initialize();
      expect(cubit.state.billingAvailable, isFalse);

      await cubit.refresh();

      expect(cubit.state.status, ExampleStatus.ready);
      expect(cubit.state.entitlements.single.grantsAccess, isTrue);
      expect(cubit.state.requestId, 'play-refresh-test');
    },
  );
}

ExampleCubit _cubit({
  required IapStackClient backend,
  required GooglePlayIapPlatform platform,
}) => ExampleCubit(
  backend: backend,
  googlePlay: GooglePlayIapStack(
    client: backend,
    productKinds: const <String, GooglePlayProductKind>{
      'premium-monthly': GooglePlayProductKind.subscription,
    },
    platform: platform,
  ),
  externalCustomerId: 'customer-1',
  productIds: const <String>{'premium-monthly'},
  requestIdFactory: (operation) => 'play-$operation-test',
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

GooglePlayProduct _subscriptionProduct() => GooglePlayProduct(
  id: 'premium-monthly',
  kind: GooglePlayProductKind.subscription,
  title: 'Premium Monthly',
  description: 'Monthly access',
  price: r'$4.99',
  priceMicros: 4990000,
  currencyCode: 'USD',
  offerToken: 'monthly-token',
  basePlanId: 'monthly',
  pricingPhases: const <GooglePlayPricingPhase>[
    GooglePlayPricingPhase(
      billingCycleCount: 0,
      billingPeriod: 'P1M',
      formattedPrice: r'$4.99',
      priceMicros: 4990000,
      currencyCode: 'USD',
      recurrence: GooglePlayPricingRecurrence.infinite,
    ),
  ],
);

final Map<String, Object?> _snapshotJson = <String, Object?>{
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

final class _FakePlatform implements GooglePlayIapPlatform {
  _FakePlatform({this.available = true, GooglePlayProductQuery? query})
    : query =
          query ??
          GooglePlayProductQuery(
            products: <GooglePlayProduct>[],
            notFoundProductIds: const <String>{'premium-monthly'},
          );

  final bool available;
  final GooglePlayProductQuery query;
  GooglePlayProduct? launchedProduct;
  String? launchedAccountId;

  @override
  Stream<GooglePlayPurchase> get purchaseUpdates => const Stream.empty();

  @override
  Future<bool> isAvailable() async => available;

  @override
  Future<void> launchPurchase({
    required GooglePlayProduct product,
    required String obfuscatedAccountId,
  }) async {
    launchedProduct = product;
    launchedAccountId = obfuscatedAccountId;
  }

  @override
  Future<List<GooglePlayPurchase>> ownedPurchases({
    required String obfuscatedAccountId,
  }) async => const <GooglePlayPurchase>[];

  @override
  Future<GooglePlayProductQuery> queryProducts(Set<String> productIds) async =>
      query;
}
