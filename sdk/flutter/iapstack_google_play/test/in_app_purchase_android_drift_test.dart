// Drift tests for the in_app_purchase_android internals this package relies on.
//
// lib/src/plugin_platform.dart depends on two things the plugin does not
// promise across releases:
//
// * the @visibleForTesting InAppPurchaseAndroidPlatform.billingClientManager
//   field, read by product queries to get Play Billing response codes, and
// * the `BillingResponse.<name>` text the plugin puts in IAPError.message for
//   a failed purchase update, parsed back into a Billing result.
//
// These tests drive the real plugin classes, so a dependency bump that changes
// either one fails CI instead of host apps. See "Dependency on
// in_app_purchase_android" in README.md before changing the constraint.
import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';
import 'package:in_app_purchase_android/billing_client_wrappers.dart';
import 'package:in_app_purchase_android/in_app_purchase_android.dart';
import 'package:in_app_purchase_platform_interface/in_app_purchase_platform_interface.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late _FakeBillingClient billingClient;
  late PurchasesUpdatedListener onPurchasesUpdated;
  late BillingClientManager manager;
  late InAppPurchaseAndroidPlatform platform;

  setUp(() {
    // Keeps InAppPurchase.instance from registering its own Android platform.
    debugDefaultTargetPlatformOverride = TargetPlatform.fuchsia;
    billingClient = _FakeBillingClient();
    manager = BillingClientManager(
      billingClientFactory: (listener, _) {
        // The listener the plugin's BillingClient calls with every
        // onPurchasesUpdated result from Play.
        onPurchasesUpdated = listener;
        return billingClient;
      },
    );
    platform = InAppPurchaseAndroidPlatform(manager: manager);
    InAppPurchasePlatform.instance = platform;
  });

  tearDown(() {
    manager.dispose();
    debugDefaultTargetPlatformOverride = null;
  });

  group('InAppPurchaseAndroidPlatform.billingClientManager', () {
    test('registerPlatform installs the type product queries check', () async {
      final connectionErrors = <Object>[];
      // The real BillingClient cannot reach Play in a test, so its connection
      // attempt fails with a channel error after registration.
      runZonedGuarded(
        InAppPurchaseAndroidPlatform.registerPlatform,
        (error, _) => connectionErrors.add(error),
      );
      await pumpEventQueue();

      final registered = InAppPurchasePlatform.instance;
      expect(registered, isNot(same(platform)));
      expect(registered, isA<InAppPurchaseAndroidPlatform>());
      // The registered instance owns a live manager of its own.
      final BillingClientManager registeredManager =
          (registered as InAppPurchaseAndroidPlatform).billingClientManager;
      expect(registeredManager, isNot(same(manager)));
      expect(connectionErrors, everyElement(isA<PlatformException>()));
    });

    test('is the live manager the plugin queries Play with', () async {
      final BillingClientManager exposed = platform.billingClientManager;
      expect(exposed, same(manager));

      await platform.queryProductDetails(const <String>{'premium_lifetime'});
      expect(billingClient.queries, hasLength(2));

      billingClient.responseCodes[ProductType.inapp] =
          BillingResponse.serviceUnavailable;
      await expectLater(
        GooglePlayPluginPlatform().queryProducts(const <String>{
          'premium_lifetime',
        }),
        throwsA(
          isA<GooglePlayIapStackException>().having(
            (error) => error.code,
            'code',
            'billing_error_2',
          ),
        ),
      );
      // The bridge reached Play through the same manager and client.
      expect(billingClient.queries, hasLength(4));
      expect(
        billingClient.queries.skip(2).map((query) => query.productType),
        unorderedEquals(<ProductType>[ProductType.inapp, ProductType.subs]),
      );
    });
  });

  group('purchase update errors from the plugin translation', () {
    // The Android SDK's codes for each Play Billing result.
    const expectedCodes = <BillingResponse, String>{
      BillingResponse.serviceTimeout: 'billing_error_-3',
      BillingResponse.featureNotSupported: 'billing_error_-2',
      BillingResponse.serviceDisconnected: 'billing_service_disconnected',
      BillingResponse.userCanceled: 'purchase_cancelled',
      BillingResponse.serviceUnavailable: 'billing_error_2',
      BillingResponse.billingUnavailable: 'billing_error_3',
      BillingResponse.itemUnavailable: 'billing_error_4',
      BillingResponse.developerError: 'billing_error_5',
      BillingResponse.error: 'billing_error_6',
      BillingResponse.itemAlreadyOwned: 'billing_error_7',
      BillingResponse.itemNotOwned: 'billing_error_8',
      BillingResponse.networkError: 'billing_error_12',
    };

    test('cover every non-OK BillingResponse of the plugin', () {
      expect(
        expectedCodes.keys,
        unorderedEquals(
          BillingResponse.values.where(
            (response) => response != BillingResponse.ok,
          ),
        ),
        reason:
            'A new BillingResponse needs a Play Billing code in '
            'plugin_platform.dart and a row here.',
      );
    });

    test(
      'map results without purchase rows to the Android SDK codes',
      () async {
        final responses = expectedCodes.keys.toList(growable: false);
        final updates = GooglePlayPluginPlatform().purchaseUpdates
            .take(responses.length)
            .toList();

        // The bridge skips OK without purchase rows.
        onPurchasesUpdated(_purchasesResult(BillingResponse.ok));
        for (final response in responses) {
          onPurchasesUpdated(_purchasesResult(response));
        }
        final purchases = await updates;

        expect(
          purchases.map((purchase) => purchase.errorCode),
          responses.map((response) => expectedCodes[response]),
        );
        expect(
          purchases.map((purchase) => purchase.status),
          responses.map(
            (response) => response == BillingResponse.userCanceled
                ? GooglePlayPurchaseStatus.cancelled
                : GooglePlayPurchaseStatus.failed,
          ),
        );
        for (final purchase in purchases) {
          expect(purchase.purchaseToken, isEmpty);
          expect(purchase.productIds, isEmpty);
          expect(purchase.canVerify, isFalse);
        }
      },
    );

    test('map a non-OK result on a purchase row', () async {
      final update = GooglePlayPluginPlatform().purchaseUpdates.first;

      onPurchasesUpdated(
        _purchasesResult(
          BillingResponse.itemAlreadyOwned,
          purchases: const <PurchaseWrapper>[_nativePurchase],
        ),
      );
      final purchase = await update;

      expect(purchase.errorCode, 'billing_error_7');
      expect(purchase.purchaseToken, 'opaque-purchase-token');
      expect(purchase.productIds, <String>['premium_monthly']);
    });
  });

  test('pubspec caps in_app_purchase_android just above a release', () {
    final pubspec = File('pubspec.yaml').readAsStringSync();
    final constraint = RegExp(
      r'''^  in_app_purchase_android:[ \t]*["']?([^"'\n]*?)["']?[ \t]*$''',
      multiLine: true,
    ).firstMatch(pubspec)?.group(1);
    final range = RegExp(
      r'^>=\d+\.\d+\.\d+ <\d+\.\d+\.(\d+)$',
    ).firstMatch(constraint ?? '');

    // Dependabot rewrites the cap as a caret constraint (^0.5.6), and a
    // widened range ends on a minor boundary (<0.6.0). Both admit releases
    // nobody has checked yet.
    expect(
      range,
      isNotNull,
      reason:
          'Expected ">=<lowest> <next patch>", found "$constraint". After '
          'the drift tests pass, keep the lower bound and cap just above the '
          'checked release.',
    );
    expect(
      int.parse(range!.group(1)!),
      isNonZero,
      reason:
          'The upper bound "$constraint" sits on a minor or major boundary. '
          'Cap just above the checked release instead.',
    );
  });
}

