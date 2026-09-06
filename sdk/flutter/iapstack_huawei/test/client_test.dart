import 'dart:collection';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_huawei/iapstack_huawei.dart';

void main() {
  group('HuaweiIapStack', () {
    test(
        'purchases with the external customer binding and preserves signed data',
        () async {
      late Map<String, dynamic> requestJson;
      final platform = _FakePlatform(
        purchaseResult:
            _signedPurchase('premium_monthly', customerId: 'customer-1'),
      );
      final backend = _backend((request) async {
        requestJson = jsonDecode(request.body) as Map<String, dynamic>;
        return http.Response(jsonEncode(_verificationJson), 200);
      });
      final huawei = HuaweiIapStack(
        client: backend,
        productKinds: _productKinds,
        platform: platform,
      );

      final products = await huawei.queryProducts(
        const <String>{'premium_monthly'},
      );

      final result = await huawei.purchaseAndVerify(
        externalCustomerId: 'customer-1',
        product: products.products.single,
      );

      expect(platform.purchasedProductId, 'premium_monthly');
      expect(platform.purchasedDeveloperPayload, 'customer-1');
      expect(requestJson['claimed_products'], <String>['premium_monthly']);
      expect(requestJson['external_customer_id'], 'customer-1');
      final evidence = requestJson['evidence']! as Map<String, dynamic>;
      expect(evidence['purchase_data'],
          _purchaseData('premium_monthly', customerId: 'customer-1'));
      expect(evidence['signature'], 'signature-premium_monthly');
      expect(evidence['product_kind'], 'subscription');
      expect(result.entitlements.single.grantsAccess, isTrue);
    });

    test('exposes device sandbox eligibility before purchase', () async {
      const expected = HuaweiSandboxStatus(
        isSandboxUser: true,
        isSandboxApk: true,
        marketVersion: '42',
        apkVersion: '43',
      );
      final huawei = HuaweiIapStack(
        client: _backend((request) async => http.Response('{}', 500)),
        productKinds: _productKinds,
        platform: _FakePlatform(sandboxResult: expected),
      );

      final status = await huawei.sandboxStatus();

      expect(status, same(expected));
      expect(status.isActive, isTrue);
    });

    test('checks Huawei environment readiness', () async {
      final huawei = HuaweiIapStack(
        client: _backend((request) async => http.Response('{}', 500)),
        productKinds: _productKinds,
        platform: _FakePlatform(environmentAvailable: true),
      );

      expect(await huawei.isAvailable(), isTrue);
    });

    test('groups configured product queries by kind and reports missing IDs',
        () async {
      final platform = _FakePlatform(
        omittedProductIds: const <String>{'premium_lifetime'},
      );
      final huawei = HuaweiIapStack(
        client: _backend((request) async => http.Response('{}', 500)),
        productKinds: _productKinds,
        platform: platform,
      );

      final result = await huawei.queryProducts(_productKinds.keys.toSet());

      expect(result.products.single.id, 'premium_monthly');
      expect(result.notFoundProductIds, <String>{'premium_lifetime'});
      expect(platform.productQueryCalls, <String>[
        'nonConsumable:premium_lifetime',
        'subscription:premium_monthly',
      ]);
    });

    test('requires a queried, purchasable product', () async {
      final platform = _FakePlatform();
      final huawei = HuaweiIapStack(
        client: _backend((request) async => http.Response('{}', 500)),
        productKinds: _productKinds,
        platform: platform,
      );

      await expectLater(
        huawei.purchaseAndVerify(
          externalCustomerId: 'customer-1',
          product: _product('premium_monthly'),
        ),
        throwsA(
          isA<HuaweiIapStackException>().having(
            (error) => error.code,
            'code',
            'product_not_queried',
          ),
        ),
      );
      expect(platform.purchaseCalls, 0);
    });

    test('paginates, deduplicates, and restores both supported kinds',
        () async {
      final lifetime =
          _signedPurchase('premium_lifetime', customerId: 'customer-1');
      final monthly =
          _signedPurchase('premium_monthly', customerId: 'customer-1');
      final platform = _FakePlatform(
        pages: <HuaweiProductKind, Queue<HuaweiOwnedPurchasesPage>>{
          HuaweiProductKind.nonConsumable: Queue<HuaweiOwnedPurchasesPage>.of(
            <HuaweiOwnedPurchasesPage>[
              HuaweiOwnedPurchasesPage(
                  purchases: <HuaweiSignedPurchase>[lifetime],
                  continuationToken: 'next'),
              HuaweiOwnedPurchasesPage(
                  purchases: <HuaweiSignedPurchase>[lifetime]),
            ],
          ),
          HuaweiProductKind.subscription: Queue<HuaweiOwnedPurchasesPage>.of(
            <HuaweiOwnedPurchasesPage>[
              HuaweiOwnedPurchasesPage(
                  purchases: <HuaweiSignedPurchase>[monthly]),
            ],
          ),
        },
      );
      late List<dynamic> purchases;
      final huawei = HuaweiIapStack(
        client: _backend((request) async {
          purchases = (jsonDecode(request.body)
              as Map<String, dynamic>)['purchases']! as List<dynamic>;
          return http.Response(
            jsonEncode(<String, Object?>{
              'results':
                  List<Object?>.filled(purchases.length, _verificationJson),
            }),
            200,
          );
        }),
        productKinds: _productKinds,
        platform: platform,
      );

      final result =
          await huawei.restorePurchases(externalCustomerId: 'customer-1');

      expect(platform.restoreCalls,
          <String>['nonConsumable:', 'nonConsumable:next', 'subscription:']);
      expect(purchases, hasLength(2));
      expect(result.results, hasLength(2));
    });

    test('skips unexpected restore history and verifies configured purchases',
        () async {
      late List<dynamic> submittedPurchases;
      final platform = _FakePlatform(
        pages: <HuaweiProductKind, Queue<HuaweiOwnedPurchasesPage>>{
          HuaweiProductKind.nonConsumable: Queue<HuaweiOwnedPurchasesPage>.of(
            <HuaweiOwnedPurchasesPage>[
              HuaweiOwnedPurchasesPage(
                purchases: <HuaweiSignedPurchase>[
                  _signedPurchase('legacy_product', customerId: 'customer-1'),
                  _signedPurchase('premium_lifetime',
                      customerId: 'another-customer'),
                  const HuaweiSignedPurchase(purchaseData: '', signature: ''),
                  _signedPurchase('premium_lifetime', customerId: 'customer-1'),
                ],
              ),
            ],
          ),
        },
      );
      final huawei = HuaweiIapStack(
        client: _backend((request) async {
          submittedPurchases = (jsonDecode(request.body)
              as Map<String, dynamic>)['purchases']! as List<dynamic>;
          return http.Response(
            jsonEncode(<String, Object?>{
              'results': <Object?>[_verificationJson],
            }),
            200,
          );
        }),
        productKinds: _productKinds,
        platform: platform,
      );

      final result = await huawei.restorePurchases(
        externalCustomerId: 'customer-1',
        productKinds: const <HuaweiProductKind>{
          HuaweiProductKind.nonConsumable
        },
      );

      expect(submittedPurchases, hasLength(1));
      final submission =
          (submittedPurchases.single as Map).cast<String, Object?>();
      expect(submission['claimed_products'], <String>['premium_lifetime']);
      expect((submission['evidence']! as Map)['purchase_data'],
          _purchaseData('premium_lifetime', customerId: 'customer-1'));
      expect(result.results, hasLength(1));
    });

    test('splits more than 100 restored purchases into bounded API batches',
        () async {
      final owned = List<HuaweiSignedPurchase>.generate(
        101,
        (index) => _signedPurchase('product-$index', customerId: 'customer-1'),
      );
      final platform = _FakePlatform(
        pages: <HuaweiProductKind, Queue<HuaweiOwnedPurchasesPage>>{
          HuaweiProductKind.nonConsumable: Queue<HuaweiOwnedPurchasesPage>.of(
            <HuaweiOwnedPurchasesPage>[
              HuaweiOwnedPurchasesPage(purchases: owned)
            ],
          ),
        },
      );
      final batchSizes = <int>[];
      final requestIds = <String?>[];
      final productKinds = <String, HuaweiProductKind>{
        for (var index = 0; index < 101; index++)
          'product-$index': HuaweiProductKind.nonConsumable,
      };
      final huawei = HuaweiIapStack(
        client: _backend((request) async {
          final purchases = (jsonDecode(request.body)
              as Map<String, dynamic>)['purchases']! as List<dynamic>;
          batchSizes.add(purchases.length);
          requestIds.add(request.headers['X-Request-ID']);
          return http.Response(
            jsonEncode(<String, Object?>{
              'results':
                  List<Object?>.filled(purchases.length, _verificationJson),
            }),
            200,
          );
        }),
        productKinds: productKinds,
        platform: platform,
      );

      final result = await huawei.restorePurchases(
        externalCustomerId: 'customer-1',
        requestId: 'sandbox-restore-1',
        productKinds: const <HuaweiProductKind>{
          HuaweiProductKind.nonConsumable
        },
      );

      expect(batchSizes, <int>[100, 1]);
      expect(requestIds, <String?>[
        'sandbox-restore-1',
        'sandbox-restore-1-2',
      ]);
      expect(result.results, hasLength(101));
    });

    test('rejects repeated continuation tokens', () async {
      final platform = _FakePlatform(
        pages: <HuaweiProductKind, Queue<HuaweiOwnedPurchasesPage>>{
          HuaweiProductKind.subscription: Queue<HuaweiOwnedPurchasesPage>.of(
            <HuaweiOwnedPurchasesPage>[
              HuaweiOwnedPurchasesPage(
                  purchases: const <HuaweiSignedPurchase>[],
                  continuationToken: 'same'),
              HuaweiOwnedPurchasesPage(
                  purchases: const <HuaweiSignedPurchase>[],
                  continuationToken: 'same'),
            ],
          ),
        },
      );
      final huawei = HuaweiIapStack(
        client: _backend((request) async => http.Response('{}', 500)),
        productKinds: _productKinds,
        platform: platform,
      );

      await expectLater(
        huawei.restorePurchases(
          externalCustomerId: 'customer-1',
          productKinds: const <HuaweiProductKind>{
            HuaweiProductKind.subscription
          },
        ),
        throwsA(
          isA<HuaweiIapStackException>().having(
              (error) => error.code, 'code', 'restore_pagination_cycle'),
        ),
      );
    });

    test('rejects customer binding mismatch before contacting IAPStack',
        () async {
      var backendCalled = false;
      final huawei = HuaweiIapStack(
        client: _backend((request) async {
          backendCalled = true;
          return http.Response('{}', 500);
        }),
        productKinds: _productKinds,
        platform: _FakePlatform(
          purchaseResult: _signedPurchase('premium_monthly',
              customerId: 'another-customer'),
        ),
      );

      final products = await huawei.queryProducts(
        const <String>{'premium_monthly'},
      );

      await expectLater(
        huawei.purchaseAndVerify(
          externalCustomerId: 'customer-1',
          product: products.products.single,
        ),
        throwsA(
          isA<HuaweiIapStackException>().having(
              (error) => error.code, 'code', 'customer_binding_mismatch'),
        ),
      );
      expect(backendCalled, isFalse);
    });
  });
}

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

