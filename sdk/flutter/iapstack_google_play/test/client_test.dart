import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';

void main() {
  group('GooglePlayIapStack', () {
    test('delegates Billing availability checks', () async {
      final googlePlay = _googlePlay(platform: _FakePlatform(available: false));

      expect(await googlePlay.isAvailable(), isFalse);
    });

    test('rejects account identifiers longer than the Billing limit', () async {
      final platform = _FakePlatform();
      final googlePlay = _googlePlay(platform: platform);

      await expectLater(
        googlePlay.launchPurchase(
          externalCustomerId: 'a' * 65,
          product: const GooglePlayProduct(
            id: 'premium_lifetime',
            kind: GooglePlayProductKind.nonConsumable,
            title: 'Premium Lifetime',
            description: 'Premium access',
            price: r'$49.99',
            rawPrice: 49.99,
            currencyCode: 'USD',
          ),
        ),
        throwsArgumentError,
      );
      expect(platform.launchedProduct, isNull);
    });

    test(
      'queries configured products and launches with customer binding',
      () async {
        const product = GooglePlayProduct(
          id: 'premium_monthly',
          kind: GooglePlayProductKind.subscription,
          title: 'Premium Monthly',
          description: 'Premium access',
          price: r'$4.99',
          rawPrice: 4.99,
          currencyCode: 'USD',
          offerToken: 'base-plan-offer',
        );
        final platform = _FakePlatform(
          productQuery: GooglePlayProductQuery(
            products: const <GooglePlayProduct>[product],
            notFoundProductIds: const <String>{},
          ),
        );
        final googlePlay = _googlePlay(platform: platform);

        final query = await googlePlay.queryProducts(const <String>{
          'premium_monthly',
        });
        await googlePlay.launchPurchase(
          externalCustomerId: 'customer-1',
          product: query.products.single,
        );

        expect(query.products.single, same(product));
        expect(platform.queriedProductIds, const <String>{'premium_monthly'});
        expect(platform.launchedProduct, same(product));
        expect(platform.launchedAccountId, 'customer-1');
      },
    );

    test('verifies exact token evidence and configured product kind', () async {
      late Map<String, Object?> requestJson;
      final backend = _backend((request) async {
        requestJson = (jsonDecode(request.body) as Map).cast<String, Object?>();
        return http.Response(jsonEncode(_verificationJson), 200);
      });
      final googlePlay = GooglePlayIapStack(
        client: backend,
        productKinds: const <String, GooglePlayProductKind>{
          'premium_lifetime': GooglePlayProductKind.nonConsumable,
        },
        platform: _FakePlatform(),
      );

      final result = await googlePlay.verifyPurchase(
        externalCustomerId: 'customer-1',
        purchase: _purchase('premium_lifetime', token: 'opaque-token'),
      );

      expect(requestJson['external_customer_id'], 'customer-1');
      expect(requestJson['claimed_products'], <String>['premium_lifetime']);
      expect(requestJson['evidence'], <String, Object?>{
        'purchase_token': 'opaque-token',
        'product_kind': 'non_consumable',
      });
      expect(result.entitlements.single.grantsAccess, isTrue);
    });

    test(
      'rejects a mismatched account binding before contacting IAPStack',
      () async {
        var backendCalled = false;
        final googlePlay = GooglePlayIapStack(
          client: _backend((request) async {
            backendCalled = true;
            return http.Response('{}', 500);
          }),
          productKinds: const <String, GooglePlayProductKind>{
            'premium_lifetime': GooglePlayProductKind.nonConsumable,
          },
          platform: _FakePlatform(),
        );

        await expectLater(
          googlePlay.verifyPurchase(
            externalCustomerId: 'customer-1',
            purchase: _purchase(
              'premium_lifetime',
              accountId: 'another-customer',
            ),
          ),
          throwsA(
            isA<GooglePlayIapStackException>().having(
              (error) => error.code,
              'code',
              'customer_binding_mismatch',
            ),
          ),
        );
        expect(backendCalled, isFalse);
      },
    );

    test('deduplicates owned tokens and batches at the API limit', () async {
      final productKinds = <String, GooglePlayProductKind>{};
      final purchases = <GooglePlayPurchase>[];
      for (var index = 0; index < 101; index++) {
        final productId = 'premium_$index';
        productKinds[productId] = GooglePlayProductKind.nonConsumable;
        purchases.add(_purchase(productId, token: 'token-$index'));
      }
      purchases.add(_purchase('premium_0', token: 'token-0'));
      purchases.add(
        _purchase(
          'premium_0',
          token: 'pending-token',
          status: GooglePlayPurchaseStatus.pending,
        ),
      );
      final batchSizes = <int>[];
      final requestIds = <String?>[];
      final googlePlay = GooglePlayIapStack(
        client: _backend((request) async {
          final json = (jsonDecode(request.body) as Map)
              .cast<String, Object?>();
          final batch = json['purchases']! as List<Object?>;
          batchSizes.add(batch.length);
          requestIds.add(request.headers['X-Request-ID']);
          return http.Response(
            jsonEncode(<String, Object?>{
              'results': List<Object?>.filled(batch.length, _verificationJson),
            }),
            200,
          );
        }),
        productKinds: productKinds,
        platform: _FakePlatform(owned: purchases),
      );

      final restored = await googlePlay.restorePurchases(
        externalCustomerId: 'customer-1',
        requestId: 'google-restore-1',
      );

      expect(batchSizes, <int>[100, 1]);
      expect(requestIds, <String?>['google-restore-1', 'google-restore-1-2']);
      expect(restored.results, hasLength(101));
    });

    test('rejects cancelled and multi-product updates', () async {
      final googlePlay = _googlePlay(platform: _FakePlatform());
      await expectLater(
        googlePlay.verifyPurchase(
          externalCustomerId: 'customer-1',
          purchase: _purchase(
            'premium_monthly',
            status: GooglePlayPurchaseStatus.cancelled,
          ),
        ),
        throwsA(
          isA<GooglePlayIapStackException>()
              .having((error) => error.code, 'code', 'purchase_cancelled')
              .having((error) => error.userCancelled, 'userCancelled', isTrue),
        ),
      );
      await expectLater(
        googlePlay.verifyPurchase(
          externalCustomerId: 'customer-1',
          purchase: GooglePlayPurchase(
            purchaseToken: 'opaque-token',
            productIds: const <String>['premium_monthly', 'premium_lifetime'],
            status: GooglePlayPurchaseStatus.purchased,
            isAcknowledged: false,
            obfuscatedAccountId: 'customer-1',
          ),
        ),
        throwsA(
          isA<GooglePlayIapStackException>().having(
            (error) => error.code,
            'code',
            'unsupported_multi_product_purchase',
          ),
        ),
      );
    });

    test('keeps evidence and purchase string representations redacted', () {
      final purchase = _purchase(
        'premium_monthly',
        token: 'secret-purchase-token',
      );
      final evidence = GooglePlayPurchaseEvidence(
        purchaseToken: 'secret-purchase-token',
        productKind: GooglePlayProductKind.subscription,
      );

      expect(purchase.toString(), isNot(contains('secret-purchase-token')));
      expect(evidence.toString(), isNot(contains('secret-purchase-token')));
    });
  });
}

