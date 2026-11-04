import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_apple/iapstack_apple.dart';

const _customerId = '018f59d0-a200-7000-8000-000000000001';

void main() {
  group('AppleIapStack', () {
    test('delegates StoreKit availability checks', () async {
      final apple = _apple(platform: _FakePlatform(available: false));

      expect(await apple.isAvailable(), isFalse);
    });

    test('requires a canonical UUID app account token', () async {
      final apple = _apple(platform: _FakePlatform());

      await expectLater(
        apple.launchPurchase(
          externalCustomerId: 'customer-1',
          product: _monthlyProduct,
        ),
        throwsArgumentError,
      );
    });

    test(
      'queries configured products and preserves customer binding',
      () async {
        final platform = _FakePlatform(
          productQuery: AppleProductQuery(
            products: const <AppleProduct>[_monthlyProduct],
            notFoundProductIds: const <String>{'missing'},
          ),
        );
        final apple = _apple(platform: platform);

        final query = await apple.queryProducts(const <String>{
          'premium_monthly',
        });
        await apple.launchPurchase(
          externalCustomerId: _customerId,
          product: query.products.single,
        );

        expect(platform.queriedProductIds, const <String>{'premium_monthly'});
        expect(platform.launchedToken, _customerId);
        expect(query.notFoundProductIds, const <String>{'missing'});
      },
    );

    test(
      'verifies signed JWS before completing the StoreKit transaction',
      () async {
        final events = <String>[];
        late Map<String, Object?> requestJson;
        final platform = _FakePlatform(events: events);
        final apple = AppleIapStack(
          client: _backend((request) async {
            events.add('verified');
            requestJson = (jsonDecode(request.body) as Map)
                .cast<String, Object?>();
            return http.Response(jsonEncode(_verificationJson), 200);
          }),
          productKinds: const <String, AppleProductKind>{
            'premium_monthly': AppleProductKind.subscription,
          },
          platform: platform,
        );

        final result = await apple.verifyPurchase(
          externalCustomerId: _customerId,
          purchase: _purchase(pendingCompletion: true),
        );

        expect(events, <String>['verified', 'completed']);
        expect(requestJson['external_customer_id'], _customerId);
        expect(requestJson['claimed_products'], <String>['premium_monthly']);
        expect(requestJson['evidence'], <String, Object?>{
          'signed_transaction': 'header.payload.signature',
          'product_kind': 'subscription',
        });
        expect(result.entitlements.single.grantsAccess, isTrue);
      },
    );

    test(
      'does not finish a transaction when server verification fails',
      () async {
        final events = <String>[];
        final apple = AppleIapStack(
          client: _backend((request) async {
            events.add('failed');
            return http.Response(
              jsonEncode(<String, Object?>{
                'error': <String, Object?>{
                  'code': 'provider_unavailable',
                  'message': 'Verification is temporarily unavailable',
                },
              }),
              503,
            );
          }),
          productKinds: const <String, AppleProductKind>{
            'premium_monthly': AppleProductKind.subscription,
          },
          platform: _FakePlatform(events: events),
        );

        await expectLater(
          apple.verifyPurchase(
            externalCustomerId: _customerId,
            purchase: _purchase(pendingCompletion: true),
          ),
          throwsA(isA<IapStackException>()),
        );
        expect(events, <String>['failed']);
      },
    );

    test(
      'deduplicates restored transaction IDs before batch verification',
      () async {
        final batchSizes = <int>[];
        final platform = _FakePlatform(
          restored: <ApplePurchase>[
            _purchase(transactionId: '100'),
            _purchase(transactionId: '100'),
            _purchase(transactionId: '101'),
          ],
        );
        final apple = AppleIapStack(
          client: _backend((request) async {
            final body = (jsonDecode(request.body) as Map)
                .cast<String, Object?>();
            final purchases = body['purchases']! as List<Object?>;
            batchSizes.add(purchases.length);
            return http.Response(
              jsonEncode(<String, Object?>{
                'results': List<Object?>.filled(
                  purchases.length,
                  _verificationJson,
                ),
              }),
              200,
            );
          }),
          productKinds: const <String, AppleProductKind>{
            'premium_monthly': AppleProductKind.subscription,
          },
          platform: platform,
        );

        final result = await apple.restorePurchases(
          externalCustomerId: _customerId,
        );

        expect(batchSizes, <int>[2]);
        expect(result.results, hasLength(2));
      },
    );

    test(
      'finishes unfinished restored rows after the server accepts them',
      () async {
        final events = <String>[];
        final platform = _FakePlatform(
          events: events,
          restored: <ApplePurchase>[
            _purchase(transactionId: '100', pendingCompletion: true),
            _purchase(transactionId: '101'),
            _purchase(transactionId: '102', pendingCompletion: true),
          ],
        );
        final apple = AppleIapStack(
          client: _backend((request) async {
            events.add('restored');
            return _restoreResponse(request);
          }),
          productKinds: const <String, AppleProductKind>{
            'premium_monthly': AppleProductKind.subscription,
          },
          platform: platform,
        );

        final result = await apple.restorePurchases(
          externalCustomerId: _customerId,
        );

        expect(result.results, hasLength(3));
        expect(events, <String>['restored', 'completed', 'completed']);
        expect(platform.completedIds, <String>['100', '102']);
      },
    );

    test('surfaces a StoreKit failure to finish an accepted row', () async {
      final platform = _FakePlatform(
        restored: <ApplePurchase>[
          _purchase(transactionId: '100', pendingCompletion: true),
          _purchase(transactionId: '101', pendingCompletion: true),
        ],
        completionError: const AppleIapStackException(
          code: 'apple_completion_failed',
          message: 'Apple completion failed',
        ),
      );
      final apple = AppleIapStack(
        client: _backend((request) async => _restoreResponse(request)),
        productKinds: const <String, AppleProductKind>{
          'premium_monthly': AppleProductKind.subscription,
        },
        platform: platform,
      );

      await expectLater(
        apple.restorePurchases(externalCustomerId: _customerId),
        throwsA(
          isA<AppleIapStackException>().having(
            (error) => error.code,
            'code',
            'apple_completion_failed',
          ),
        ),
      );
      expect(platform.completedIds, <String>['100']);
    });

    test('leaves restored rows unfinished when restore fails', () async {
      final events = <String>[];
      final platform = _FakePlatform(
        events: events,
        restored: <ApplePurchase>[
          _purchase(transactionId: '100', pendingCompletion: true),
        ],
      );
      final apple = AppleIapStack(
        client: _backend((request) async {
          events.add('failed');
          return _unavailableResponse();
        }),
        productKinds: const <String, AppleProductKind>{
          'premium_monthly': AppleProductKind.subscription,
        },
        platform: platform,
      );

      await expectLater(
        apple.restorePurchases(externalCustomerId: _customerId),
        throwsA(isA<IapStackApiException>()),
      );
      expect(events, <String>['failed']);
      expect(platform.completedIds, isEmpty);
    });

    test(
      'finishes only rows from batches accepted before a later batch fails',
      () async {
        final transactionIds = List<String>.generate(
          maxRestoreBatchSize + 1,
          (index) => '${1000 + index}',
        );
        final platform = _FakePlatform(
          restored: <ApplePurchase>[
            for (final transactionId in transactionIds)
              _purchase(transactionId: transactionId, pendingCompletion: true),
          ],
        );
        var calls = 0;
        final apple = AppleIapStack(
          client: _backend((request) async {
            calls += 1;
            return calls == 1
                ? _restoreResponse(request)
                : _unavailableResponse();
          }),
          productKinds: const <String, AppleProductKind>{
            'premium_monthly': AppleProductKind.subscription,
          },
          platform: platform,
        );

        await expectLater(
          apple.restorePurchases(externalCustomerId: _customerId),
          throwsA(
            isA<IapStackApiException>().having(
              (error) => error.committedRestoreResults,
              'committedRestoreResults',
              hasLength(maxRestoreBatchSize),
            ),
          ),
        );
        expect(calls, 2);
        expect(
          platform.completedIds,
          transactionIds.take(maxRestoreBatchSize).toList(),
        );
      },
    );

    test(
      'skips unexpected restore history and verifies configured purchases',
      () async {
        late List<Object?> submittedPurchases;
        final platform = _FakePlatform(
          restored: <ApplePurchase>[
            _purchase(transactionId: 'unknown', productId: 'legacy_product'),
            _purchase(
              transactionId: 'other-customer',
              appAccountToken: '018f59d0-a200-7000-8000-000000000002',
            ),
            _purchase(transactionId: 'legacy-unbound', appAccountToken: ''),
            _purchase(transactionId: 'empty', signedTransaction: ''),
            _purchase(transactionId: 'malformed', signedTransaction: 'not-jws'),
            // Another customer's row counts as that, even without a JWS.
            _purchase(
              transactionId: 'other-customer-unsigned',
              appAccountToken: '018f59d0-a200-7000-8000-000000000002',
              signedTransaction: '',
            ),
            const ApplePurchase(
              transactionId: 'pending',
              productId: 'premium_monthly',
              signedTransaction: '',
              appAccountToken: _customerId,
              status: ApplePurchaseStatus.pending,
              pendingCompletion: false,
            ),
            _purchase(transactionId: 'configured'),
          ],
        );
        final apple = AppleIapStack(
          client: _backend((request) async {
            final body = (jsonDecode(request.body) as Map)
                .cast<String, Object?>();
            submittedPurchases = body['purchases']! as List<Object?>;
            return http.Response(
              jsonEncode(<String, Object?>{
                'results': <Object?>[_verificationJson],
              }),
              200,
            );
          }),
          productKinds: const <String, AppleProductKind>{
            'premium_monthly': AppleProductKind.subscription,
          },
          platform: platform,
        );

        final result = await apple.restorePurchases(
          externalCustomerId: _customerId,
        );

        expect(submittedPurchases, hasLength(1));
        final submission = (submittedPurchases.single as Map)
            .cast<String, Object?>();
        expect(submission['claimed_products'], <String>['premium_monthly']);
        expect(
          (submission['evidence']! as Map)['signed_transaction'],
          'header.payload.signature',
        );
        expect(result.results, hasLength(1));
        expect(result.skipped.unknownProduct, 1);
        expect(result.skipped.differentCustomer, 2);
        expect(result.skipped.unbound, 1);
        expect(result.skipped.multipleProducts, 0);
        expect(result.skipped.unsigned, 2);
        expect(result.skipped.total, 6);
        expect(result.pageLimitReached, isFalse);
      },
    );

    test(
      'rejects mismatched customer bindings and redacts JWS strings',
      () async {
        var backendCalled = false;
        final purchase = _purchase(
          appAccountToken: '018f59d0-a200-7000-8000-000000000002',
        );
        final apple = AppleIapStack(
          client: _backend((request) async {
            backendCalled = true;
            return http.Response('{}', 500);
          }),
          productKinds: const <String, AppleProductKind>{
            'premium_monthly': AppleProductKind.subscription,
          },
          platform: _FakePlatform(),
        );

        await expectLater(
          apple.verifyPurchase(
            externalCustomerId: _customerId,
            purchase: purchase,
          ),
          throwsA(
            isA<AppleIapStackException>().having(
              (error) => error.code,
              'code',
              'customer_binding_mismatch',
            ),
          ),
        );
        expect(backendCalled, isFalse);
        expect(
          purchase.toString(),
          isNot(contains('header.payload.signature')),
        );
        expect(
          ApplePurchaseEvidence(
            signedTransaction: 'header.payload.signature',
            productKind: AppleProductKind.subscription,
          ).toString(),
          isNot(contains('header.payload.signature')),
        );
      },
    );

    test('accepts StoreKit UUID bindings regardless of hex casing', () async {
      var backendCalled = false;
      final apple = AppleIapStack(
        client: _backend((request) async {
          backendCalled = true;
          return http.Response(jsonEncode(_verificationJson), 200);
        }),
        productKinds: const <String, AppleProductKind>{
          'premium_monthly': AppleProductKind.subscription,
        },
        platform: _FakePlatform(),
      );

      final result = await apple.verifyPurchase(
        externalCustomerId: _customerId,
        purchase: _purchase(appAccountToken: _customerId.toUpperCase()),
      );

      expect(backendCalled, isTrue);
      expect(result.entitlements.single.grantsAccess, isTrue);
    });
  });
}

