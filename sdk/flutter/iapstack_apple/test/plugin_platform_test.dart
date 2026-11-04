import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_apple/iapstack_apple.dart';
import 'package:in_app_purchase_platform_interface/in_app_purchase_platform_interface.dart';
import 'package:in_app_purchase_storekit/in_app_purchase_storekit.dart';
import 'package:in_app_purchase_storekit/store_kit_2_wrappers.dart';
import 'package:plugin_platform_interface/plugin_platform_interface.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late _FakeOfficialPlatform officialPlatform;
  late _FakeStoreKit2 storeKit;
  late ApplePluginPlatform bridge;

  setUp(() {
    debugDefaultTargetPlatformOverride = TargetPlatform.fuchsia;
    officialPlatform = _FakeOfficialPlatform();
    InAppPurchasePlatform.instance = officialPlatform;
    storeKit = _FakeStoreKit2()..install();
    bridge = ApplePluginPlatform();
  });

  tearDown(() async {
    storeKit.uninstall();
    await officialPlatform.close();
    debugDefaultTargetPlatformOverride = null;
  });

  test(
    'maps StoreKit 2 products and preserves checkout account token',
    () async {
      officialPlatform.productResponse = ProductDetailsResponse(
        productDetails: <ProductDetails>[
          _productDetails(
            id: 'premium_monthly',
            type: SK2ProductType.autoRenewable,
          ),
          _productDetails(
            id: 'premium_lifetime',
            type: SK2ProductType.nonConsumable,
          ),
        ],
        notFoundIDs: <String>['missing'],
      );

      final result = await bridge.queryProducts(const <String>{
        'premium_monthly',
        'premium_lifetime',
        'missing',
      });
      await bridge.launchPurchase(
        product: result.products.first,
        appAccountToken: '018f59d0-a200-7000-8000-000000000001',
      );

      expect(result.products.map((product) => product.kind), <AppleProductKind>[
        AppleProductKind.subscription,
        AppleProductKind.nonConsumable,
      ]);
      expect(result.products.first.rawPrice, 4.99);
      expect(result.products.first.currencyCode, 'USD');
      expect(result.notFoundProductIds, const <String>{'missing'});
      expect(officialPlatform.purchaseParam, isA<Sk2PurchaseParam>());
      expect(
        officialPlatform.purchaseParam?.applicationUserName,
        '018f59d0-a200-7000-8000-000000000001',
      );
    },
  );

  test('rejects non-StoreKit 2 and unsupported products', () async {
    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: <ProductDetails>[
        ProductDetails(
          id: 'legacy',
          title: 'Legacy',
          description: 'StoreKit 1',
          price: r'$1.00',
          rawPrice: 1,
          currencyCode: 'USD',
        ),
      ],
      notFoundIDs: const <String>[],
    );
    await expectLater(
      bridge.queryProducts(const <String>{'legacy'}),
      throwsA(
        isA<AppleIapStackException>().having(
          (error) => error.code,
          'code',
          'storekit_2_required',
        ),
      ),
    );

    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: <ProductDetails>[
        _productDetails(id: 'coins', type: SK2ProductType.consumable),
      ],
      notFoundIDs: const <String>[],
    );
    await expectLater(
      bridge.queryProducts(const <String>{'coins'}),
      throwsA(
        isA<AppleIapStackException>().having(
          (error) => error.code,
          'code',
          'unsupported_product_kind',
        ),
      ),
    );
  });

  test('failed refresh invalidates the previous checkout handle', () async {
    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: <ProductDetails>[
        _productDetails(
          id: 'premium_lifetime',
          type: SK2ProductType.nonConsumable,
        ),
      ],
      notFoundIDs: const <String>[],
    );
    final product = (await bridge.queryProducts(const <String>{
      'premium_lifetime',
    })).products.single;
    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: const <ProductDetails>[],
      notFoundIDs: const <String>[],
      error: IAPError(source: 'app_store', code: 'offline', message: 'offline'),
    );
    await expectLater(
      bridge.queryProducts(const <String>{'premium_lifetime'}),
      throwsA(isA<AppleIapStackException>()),
    );
    await expectLater(
      bridge.launchPurchase(
        product: product,
        appAccountToken: '018f59d0-a200-7000-8000-000000000001',
      ),
      throwsA(
        isA<AppleIapStackException>().having(
          (error) => error.code,
          'code',
          'product_not_queried',
        ),
      ),
    );
    expect(officialPlatform.purchaseParam, isNull);
  });

  test(
    'rejected mixed query never exposes a partial checkout handle',
    () async {
      officialPlatform.productResponse = ProductDetailsResponse(
        productDetails: <ProductDetails>[
          _productDetails(
            id: 'premium_lifetime',
            type: SK2ProductType.nonConsumable,
          ),
          _productDetails(id: 'coins', type: SK2ProductType.consumable),
        ],
        notFoundIDs: const <String>[],
      );
      await expectLater(
        bridge.queryProducts(const <String>{'premium_lifetime', 'coins'}),
        throwsA(
          isA<AppleIapStackException>().having(
            (error) => error.code,
            'code',
            'unsupported_product_kind',
          ),
        ),
      );
      await expectLater(
        bridge.launchPurchase(
          product: const AppleProduct(
            id: 'premium_lifetime',
            kind: AppleProductKind.nonConsumable,
            title: 'Premium',
            description: 'Premium',
            price: r'$49.99',
            rawPrice: 49.99,
            currencyCode: 'USD',
          ),
          appAccountToken: '018f59d0-a200-7000-8000-000000000001',
        ),
        throwsA(
          isA<AppleIapStackException>().having(
            (error) => error.code,
            'code',
            'product_not_queried',
          ),
        ),
      );
      expect(officialPlatform.purchaseParam, isNull);
    },
  );

  test('rejects incomplete or inconsistent product query results', () async {
    for (final response in <ProductDetailsResponse>[
      ProductDetailsResponse(
        productDetails: const <ProductDetails>[],
        notFoundIDs: const <String>[],
      ),
      ProductDetailsResponse(
        productDetails: const <ProductDetails>[],
        notFoundIDs: const <String>['unexpected'],
      ),
      ProductDetailsResponse(
        productDetails: <ProductDetails>[
          _productDetails(
            id: 'premium_lifetime',
            type: SK2ProductType.nonConsumable,
          ),
        ],
        notFoundIDs: const <String>['premium_lifetime'],
      ),
      ProductDetailsResponse(
        productDetails: <ProductDetails>[
          _productDetails(id: 'unexpected', type: SK2ProductType.nonConsumable),
        ],
        notFoundIDs: const <String>[],
      ),
      ProductDetailsResponse(
        productDetails: <ProductDetails>[
          _productDetails(
            id: 'premium_lifetime',
            type: SK2ProductType.nonConsumable,
          ),
          _productDetails(
            id: 'premium_lifetime',
            type: SK2ProductType.nonConsumable,
          ),
        ],
        notFoundIDs: const <String>[],
      ),
    ]) {
      officialPlatform.productResponse = response;
      await expectLater(
        bridge.queryProducts(const <String>{'premium_lifetime'}),
        throwsA(
          isA<AppleIapStackException>().having(
            (error) => error.code,
            'code',
            'invalid_plugin_response',
          ),
        ),
      );
    }
  });

  test('requires a cached product and a launched StoreKit flow', () async {
    const product = AppleProduct(
      id: 'premium_lifetime',
      kind: AppleProductKind.nonConsumable,
      title: 'Premium Lifetime',
      description: 'Permanent access',
      price: r'$49.99',
      rawPrice: 49.99,
      currencyCode: 'USD',
    );
    await expectLater(
      bridge.launchPurchase(
        product: product,
        appAccountToken: '018f59d0-a200-7000-8000-000000000001',
      ),
      throwsA(
        isA<AppleIapStackException>().having(
          (error) => error.code,
          'code',
          'product_not_queried',
        ),
      ),
    );

    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: <ProductDetails>[
        _productDetails(
          id: 'premium_lifetime',
          type: SK2ProductType.nonConsumable,
        ),
      ],
      notFoundIDs: const <String>[],
    );
    officialPlatform.purchaseLaunched = false;
    final query = await bridge.queryProducts(const <String>{
      'premium_lifetime',
    });
    await expectLater(
      bridge.launchPurchase(
        product: query.products.single,
        appAccountToken: '018f59d0-a200-7000-8000-000000000001',
      ),
      throwsA(
        isA<AppleIapStackException>().having(
          (error) => error.code,
          'code',
          'purchase_not_launched',
        ),
      ),
    );
  });

  test(
    'maps purchase updates and completes each cached transaction once',
    () async {
      final purchaseFuture = bridge.purchaseUpdates.first;
      final native = _purchaseDetails(
        status: PurchaseStatus.purchased,
        appAccountToken: '018F59D0-A200-7000-8000-000000000001',
      );
      officialPlatform.emitPurchase(native);

      final purchase = await purchaseFuture;
      expect(purchase.transactionId, '100');
      expect(purchase.productId, 'premium_monthly');
      expect(purchase.signedTransaction, 'header.payload.signature');
      expect(purchase.appAccountToken, '018f59d0-a200-7000-8000-000000000001');
      expect(purchase.status, ApplePurchaseStatus.purchased);
      expect(purchase.pendingCompletion, isTrue);
      expect(purchase.toString(), isNot(contains('header.payload.signature')));

      await bridge.completePurchase(purchase);
      await bridge.completePurchase(purchase);
      expect(officialPlatform.completed, <PurchaseDetails>[native]);
      // The cached handle is used once; a repeat finishes by id, a no-op.
      expect(storeKit.finished, <int>[100]);
    },
  );

  test(
    'restore marks unfinished history rows and finishes them by id',
    () async {
      storeKit
        ..unfinished = <SK2TransactionMessage>[
          _transactionMessage(id: 101, receiptData: 'header.payload.signature'),
        ]
        ..history = <SK2TransactionMessage>[
          _transactionMessage(id: 100),
          _transactionMessage(id: 101),
        ];

      final restored = await bridge.restorePurchases();

      expect(storeKit.calls, <String>[
        'restorePurchases',
        'unfinishedTransactions',
        'currentEntitlements',
        'transactions',
      ]);
      expect(restored.map((purchase) => purchase.transactionId), <String>[
        '100',
        '101',
      ]);
      expect(restored.first.pendingCompletion, isFalse);
      final unfinished = restored.last;
      expect(unfinished.status, ApplePurchaseStatus.restored);
      expect(unfinished.pendingCompletion, isTrue);
      expect(unfinished.signedTransaction, 'header.payload.signature');
      expect(
        unfinished.appAccountToken,
        '018f59d0-a200-7000-8000-000000000001',
      );
      expect(unfinished.canVerify, isTrue);

      await bridge.completePurchase(restored.first);
      await bridge.completePurchase(unfinished);
      await bridge.completePurchase(unfinished);
      // Native finish(id) does nothing for a transaction already finished.
      expect(storeKit.finished, <int>[101, 101]);
      expect(officialPlatform.completed, isEmpty);
    },
  );

  test(
    'restore signs finished current entitlements from the entitlements read',
    () async {
      storeKit
        ..unfinished = <SK2TransactionMessage>[
          _transactionMessage(id: 101, receiptData: 'unfinished.jws.101'),
        ]
        ..entitlements = <SK2TransactionMessage>[
          _transactionMessage(
            id: 100,
            originalId: 90,
            receiptData: 'entitlement.jws.100',
          ),
          _transactionMessage(id: 101, receiptData: 'entitlement.jws.101'),
          _transactionMessage(
            id: 200,
            productId: 'premium_lifetime',
            receiptData: 'entitlement.jws.200',
          ),
        ]
        ..history = <SK2TransactionMessage>[
          // An earlier renewal of 100: finished and no longer an entitlement.
          _transactionMessage(id: 90),
          _transactionMessage(id: 100, originalId: 90),
          _transactionMessage(id: 101),
        ];

      final restored = await bridge.restorePurchases();

      expect(restored.map((purchase) => purchase.transactionId), <String>[
        '90',
        '100',
        '101',
        '200',
      ]);
      final byId = <String, ApplePurchase>{
        for (final purchase in restored) purchase.transactionId: purchase,
      };
      expect(byId['90']!.signedTransaction, isEmpty);
      expect(byId['90']!.canVerify, isFalse);
      expect(byId['90']!.pendingCompletion, isFalse);
      expect(byId['100']!.signedTransaction, 'entitlement.jws.100');
      expect(byId['100']!.canVerify, isTrue);
      expect(byId['100']!.pendingCompletion, isFalse);
      expect(byId['101']!.signedTransaction, 'unfinished.jws.101');
      expect(byId['101']!.pendingCompletion, isTrue);
      // Missing from the history read, but still a current entitlement.
      expect(byId['200']!.productId, 'premium_lifetime');
      expect(byId['200']!.signedTransaction, 'entitlement.jws.200');
      expect(byId['200']!.canVerify, isTrue);
      expect(byId['200']!.pendingCompletion, isFalse);

      for (final purchase in restored) {
        await bridge.completePurchase(purchase);
      }
      expect(storeKit.finished, <int>[101]);
    },
  );

  test(
    'restore submits finished entitlements and counts unsigned history',
    () async {
      storeKit
        ..entitlements = <SK2TransactionMessage>[
          _transactionMessage(
            id: 100,
            originalId: 90,
            receiptData: 'entitlement.jws.100',
          ),
        ]
        ..history = <SK2TransactionMessage>[
          _transactionMessage(id: 90),
          _transactionMessage(id: 100, originalId: 90),
          _transactionMessage(
            id: 300,
            appAccountToken: '018F59D0-A200-7000-8000-000000000002',
          ),
        ];
      final submitted = <Object?>[];
      final apple = AppleIapStack(
        client: IapStackClient(
          IapStackConfig(
            baseUri: Uri.parse('https://iap.example'),
            applicationId: 'application-1',
            customerToken: 'customer-token',
            retryPolicy: const IapStackRetryPolicy(maxAttempts: 1),
          ),
          httpClient: MockClient((request) async {
            final body = (jsonDecode(request.body) as Map)
                .cast<String, Object?>();
            final purchases = body['purchases']! as List<Object?>;
            submitted.addAll(purchases);
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
        ),
        productKinds: const <String, AppleProductKind>{
          'premium_monthly': AppleProductKind.subscription,
        },
        platform: bridge,
      );

      final result = await apple.restorePurchases(
        externalCustomerId: '018f59d0-a200-7000-8000-000000000001',
      );

      expect(submitted, hasLength(1));
      final evidence =
          (submitted.single! as Map)['evidence']! as Map<String, Object?>;
      expect(evidence['signed_transaction'], 'entitlement.jws.100');
      expect(result.results, hasLength(1));
      expect(result.skipped.unsigned, 1);
      expect(result.skipped.differentCustomer, 1);
      expect(result.skipped.total, 2);
      expect(storeKit.finished, isEmpty);
    },
  );

  test(
    'restore keeps verified entitlements when the plugin rejects an unverified one',
    () async {
      // The plugin's restore re-emits the verified entitlements, then fails
      // because another entitlement is unverified. The reads skip that row.
      storeKit
        ..errors['restorePurchases'] = PlatformException(
          code: 'storekit2_restore_failed',
          message: 'This purchase could not be restored.',
        )
        ..entitlements = <SK2TransactionMessage>[
          _transactionMessage(id: 100, receiptData: 'entitlement.jws.100'),
        ]
        ..history = <SK2TransactionMessage>[_transactionMessage(id: 100)];

      final restored = await bridge.restorePurchases();

      expect(storeKit.calls, <String>[
        'restorePurchases',
        'unfinishedTransactions',
        'currentEntitlements',
        'transactions',
      ]);
      expect(restored.single.transactionId, '100');
      expect(restored.single.signedTransaction, 'entitlement.jws.100');
      expect(restored.single.canVerify, isTrue);
    },
  );

  test(
    'restore fails with a redacted error when a StoreKit call fails',
    () async {
      for (final method in <String>[
        'restorePurchases',
        'unfinishedTransactions',
        'currentEntitlements',
        'transactions',
      ]) {
        storeKit.errors
          ..clear()
          ..[method] = PlatformException(
            code: 'storekit_error',
            message: 'secret native detail',
          );

        await expectLater(
          bridge.restorePurchases(),
          throwsA(
            isA<AppleIapStackException>()
                .having((error) => error.code, 'code', 'apple_restore_failed')
                .having(
                  (error) => error.message,
                  'message',
                  isNot(contains('secret')),
                ),
          ),
          reason: method,
        );
      }
    },
  );

  test(
    'finishes by StoreKit id when another bridge cached the update',
    () async {
      final otherBridge = ApplePluginPlatform();
      final otherPurchaseFuture = otherBridge.purchaseUpdates.first;
      officialPlatform.emitPurchase(
        _purchaseDetails(status: PurchaseStatus.purchased, purchaseID: '4242'),
      );
      final otherPurchase = await otherPurchaseFuture;

      await bridge.completePurchase(otherPurchase);

      expect(storeKit.finished, <int>[4242]);
      expect(officialPlatform.completed, isEmpty);
    },
  );

  test(
    'fails completion when the purchase has no StoreKit id to finish',
    () async {
      final missingIdFuture = bridge.purchaseUpdates.first;
      officialPlatform.emitPurchase(
        _purchaseDetails(status: PurchaseStatus.purchased, purchaseID: null),
      );
      final missingId = await missingIdFuture;

      for (final purchase in <ApplePurchase>[
        missingId,
        const ApplePurchase(
          transactionId: 'not-a-storekit-id',
          productId: 'premium_monthly',
          signedTransaction: 'header.payload.signature',
          status: ApplePurchaseStatus.purchased,
          pendingCompletion: true,
        ),
      ]) {
        expect(purchase.pendingCompletion, isTrue);
        await expectLater(
          bridge.completePurchase(purchase),
          throwsA(
            isA<AppleIapStackException>().having(
              (error) => error.code,
              'code',
              'transaction_not_finishable',
            ),
          ),
        );
      }
      expect(officialPlatform.completed, isEmpty);
      expect(storeKit.finished, isEmpty);
    },
  );

  test('maps every StoreKit purchase status and safe error code', () async {
    final cases = <({PurchaseStatus native, ApplePurchaseStatus expected})>[
      (native: PurchaseStatus.pending, expected: ApplePurchaseStatus.pending),
      (
        native: PurchaseStatus.purchased,
        expected: ApplePurchaseStatus.purchased,
      ),
      (native: PurchaseStatus.restored, expected: ApplePurchaseStatus.restored),
      (
        native: PurchaseStatus.canceled,
        expected: ApplePurchaseStatus.cancelled,
      ),
      (native: PurchaseStatus.error, expected: ApplePurchaseStatus.failed),
    ];
    for (final testCase in cases) {
      final purchaseFuture = bridge.purchaseUpdates.first;
      final native = _purchaseDetails(status: testCase.native);
      if (testCase.native == PurchaseStatus.error) {
        native.error = IAPError(
          source: 'app_store',
          code: 'storekit_error',
          message: 'sensitive native detail',
        );
      }
      officialPlatform.emitPurchase(native);

      final purchase = await purchaseFuture;
      expect(purchase.status, testCase.expected, reason: testCase.native.name);
      expect(
        purchase.errorCode,
        testCase.native == PurchaseStatus.error ? 'storekit_error' : isNull,
      );
    }
  });

  test('rejects non-StoreKit 2 updates and redacts plugin failures', () async {
    final purchaseFuture = bridge.purchaseUpdates.first;
    officialPlatform.emitPurchase(
      PurchaseDetails(
        purchaseID: 'legacy-1',
        productID: 'legacy',
        verificationData: _verificationData,
        transactionDate: null,
        status: PurchaseStatus.purchased,
      ),
    );
    await expectLater(
      purchaseFuture,
      throwsA(
        isA<AppleIapStackException>().having(
          (error) => error.code,
          'code',
          'storekit_2_required',
        ),
      ),
    );

    officialPlatform.availabilityError = StateError('secret native detail');
    await expectLater(
      bridge.isAvailable(),
      throwsA(
        isA<AppleIapStackException>()
            .having((error) => error.code, 'code', 'apple_availability_failed')
            .having(
              (error) => error.message,
              'message',
              isNot(contains('secret')),
            ),
      ),
    );
  });

  test('preserves safe plugin error codes from product queries', () async {
    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: const <ProductDetails>[],
      notFoundIDs: const <String>[],
      error: IAPError(
        source: 'app_store',
        code: 'store_unavailable',
        message: 'sensitive native detail',
      ),
    );

    await expectLater(
      bridge.queryProducts(const <String>{'premium'}),
      throwsA(
        isA<AppleIapStackException>()
            .having((error) => error.code, 'code', 'store_unavailable')
            .having(
              (error) => error.message,
              'message',
              isNot(contains('sensitive')),
            ),
      ),
    );
  });
}