GooglePlayIapStack _googlePlay({required GooglePlayIapPlatform platform}) =>
    GooglePlayIapStack(
      client: _backend((request) async => http.Response('{}', 500)),
      productKinds: const <String, GooglePlayProductKind>{
        'premium_monthly': GooglePlayProductKind.subscription,
        'premium_lifetime': GooglePlayProductKind.nonConsumable,
      },
      platform: platform,
    );

IapStackClient _backend(Future<http.Response> Function(http.Request) handler) =>
    IapStackClient(
      IapStackConfig(
        baseUri: Uri.parse('https://iap.example'),
        applicationId: 'application-1',
        applicationKey: 'application-key',
        retryPolicy: const IapStackRetryPolicy(maxAttempts: 1),
      ),
      httpClient: MockClient(handler),
    );

GooglePlayPurchase _purchase(
  String productId, {
  String token = 'opaque-token',
  String accountId = 'customer-1',
  GooglePlayPurchaseStatus status = GooglePlayPurchaseStatus.purchased,
}) => GooglePlayPurchase(
  purchaseToken: token,
  productIds: <String>[productId],
  status: status,
  isAcknowledged: false,
  obfuscatedAccountId: accountId,
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

final class _FakePlatform implements GooglePlayIapPlatform {
  _FakePlatform({
    GooglePlayProductQuery? productQuery,
    List<GooglePlayPurchase> owned = const <GooglePlayPurchase>[],
    this.available = true,
  }) : productQuery =
           productQuery ??
           GooglePlayProductQuery(
             products: const <GooglePlayProduct>[],
             notFoundProductIds: const <String>{},
           ),
       owned = List<GooglePlayPurchase>.of(owned);

  final GooglePlayProductQuery productQuery;
  final List<GooglePlayPurchase> owned;
  final bool available;
  Set<String>? queriedProductIds;
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
  }) async => List<GooglePlayPurchase>.of(owned);

  @override
  Future<GooglePlayProductQuery> queryProducts(Set<String> productIds) async {
    queriedProductIds = Set<String>.of(productIds);
    return productQuery;
  }
}