AppleIapStack _apple({required AppleIapPlatform platform}) => AppleIapStack(
  client: _backend((request) async => http.Response('{}', 500)),
  productKinds: const <String, AppleProductKind>{
    'premium_monthly': AppleProductKind.subscription,
  },
  platform: platform,
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

http.Response _restoreResponse(http.Request request) {
  final body = (jsonDecode(request.body) as Map).cast<String, Object?>();
  final purchases = body['purchases']! as List<Object?>;
  return http.Response(
    jsonEncode(<String, Object?>{
      'results': List<Object?>.filled(purchases.length, _verificationJson),
    }),
    200,
  );
}

http.Response _unavailableResponse() => http.Response(
  jsonEncode(<String, Object?>{
    'error': <String, Object?>{
      'code': 'provider_unavailable',
      'message': 'Verification is temporarily unavailable',
    },
  }),
  503,
);

ApplePurchase _purchase({
  String transactionId = '100',
  String productId = 'premium_monthly',
  String signedTransaction = 'header.payload.signature',
  String appAccountToken = _customerId,
  bool pendingCompletion = false,
}) => ApplePurchase(
  transactionId: transactionId,
  productId: productId,
  signedTransaction: signedTransaction,
  appAccountToken: appAccountToken,
  status: ApplePurchaseStatus.restored,
  pendingCompletion: pendingCompletion,
);

const AppleProduct _monthlyProduct = AppleProduct(
  id: 'premium_monthly',
  kind: AppleProductKind.subscription,
  title: 'Premium Monthly',
  description: 'Premium access',
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

final class _FakePlatform implements AppleIapPlatform {
  _FakePlatform({
    AppleProductQuery? productQuery,
    List<ApplePurchase> restored = const <ApplePurchase>[],
    List<String>? events,
    this.available = true,
    this.completionError,
  }) : productQuery =
           productQuery ??
           AppleProductQuery(
             products: const <AppleProduct>[],
             notFoundProductIds: const <String>{},
           ),
       restored = List<ApplePurchase>.of(restored),
       events = events ?? <String>[];

  final AppleProductQuery productQuery;
  final List<ApplePurchase> restored;
  final List<String> events;
  final bool available;
  final AppleIapStackException? completionError;
  final List<String> completedIds = <String>[];
  Set<String>? queriedProductIds;
  String? launchedToken;

  @override
  Stream<ApplePurchase> get purchaseUpdates => const Stream.empty();

  @override
  Future<bool> isAvailable() async => available;

  @override
  Future<void> launchPurchase({
    required AppleProduct product,
    required String appAccountToken,
  }) async {
    launchedToken = appAccountToken;
  }

  @override
  Future<AppleProductQuery> queryProducts(Set<String> productIds) async {
    queriedProductIds = Set<String>.of(productIds);
    return productQuery;
  }

  @override
  Future<List<ApplePurchase>> restorePurchases() async => restored;

  @override
  Future<void> completePurchase(ApplePurchase purchase) async {
    events.add('completed');
    completedIds.add(purchase.transactionId);
    final error = completionError;
    if (error != null) {
      throw error;
    }
  }
}