final class _FakeOfficialPlatform extends Fake
    with MockPlatformInterfaceMixin
    implements InAppPurchasePlatform {
  final StreamController<List<PurchaseDetails>> _purchases =
      StreamController<List<PurchaseDetails>>.broadcast();

  ProductDetailsResponse productResponse = ProductDetailsResponse(
    productDetails: const <ProductDetails>[],
    notFoundIDs: const <String>[],
  );
  final List<PurchaseDetails> completed = <PurchaseDetails>[];
  PurchaseParam? purchaseParam;
  Object? availabilityError;
  bool purchaseLaunched = true;

  @override
  Stream<List<PurchaseDetails>> get purchaseStream => _purchases.stream;

  @override
  Future<bool> isAvailable() async {
    final error = availabilityError;
    if (error != null) {
      throw error;
    }
    return true;
  }

  @override
  Future<ProductDetailsResponse> queryProductDetails(
    Set<String> identifiers,
  ) async => productResponse;

  @override
  Future<bool> buyNonConsumable({required PurchaseParam purchaseParam}) async {
    this.purchaseParam = purchaseParam;
    return purchaseLaunched;
  }

  @override
  Future<void> completePurchase(PurchaseDetails purchase) async {
    completed.add(purchase);
  }

  void emitPurchase(PurchaseDetails purchase) {
    _purchases.add(<PurchaseDetails>[purchase]);
  }

  Future<void> close() => _purchases.close();
}

