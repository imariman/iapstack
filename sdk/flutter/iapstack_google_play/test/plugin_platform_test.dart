import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';
import 'package:in_app_purchase/in_app_purchase.dart';
import 'package:in_app_purchase_android/billing_client_wrappers.dart';
import 'package:in_app_purchase_android/in_app_purchase_android.dart';
import 'package:in_app_purchase_platform_interface/in_app_purchase_platform_interface.dart';
import 'package:plugin_platform_interface/plugin_platform_interface.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late _FakeOfficialPlatform officialPlatform;
  late GooglePlayPluginPlatform bridge;

  setUp(() {
    debugDefaultTargetPlatformOverride = TargetPlatform.fuchsia;
    officialPlatform = _FakeOfficialPlatform();
    InAppPurchasePlatform.instance = officialPlatform;
    bridge = GooglePlayPluginPlatform();
  });

  tearDown(() async {
    await officialPlatform.close();
    debugDefaultTargetPlatformOverride = null;
  });

  test(
    'maps subscription offers and preserves checkout account binding',
    () async {
      final nativeProduct = GooglePlayProductDetails.fromProductDetails(
        _subscriptionDetails,
      ).single;
      officialPlatform.productResponse = ProductDetailsResponse(
        productDetails: <ProductDetails>[nativeProduct],
        notFoundIDs: <String>['missing_product'],
      );

      final result = await bridge.queryProducts(<String>{
        'premium_monthly',
        'missing_product',
      });
      await bridge.launchPurchase(
        product: result.products.single,
        obfuscatedAccountId: 'opaque-customer-1',
      );

      final purchaseParam = officialPlatform.purchaseParam;
      expect(result.products.single.kind, GooglePlayProductKind.subscription);
      expect(result.products.single.offerToken, 'monthly-base-plan-token');
      expect(result.products.single.basePlanId, 'monthly');
      expect(result.products.single.offerId, isNull);
      expect(result.products.single.priceMicros, 4990000);
      expect(result.products.single.rawPrice, 4.99);
      expect(result.products.single.pricingPhases, hasLength(1));
      expect(
        result.products.single.pricingPhases.single.recurrence,
        GooglePlayPricingRecurrence.infinite,
      );
      expect(result.notFoundProductIds, <String>{'missing_product'});
      expect(purchaseParam, isA<GooglePlayPurchaseParam>());
      expect(purchaseParam?.applicationUserName, 'opaque-customer-1');
      expect(
        (purchaseParam as GooglePlayPurchaseParam).offerToken,
        'monthly-base-plan-token',
      );
    },
  );

  test('maps purchase updates without exposing the native payload', () async {
    final update = bridge.purchaseUpdates.first;
    officialPlatform.emitPurchase(_nativePurchase);

    final purchase = await update;

    expect(purchase.purchaseToken, 'opaque-purchase-token');
    expect(purchase.productIds, <String>['premium_monthly']);
    expect(purchase.status, GooglePlayPurchaseStatus.purchased);
    expect(purchase.obfuscatedAccountId, 'opaque-customer-1');
    expect(purchase.isAcknowledged, isFalse);
    expect(purchase.toString(), isNot(contains('opaque-purchase-token')));
    expect(purchase.toString(), isNot(contains('native-purchase-json')));
  });

  test(
    'maps a plain cancelled purchase sentinel and keeps the stream open',
    () async {
      final updates = bridge.purchaseUpdates.take(2).toList();
      officialPlatform.emitPurchases(<PurchaseDetails>[
        _plainPurchase(
          status: PurchaseStatus.canceled,
          response: BillingResponse.userCanceled,
        ),
      ]);
      officialPlatform.emitPurchase(_nativePurchase);

      final purchases = await updates;

      expect(purchases, hasLength(2));
      expect(purchases.first.status, GooglePlayPurchaseStatus.cancelled);
      expect(purchases.first.errorCode, 'purchase_cancelled');
      expect(purchases.first.purchaseToken, isEmpty);
      expect(purchases.first.productIds, isEmpty);
      expect(purchases.first.isAcknowledged, isFalse);
      expect(purchases.first.canVerify, isFalse);
      expect(purchases.last.status, GooglePlayPurchaseStatus.purchased);
      expect(purchases.last.purchaseToken, 'opaque-purchase-token');
    },
  );

  test(
    'maps plain error purchase sentinels to the Android SDK billing codes',
    () async {
      final updates = bridge.purchaseUpdates.take(4).toList();
      officialPlatform.emitPurchases(<PurchaseDetails>[
        _plainPurchase(
          status: PurchaseStatus.error,
          response: BillingResponse.serviceUnavailable,
        ),
        _plainPurchase(
          status: PurchaseStatus.error,
          response: BillingResponse.itemAlreadyOwned,
        ),
        _plainPurchase(
          status: PurchaseStatus.error,
          response: BillingResponse.serviceDisconnected,
        ),
      ]);
      officialPlatform.emitPurchase(_nativePurchase);

      final purchases = await updates;

      expect(
        purchases.map((purchase) => purchase.status),
        <GooglePlayPurchaseStatus>[
          GooglePlayPurchaseStatus.failed,
          GooglePlayPurchaseStatus.failed,
          GooglePlayPurchaseStatus.failed,
          GooglePlayPurchaseStatus.purchased,
        ],
      );
      expect(purchases.map((purchase) => purchase.errorCode), <String?>[
        'billing_error_2',
        'billing_error_7',
        'billing_service_disconnected',
        null,
      ]);
      expect(purchases.first.purchaseToken, isEmpty);
      expect(purchases.first.productIds, isEmpty);
      expect(purchases.first.canVerify, isFalse);
    },
  );

  test(
    'maps billing codes on purchase rows and keeps other plugin codes',
    () async {
      final updates = bridge.purchaseUpdates.take(3).toList();
      final failedRow = GooglePlayPurchaseDetails.fromPurchase(
        _nativePurchase,
      ).single;
      failedRow
        ..status = PurchaseStatus.error
        ..error = _pluginPurchaseError(BillingResponse.error.toString());
      final consumeFailure = GooglePlayPurchaseDetails.fromPurchase(
        _nativePurchase,
      ).single;
      consumeFailure
        ..status = PurchaseStatus.error
        ..error = IAPError(
          source: kIAPSource,
          code: kConsumptionFailedErrorCode,
          message: BillingResponse.error.toString(),
        );
      final unknownResponse = _plainPurchase(status: PurchaseStatus.error)
        ..error = _pluginPurchaseError('BillingResponse.somethingNew');
      officialPlatform.emitPurchases(<PurchaseDetails>[
        failedRow,
        consumeFailure,
        unknownResponse,
      ]);

      final purchases = await updates;

      expect(purchases.map((purchase) => purchase.errorCode), <String>[
        'billing_error_6',
        kConsumptionFailedErrorCode,
        kPurchaseErrorCode,
      ]);
      expect(purchases.first.purchaseToken, 'opaque-purchase-token');
      expect(purchases.first.status, GooglePlayPurchaseStatus.failed);
    },
  );

  test('skips the OK sentinel Play sends without purchase rows', () async {
    final updates = bridge.purchaseUpdates.take(1).toList();
    officialPlatform.emitPurchases(<PurchaseDetails>[
      _plainPurchase(status: PurchaseStatus.purchased),
    ]);
    officialPlatform.emitPurchase(_nativePurchase);

    final purchases = await updates;

    expect(purchases.single.status, GooglePlayPurchaseStatus.purchased);
    expect(purchases.single.purchaseToken, 'opaque-purchase-token');
  });

  test(
    'still rejects plain purchases that are not the plugin sentinel',
    () async {
      final foreignPurchases = <PurchaseDetails>[
        _plainPurchase(
          status: PurchaseStatus.purchased,
          productId: 'premium_monthly',
          token: 'foreign-token',
        ),
        _plainPurchase(
          status: PurchaseStatus.canceled,
          productId: 'premium_monthly',
          token: 'foreign-token',
        ),
        _plainPurchase(status: PurchaseStatus.error, source: 'app_store'),
      ];
      for (final details in foreignPurchases) {
        final update = bridge.purchaseUpdates.first;
        officialPlatform.emitPurchases(<PurchaseDetails>[details]);

        await expectLater(
          update,
          throwsA(
            isA<GooglePlayIapStackException>().having(
              (error) => error.code,
              'code',
              'invalid_plugin_response',
            ),
          ),
        );
      }
    },
  );

  test('preserves exact one-time product price micros', () async {
    final nativeProduct = GooglePlayProductDetails.fromProductDetails(
      _oneTimeDetails,
    ).single;
    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: <ProductDetails>[nativeProduct],
      notFoundIDs: const <String>[],
    );

    final result = await bridge.queryProducts(<String>{'premium_lifetime'});

    final product = result.products.single;
    expect(product.kind, GooglePlayProductKind.nonConsumable);
    expect(product.price, r'$49.99');
    expect(product.priceMicros, 49990000);
    expect(product.currencyCode, 'USD');
    expect(product.isPurchasable, isTrue);
    expect(product.pricingPhases, isEmpty);
  });

  test('rejects plugin errors and non-Google product details', () async {
    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: const <ProductDetails>[],
      notFoundIDs: const <String>[],
      error: IAPError(
        source: 'google_play',
        code: 'billing_unavailable',
        message: 'sensitive provider message',
      ),
    );

    await expectLater(
      bridge.queryProducts(const <String>{'premium'}),
      throwsA(
        isA<GooglePlayIapStackException>()
            .having((error) => error.code, 'code', 'billing_unavailable')
            .having(
              (error) => error.message,
              'message',
              isNot(contains('sensitive')),
            ),
      ),
    );

    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: <ProductDetails>[
        ProductDetails(
          id: 'premium',
          title: 'Wrong platform',
          description: 'Not Android',
          price: r'$1.00',
          rawPrice: 1,
          currencyCode: 'USD',
        ),
      ],
      notFoundIDs: const <String>[],
    );
    await expectLater(
      bridge.queryProducts(const <String>{'premium'}),
      throwsA(
        isA<GooglePlayIapStackException>().having(
          (error) => error.code,
          'code',
          'invalid_plugin_response',
        ),
      ),
    );
  });

  group('product queries through the Android plugin', () {
    late _FakeBillingClient billingClient;

    setUp(() {
      billingClient = _FakeBillingClient();
      InAppPurchasePlatform.instance = InAppPurchaseAndroidPlatform(
        manager: BillingClientManager(
          billingClientFactory: (_, _) => billingClient,
        ),
      );
    });

    test('queries both product types and reports missing ids', () async {
      billingClient.productDetails[ProductType.inapp] = <ProductDetailsWrapper>[
        _oneTimeDetails,
      ];
      billingClient.productDetails[ProductType.subs] = <ProductDetailsWrapper>[
        _subscriptionDetails,
      ];
      const productIds = <String>{
        'premium_lifetime',
        'premium_monthly',
        'missing_product',
      };

      final result = await GooglePlayPluginPlatform().queryProducts(productIds);

      expect(billingClient.queries, hasLength(2));
      expect(
        billingClient.queries.map((query) => query.productType),
        unorderedEquals(<ProductType>[ProductType.inapp, ProductType.subs]),
      );
      for (final query in billingClient.queries) {
        expect(query.productIds, productIds);
      }
      expect(
        result.products.map((product) => product.id),
        unorderedEquals(<String>['premium_lifetime', 'premium_monthly']),
      );
      expect(result.notFoundProductIds, <String>{'missing_product'});
    });

    test('fails with the Billing code when Play returns non-OK', () async {
      billingClient.responseCodes[ProductType.subs] =
          BillingResponse.serviceUnavailable;

      await expectLater(
        GooglePlayPluginPlatform().queryProducts(const <String>{
          'premium_monthly',
        }),
        throwsA(
          isA<GooglePlayIapStackException>()
              .having((error) => error.code, 'code', 'billing_error_2')
              .having(
                (error) => error.message,
                'message',
                contains('serviceUnavailable'),
              ),
        ),
      );
    });

    test('fails ITEM_UNAVAILABLE like the Android SDK', () async {
      billingClient.responseCodes[ProductType.inapp] =
          BillingResponse.itemUnavailable;
      billingClient.productDetails[ProductType.subs] = <ProductDetailsWrapper>[
        _subscriptionDetails,
      ];

      await expectLater(
        GooglePlayPluginPlatform().queryProducts(const <String>{
          'premium_lifetime',
          'premium_monthly',
        }),
        throwsA(
          isA<GooglePlayIapStackException>().having(
            (error) => error.code,
            'code',
            'billing_error_4',
          ),
        ),
      );
    });

    test('keeps the native code of a channel failure', () async {
      billingClient.queryError = PlatformException(
        code: 'some_native_code',
        message: 'sensitive native message',
      );

      await expectLater(
        GooglePlayPluginPlatform().queryProducts(const <String>{
          'premium_lifetime',
        }),
        throwsA(
          isA<GooglePlayIapStackException>()
              .having((error) => error.code, 'code', 'some_native_code')
              .having(
                (error) => error.message,
                'message',
                isNot(contains('sensitive')),
              ),
        ),
      );
    });

    test('an injected plugin answers product queries itself', () async {
      billingClient.responseCodes[ProductType.inapp] =
          BillingResponse.serviceUnavailable;
      final plugin = _FakeInAppPurchase(
        ProductDetailsResponse(
          productDetails: GooglePlayProductDetails.fromProductDetails(
            _oneTimeDetails,
          ),
          notFoundIDs: const <String>[],
        ),
      );

      final result = await GooglePlayPluginPlatform(
        plugin: plugin,
      ).queryProducts(const <String>{'premium_lifetime'});

      expect(result.products.single.id, 'premium_lifetime');
      expect(billingClient.queries, isEmpty);
    });
  });

  test('failed refresh invalidates cached native choices', () async {
    final nativeProduct = GooglePlayProductDetails.fromProductDetails(
      _oneTimeDetails,
    ).single;
    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: <ProductDetails>[nativeProduct],
      notFoundIDs: const <String>[],
    );
    final product = (await bridge.queryProducts(<String>{
      'premium_lifetime',
    })).products.single;
    officialPlatform.productResponse = ProductDetailsResponse(
      productDetails: const <ProductDetails>[],
      notFoundIDs: const <String>[],
      error: IAPError(source: 'google_play', code: 'query_failed', message: ''),
    );
    await expectLater(
      bridge.queryProducts(<String>{'premium_lifetime'}),
      throwsA(isA<GooglePlayIapStackException>()),
    );
    await expectLater(
      bridge.launchPurchase(
        product: product,
        obfuscatedAccountId: 'customer-1',
      ),
      throwsA(
        isA<GooglePlayIapStackException>().having(
          (error) => error.code,
          'code',
          'product_not_queried',
        ),
      ),
    );
    expect(officialPlatform.purchaseParam, isNull);
  });

  test(
    'malformed product responses never cache partially validated offers',
    () async {
      final nativeProduct = GooglePlayProductDetails.fromProductDetails(
        _oneTimeDetails,
      ).single;
      officialPlatform.productResponse = ProductDetailsResponse(
        productDetails: <ProductDetails>[nativeProduct],
        notFoundIDs: const <String>[],
      );
      final product = (await bridge.queryProducts(<String>{
        'premium_lifetime',
      })).products.single;
      final malformedResponses = <ProductDetailsResponse>[
        ProductDetailsResponse(
          productDetails: <ProductDetails>[nativeProduct, nativeProduct],
          notFoundIDs: const <String>['premium_monthly'],
        ),
        ProductDetailsResponse(
          productDetails: <ProductDetails>[nativeProduct],
          notFoundIDs: const <String>[],
        ),
        ProductDetailsResponse(
          productDetails: <ProductDetails>[nativeProduct],
          notFoundIDs: const <String>['premium_lifetime', 'premium_monthly'],
        ),
        ProductDetailsResponse(
          productDetails: <ProductDetails>[nativeProduct],
          notFoundIDs: const <String>['unexpected_product', 'premium_monthly'],
        ),
        ProductDetailsResponse(
          productDetails: <ProductDetails>[
            nativeProduct,
            ProductDetails(
              id: 'premium_monthly',
              title: 'Wrong platform',
              description: '',
              price: r'$1.00',
              rawPrice: 1,
              currencyCode: 'USD',
            ),
          ],
          notFoundIDs: const <String>[],
        ),
      ];
      for (final response in malformedResponses) {
        officialPlatform.productResponse = response;
        await expectLater(
          bridge.queryProducts(<String>{'premium_lifetime', 'premium_monthly'}),
          throwsA(
            isA<GooglePlayIapStackException>().having(
              (error) => error.code,
              'code',
              'invalid_plugin_response',
            ),
          ),
        );
        await expectLater(
          bridge.launchPurchase(
            product: product,
            obfuscatedAccountId: 'customer-1',
          ),
          throwsA(
            isA<GooglePlayIapStackException>().having(
              (error) => error.code,
              'code',
              'product_not_queried',
            ),
          ),
        );
        expect(officialPlatform.purchaseParam, isNull);
      }
    },
  );

  test(
    'requires a native cached product and a launched Billing flow',
    () async {
      final product = GooglePlayProduct(
        id: 'premium_lifetime',
        kind: GooglePlayProductKind.nonConsumable,
        title: 'Premium Lifetime',
        description: 'Permanent access',
        price: r'$49.99',
        priceMicros: 49990000,
        currencyCode: 'USD',
      );
      await expectLater(
        bridge.launchPurchase(
          product: product,
          obfuscatedAccountId: 'customer-1',
        ),
        throwsA(
          isA<GooglePlayIapStackException>().having(
            (error) => error.code,
            'code',
            'product_not_queried',
          ),
        ),
      );

      final nativeProduct = GooglePlayProductDetails.fromProductDetails(
        _oneTimeDetails,
      ).single;
      officialPlatform.productResponse = ProductDetailsResponse(
        productDetails: <ProductDetails>[nativeProduct],
        notFoundIDs: const <String>[],
      );
      officialPlatform.purchaseLaunched = false;
      final query = await bridge.queryProducts(const <String>{
        'premium_lifetime',
      });
      await expectLater(
        bridge.launchPurchase(
          product: query.products.single,
          obfuscatedAccountId: 'customer-1',
        ),
        throwsA(
          isA<GooglePlayIapStackException>().having(
            (error) => error.code,
            'code',
            'purchase_not_launched',
          ),
        ),
      );
    },
  );

  test('redacts unexpected platform exceptions', () async {
    officialPlatform.availabilityError = StateError('secret native details');

    await expectLater(
      bridge.isAvailable(),
      throwsA(
        isA<GooglePlayIapStackException>()
            .having(
              (error) => error.code,
              'code',
              'google_play_availability_failed',
            )
            .having(
              (error) => error.message,
              'message',
              isNot(contains('secret')),
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

  void emitPurchase(PurchaseWrapper purchase) {
    _purchases.add(GooglePlayPurchaseDetails.fromPurchase(purchase));
  }

  void emitPurchases(List<PurchaseDetails> purchases) {
    _purchases.add(purchases);
  }

  Future<void> close() => _purchases.close();
}

/// Billing client fake behind a real [BillingClientManager].
final class _FakeBillingClient extends Fake implements BillingClient {
  final List<({ProductType productType, Set<String> productIds})> queries =
      <({ProductType productType, Set<String> productIds})>[];
  final Map<ProductType, BillingResponse> responseCodes =
      <ProductType, BillingResponse>{};
  final Map<ProductType, List<ProductDetailsWrapper>> productDetails =
      <ProductType, List<ProductDetailsWrapper>>{};
  Object? queryError;

  @override
  Future<BillingResultWrapper> startConnection({
    required OnBillingServiceDisconnected onBillingServiceDisconnected,
    BillingChoiceMode billingChoiceMode = BillingChoiceMode.playBillingOnly,
    // The plugin does not export PendingPurchasesParamsWrapper.
    Object? pendingPurchasesParams,
  }) async => const BillingResultWrapper(responseCode: BillingResponse.ok);

  @override
  Future<ProductDetailsResponseWrapper> queryProductDetails({
    required List<ProductWrapper> productList,
  }) async {
    final productType = productList.first.productType;
    queries.add((
      productType: productType,
      productIds: productList.map((product) => product.productId).toSet(),
    ));
    final error = queryError;
    if (error != null) {
      throw error;
    }
    return ProductDetailsResponseWrapper(
      billingResult: BillingResultWrapper(
        responseCode: responseCodes[productType] ?? BillingResponse.ok,
      ),
      productDetailsList:
          productDetails[productType] ?? const <ProductDetailsWrapper>[],
    );
  }
}

/// Host-injected plugin that answers product queries itself.
final class _FakeInAppPurchase extends Fake implements InAppPurchase {
  _FakeInAppPurchase(this.productResponse);

  final ProductDetailsResponse productResponse;

  @override
  Future<ProductDetailsResponse> queryProductDetails(
    Set<String> identifiers,
  ) async => productResponse;
}

/// Builds a plain purchase. The defaults match the row the plugin emits when
/// Play returns no purchases (`_getPurchaseDetailsFromResult`).
PurchaseDetails _plainPurchase({
  required PurchaseStatus status,
  BillingResponse? response,
  String productId = '',
  String token = '',
  String source = kIAPSource,
}) {
  final details = PurchaseDetails(
    purchaseID: '',
    productID: productId,
    status: status,
    transactionDate: null,
    verificationData: PurchaseVerificationData(
      localVerificationData: '',
      serverVerificationData: token,
      source: source,
    ),
  );
  if (response != null) {
    details.error = _pluginPurchaseError(response.toString());
  }
  return details;
}

/// The error the plugin attaches to every non-OK purchase update.
IAPError _pluginPurchaseError(String message) => IAPError(
  source: kIAPSource,
  code: kPurchaseErrorCode,
  message: message,
  details: 'Billing debug message',
);

const ProductDetailsWrapper _subscriptionDetails = ProductDetailsWrapper(
  description: 'Premium access',
  name: 'Premium Monthly',
  productId: 'premium_monthly',
  productType: ProductType.subs,
  subscriptionOfferDetails: <SubscriptionOfferDetailsWrapper>[
    SubscriptionOfferDetailsWrapper(
      basePlanId: 'monthly',
      offerTags: <String>[],
      offerIdToken: 'monthly-base-plan-token',
      pricingPhases: <PricingPhaseWrapper>[
        PricingPhaseWrapper(
          billingCycleCount: 0,
          billingPeriod: 'P1M',
          formattedPrice: r'$4.99',
          priceAmountMicros: 4990000,
          priceCurrencyCode: 'USD',
          recurrenceMode: RecurrenceMode.infiniteRecurring,
        ),
      ],
    ),
  ],
  title: 'Premium Monthly (IAPStack)',
);

const ProductDetailsWrapper _oneTimeDetails = ProductDetailsWrapper(
  description: 'Permanent premium access',
  name: 'Premium Lifetime',
  productId: 'premium_lifetime',
  productType: ProductType.inapp,
  oneTimePurchaseOfferDetails: OneTimePurchaseOfferDetailsWrapper(
    formattedPrice: r'$49.99',
    priceAmountMicros: 49990000,
    priceCurrencyCode: 'USD',
  ),
  title: 'Premium Lifetime (IAPStack)',
);

const PurchaseWrapper _nativePurchase = PurchaseWrapper(
  orderId: 'order-id',
  packageName: 'com.example.application',
  purchaseTime: 1787745600000,
  purchaseToken: 'opaque-purchase-token',
  signature: 'native-signature',
  products: <String>['premium_monthly'],
  isAutoRenewing: true,
  originalJson: 'native-purchase-json',
  isAcknowledged: false,
  purchaseState: PurchaseStateWrapper.purchased,
  obfuscatedAccountId: 'opaque-customer-1',
);