/// A Play Billing purchase result as the plugin's BillingClient reports it.
PurchasesResultWrapper _purchasesResult(
  BillingResponse response, {
  List<PurchaseWrapper> purchases = const <PurchaseWrapper>[],
}) => PurchasesResultWrapper(
  responseCode: response,
  billingResult: BillingResultWrapper(
    responseCode: response,
    debugMessage: 'Billing debug message',
  ),
  purchasesList: purchases,
);

/// Billing client fake behind a real [BillingClientManager].
final class _FakeBillingClient extends Fake implements BillingClient {
  final List<({ProductType productType, Set<String> productIds})> queries =
      <({ProductType productType, Set<String> productIds})>[];
  final Map<ProductType, BillingResponse> responseCodes =
      <ProductType, BillingResponse>{};

  @override
  Future<BillingResultWrapper> startConnection({
    required OnBillingServiceDisconnected onBillingServiceDisconnected,
    BillingChoiceMode billingChoiceMode = BillingChoiceMode.playBillingOnly,
    // The plugin does not export PendingPurchasesParamsWrapper.
    Object? pendingPurchasesParams,
  }) async => const BillingResultWrapper(responseCode: BillingResponse.ok);

  @override
  Future<void> endConnection() async {}

  @override
  Future<ProductDetailsResponseWrapper> queryProductDetails({
    required List<ProductWrapper> productList,
  }) async {
    final productType = productList.first.productType;
    queries.add((
      productType: productType,
      productIds: productList.map((product) => product.productId).toSet(),
    ));
    return ProductDetailsResponseWrapper(
      billingResult: BillingResultWrapper(
        responseCode: responseCodes[productType] ?? BillingResponse.ok,
      ),
      productDetailsList: const <ProductDetailsWrapper>[],
    );
  }
}

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