AppStoreProduct2Details _productDetails({
  required String id,
  required SK2ProductType type,
}) => AppStoreProduct2Details.fromSK2Product(
  SK2Product(
    id: id,
    displayName: 'Premium',
    displayPrice: r'$4.99',
    description: 'Premium access',
    price: 4.99,
    type: type,
    priceLocale: SK2PriceLocale(currencyCode: 'USD', currencySymbol: r'$'),
  ),
);

SK2PurchaseDetails _purchaseDetails({
  required PurchaseStatus status,
  String? appAccountToken,
  String? purchaseID = '100',
}) => SK2PurchaseDetails(
  productID: 'premium_monthly',
  purchaseID: purchaseID,
  verificationData: _verificationData,
  transactionDate: '1787745600000',
  status: status,
  appAccountToken: appAccountToken,
);

final PurchaseVerificationData _verificationData = PurchaseVerificationData(
  localVerificationData: 'local-data',
  serverVerificationData: 'header.payload.signature',
  source: 'app_store',
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

SK2TransactionMessage _transactionMessage({
  required int id,
  String? receiptData,
  int? originalId,
  String productId = 'premium_monthly',
  String? appAccountToken = '018F59D0-A200-7000-8000-000000000001',
}) => SK2TransactionMessage(
  id: id,
  originalId: originalId ?? id,
  productId: productId,
  appAccountToken: appAccountToken,
  receiptData: receiptData,
  status: SK2PurchaseStatusMessage.purchased,
);

/// Answers the StoreKit 2 Pigeon channels the bridge calls during restore.
///
/// Like `in_app_purchase_storekit` 0.4.16, history rows carry no JWS; only the
/// unfinished and current entitlements reads do.
final class _FakeStoreKit2 {
  static const String _prefix =
      'dev.flutter.pigeon.in_app_purchase_storekit.InAppPurchase2API';
  static const List<String> _methods = <String>[
    'restorePurchases',
    'unfinishedTransactions',
    'currentEntitlements',
    'transactions',
    'finish',
  ];

  List<SK2TransactionMessage> unfinished = <SK2TransactionMessage>[];
  List<SK2TransactionMessage> entitlements = <SK2TransactionMessage>[];
  List<SK2TransactionMessage> history = <SK2TransactionMessage>[];
  // A method listed here answers with this error instead of a result.
  final Map<String, PlatformException> errors = <String, PlatformException>{};
  final List<String> calls = <String>[];
  final List<int> finished = <int>[];

  void install() {
    const codec = InAppPurchase2API.pigeonChannelCodec;
    for (final method in _methods) {
      _messenger.setMockMessageHandler('$_prefix.$method', (message) async {
        calls.add(method);
        final error = errors[method];
        if (error != null) {
          return codec.encodeMessage(wrapResponse(error: error));
        }
        Object? result;
        switch (method) {
          case 'unfinishedTransactions':
            result = unfinished;
          case 'currentEntitlements':
            result = entitlements;
          case 'transactions':
            result = history;
          case 'finish':
            final arguments = codec.decodeMessage(message)! as List<Object?>;
            finished.add(arguments.single! as int);
        }
        return codec.encodeMessage(wrapResponse(result: result));
      });
    }
  }

  void uninstall() {
    for (final method in _methods) {
      _messenger.setMockMessageHandler('$_prefix.$method', null);
    }
  }

  TestDefaultBinaryMessenger get _messenger =>
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
}
