import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';
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

  @override
  Stream<List<PurchaseDetails>> get purchaseStream => _purchases.stream;

  @override
  Future<ProductDetailsResponse> queryProductDetails(
    Set<String> identifiers,
  ) async => productResponse;

  @override
  Future<bool> buyNonConsumable({required PurchaseParam purchaseParam}) async {
    this.purchaseParam = purchaseParam;
    return true;
  }

  void emitPurchase(PurchaseWrapper purchase) {
    _purchases.add(GooglePlayPurchaseDetails.fromPurchase(purchase));
  }

  Future<void> close() => _purchases.close();
}

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