HuaweiSignedPurchase _signedPurchase(String productId,
        {required String customerId}) =>
    HuaweiSignedPurchase(
      purchaseData: _purchaseData(productId, customerId: customerId),
      signature: 'signature-$productId',
    );

String _purchaseData(String productId, {required String customerId}) =>
    jsonEncode(<String, Object?>{
      'productId': productId,
      'developerPayload': customerId,
      'purchaseToken': 'token-$productId',
    });

final Map<String, Object?> _verificationJson = <String, Object?>{
  'verified_at': '2026-08-24T20:00:00Z',
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

const Map<String, HuaweiProductKind> _productKinds =
    <String, HuaweiProductKind>{
  'premium_lifetime': HuaweiProductKind.nonConsumable,
  'premium_monthly': HuaweiProductKind.subscription,
};

HuaweiProduct _product(String id, {int status = 0}) => HuaweiProduct(
      id: id,
      kind: _productKinds[id]!,
      title: id,
      description: 'Description for $id',
      price: r'$4.99',
      priceMicros: 4990000,
      currency: 'USD',
      status: status,
      subscriptionPeriod:
          _productKinds[id] == HuaweiProductKind.subscription ? 'P1M' : null,
    );

final class _FakePlatform implements HuaweiIapPlatform {
  _FakePlatform(
      {this.purchaseResult,
      this.environmentAvailable = false,
      this.sandboxResult = const HuaweiSandboxStatus(
        isSandboxUser: false,
        isSandboxApk: false,
      ),
      this.omittedProductIds = const <String>{},
      Map<HuaweiProductKind, Queue<HuaweiOwnedPurchasesPage>>? pages})
      : pages = pages ?? <HuaweiProductKind, Queue<HuaweiOwnedPurchasesPage>>{};

  final HuaweiSignedPurchase? purchaseResult;
  final bool environmentAvailable;
  final HuaweiSandboxStatus sandboxResult;
  final Set<String> omittedProductIds;
  final Map<HuaweiProductKind, Queue<HuaweiOwnedPurchasesPage>> pages;
  final List<String> restoreCalls = <String>[];
  final List<String> productQueryCalls = <String>[];
  int purchaseCalls = 0;
  String? purchasedProductId;
  String? purchasedDeveloperPayload;

  @override
  Future<bool> isAvailable() async => environmentAvailable;

  @override
  Future<List<HuaweiProduct>> queryProducts({
    required List<String> productIds,
    required HuaweiProductKind productKind,
  }) async {
    productQueryCalls.add('${productKind.name}:${productIds.join(',')}');
    return productIds
        .where((id) => !omittedProductIds.contains(id))
        .map(_product)
        .toList(growable: false);
  }

  @override
  Future<HuaweiSandboxStatus> sandboxStatus() async => sandboxResult;

  @override
  Future<HuaweiSignedPurchase> purchase({
    required String productId,
    required HuaweiProductKind productKind,
    required String developerPayload,
  }) async {
    purchaseCalls++;
    purchasedProductId = productId;
    purchasedDeveloperPayload = developerPayload;
    final value = purchaseResult;
    if (value == null) {
      throw StateError('purchase result was not configured');
    }
    return value;
  }

  @override
  Future<HuaweiOwnedPurchasesPage> ownedPurchases({
    required HuaweiProductKind productKind,
    String? continuationToken,
  }) async {
    restoreCalls.add('${productKind.name}:${continuationToken ?? ''}');
    final queue = pages[productKind];
    if (queue == null || queue.isEmpty) {
      return HuaweiOwnedPurchasesPage(
          purchases: const <HuaweiSignedPurchase>[]);
    }
    return queue.removeFirst();
  }
}
