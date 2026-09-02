import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_apple/iapstack_apple.dart';
import 'package:in_app_purchase_platform_interface/in_app_purchase_platform_interface.dart';
import 'package:in_app_purchase_storekit/in_app_purchase_storekit.dart';
import 'package:in_app_purchase_storekit/store_kit_2_wrappers.dart';
import 'package:plugin_platform_interface/plugin_platform_interface.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late _FakeOfficialPlatform officialPlatform;
  late ApplePluginPlatform bridge;

  setUp(() {
    debugDefaultTargetPlatformOverride = TargetPlatform.fuchsia;
    officialPlatform = _FakeOfficialPlatform();
    InAppPurchasePlatform.instance = officialPlatform;
    bridge = ApplePluginPlatform();
  });

  tearDown(() async {
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
      expect(purchase.transactionId, 'transaction-100');
      expect(purchase.productId, 'premium_monthly');
      expect(purchase.signedTransaction, 'header.payload.signature');
      expect(purchase.appAccountToken, '018f59d0-a200-7000-8000-000000000001');
      expect(purchase.status, ApplePurchaseStatus.purchased);
      expect(purchase.pendingCompletion, isTrue);
      expect(purchase.toString(), isNot(contains('header.payload.signature')));

      await bridge.completePurchase(purchase);
      await bridge.completePurchase(purchase);
      expect(officialPlatform.completed, <PurchaseDetails>[native]);
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
}) => SK2PurchaseDetails(
  productID: 'premium_monthly',
  purchaseID: 'transaction-100',
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
