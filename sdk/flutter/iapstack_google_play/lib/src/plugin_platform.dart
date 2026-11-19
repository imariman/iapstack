import 'dart:async';

import 'package:flutter/services.dart' show PlatformException;
import 'package:iapstack_google_play/src/customer_id.dart';
import 'package:iapstack_google_play/src/errors.dart';
import 'package:iapstack_google_play/src/platform.dart';
import 'package:iapstack_google_play/src/product.dart';
import 'package:iapstack_google_play/src/product_kind.dart';
import 'package:in_app_purchase/in_app_purchase.dart';
import 'package:in_app_purchase_android/billing_client_wrappers.dart';
import 'package:in_app_purchase_android/in_app_purchase_android.dart';
import 'package:in_app_purchase_platform_interface/in_app_purchase_platform_interface.dart';

/// Production bridge to Flutter's official Google Play Billing implementation.
final class GooglePlayPluginPlatform implements GooglePlayIapPlatform {
  /// Creates a bridge around the official IAP plugin.
  ///
  /// Without [plugin], the bridge uses the process-wide plugin and fails a
  /// product query when Play Billing returns a non-OK result. An injected
  /// [plugin] answers product queries itself, so only its
  /// [ProductDetailsResponse.error] is reported.
  GooglePlayPluginPlatform({InAppPurchase? plugin})
    : _plugin = plugin ?? InAppPurchase.instance,
      _readsBillingResults = plugin == null;

  final InAppPurchase _plugin;
  final bool _readsBillingResults;
  final Map<String, ProductDetails> _queriedProducts =
      <String, ProductDetails>{};

  @override
  Stream<GooglePlayPurchase> get purchaseUpdates => _plugin.purchaseStream
      .expand((purchases) => purchases)
      // Play reports OK without purchase rows, for example after a deferred
      // subscription replacement. The Android SDK ignores it as well.
      .where(
        (details) =>
            !(_isEmptyBillingResult(details) &&
                details.status == PurchaseStatus.purchased &&
                details.error == null),
      )
      .map(_googlePlayPurchase);

  @override
  Future<bool> isAvailable() =>
      _guard(operation: 'availability', callback: _plugin.isAvailable);

  @override
  Future<GooglePlayProductQuery> queryProducts(Set<String> productIds) =>
      _guard(
        operation: 'product_query',
        callback: () async {
          _queriedProducts.removeWhere(
            (_, product) => productIds.contains(product.id),
          );
          final rawResponses = _readsBillingResults
              ? await _queryRawProductDetails(productIds)
              : null;
          if (rawResponses != null) {
            return _productQueryFromRaw(productIds, rawResponses);
          }
          final response = await _plugin.queryProductDetails(productIds);
          final error = response.error;
          if (error != null) {
            throw _pluginError(error, operation: 'product_query');
          }
          return _validatedProductQuery(
            productIds,
            response.productDetails,
            reportedMissingIds: response.notFoundIDs.toSet(),
          );
        },
      );

  /// Fails on non-OK billing codes the plugin response would silently drop.
  GooglePlayProductQuery _productQueryFromRaw(
    Set<String> productIds,
    List<ProductDetailsResponseWrapper> responses,
  ) {
    for (final response in responses) {
      final responseCode = response.billingResult.responseCode;
      if (responseCode != BillingResponse.ok) {
        throw GooglePlayIapStackException(
          code: _billingErrorCode(responseCode),
          message: 'Google Play product_query failed: ${responseCode.name}',
          userCancelled: responseCode == BillingResponse.userCanceled,
          cause: response.billingResult,
        );
      }
    }
    return _validatedProductQuery(
      productIds,
      responses
          .expand((response) => response.productDetailsList)
          .expand(GooglePlayProductDetails.fromProductDetails)
          .toList(growable: false),
    );
  }

  /// Validates one product query and caches its native checkout details.
  ///
  /// [reportedMissingIds] is the plugin's own not-found list and must account
  /// for every requested id. Without it, each requested id with no row is
  /// reported missing.
  GooglePlayProductQuery _validatedProductQuery(
    Set<String> productIds,
    List<ProductDetails> productDetails, {
    Set<String>? reportedMissingIds,
  }) {
    final products = <GooglePlayProduct>[];
    final validatedDetails = <String, ProductDetails>{};
    final foundIds = <String>{};
    for (final details in productDetails) {
      if (details is! GooglePlayProductDetails ||
          !productIds.contains(details.id)) {
        throw const GooglePlayIapStackException(
          code: 'invalid_plugin_response',
          message: 'The purchase plugin returned an unexpected product',
        );
      }
      final product = _googlePlayProduct(details);
      if (!product.isPurchasable ||
          validatedDetails.containsKey(product.selectionKey)) {
        throw const GooglePlayIapStackException(
          code: 'invalid_plugin_response',
          message: 'The purchase plugin returned an invalid product offer',
        );
      }
      validatedDetails[product.selectionKey] = details;
      foundIds.add(product.id);
      products.add(product);
    }
    final missingIds = reportedMissingIds ?? productIds.difference(foundIds);
    if (reportedMissingIds != null &&
        (!productIds.containsAll(missingIds) ||
            foundIds.intersection(missingIds).isNotEmpty ||
            !foundIds.union(missingIds).containsAll(productIds))) {
      throw const GooglePlayIapStackException(
        code: 'invalid_plugin_response',
        message: 'The purchase plugin returned an incomplete product query',
      );
    }
    _queriedProducts.addAll(validatedDetails);
    return GooglePlayProductQuery(
      products: products,
      notFoundProductIds: missingIds,
    );
  }

  @override
  Future<void> launchPurchase({
    required GooglePlayProduct product,
    required String obfuscatedAccountId,
  }) async {
    // The same check and ArgumentError as GooglePlayIapStack.launchPurchase,
    // for hosts that drive this platform directly. It runs outside _guard,
    // which would report it as a Billing failure.
    validateGooglePlayCustomerId(obfuscatedAccountId);
    return _guard(
      operation: 'purchase',
      callback: () async {
        final details = _queriedProducts[product.selectionKey];
        if (details == null) {
          throw const GooglePlayIapStackException(
            code: 'product_not_queried',
            message: 'Query this Google Play product before purchase',
          );
        }
        final launched = await _plugin.buyNonConsumable(
          purchaseParam: GooglePlayPurchaseParam(
            productDetails: details,
            applicationUserName: obfuscatedAccountId,
            offerToken: product.offerToken,
          ),
        );
        if (!launched) {
          throw const GooglePlayIapStackException(
            code: 'purchase_not_launched',
            message: 'Google Play did not launch purchase UI',
          );
        }
      },
    );
  }

  @override
  Future<List<GooglePlayPurchase>> ownedPurchases({
    required String obfuscatedAccountId,
  }) => _guard(
    operation: 'restore',
    callback: () async {
      final addition = _plugin
          .getPlatformAddition<InAppPurchaseAndroidPlatformAddition>();
      final response = await addition.queryPastPurchases(
        applicationUserName: obfuscatedAccountId,
      );
      final error = response.error;
      if (error != null) {
        throw _pluginError(error, operation: 'restore');
      }
      return response.pastPurchases
          .map(_googlePlayPurchase)
          .toList(growable: false);
    },
  );
}

/// Queries the live Billing connection for wrapper results with billing codes.
///
/// Returns null when the registered platform is not the Android plugin, so
/// callers fall back to the plugin response without billing codes.
Future<List<ProductDetailsResponseWrapper>?> _queryRawProductDetails(
  Set<String> productIds,
) async {
  final platform = InAppPurchasePlatform.instance;
  if (platform is! InAppPurchaseAndroidPlatform) {
    return null;
  }
  // The plugin exposes its live BillingClientManager only through this
  // testing-visible field; no supported route to billing codes exists.
  // pubspec.yaml caps in_app_purchase_android at the verified releases.
  // ignore: invalid_use_of_visible_for_testing_member
  final manager = platform.billingClientManager;
  List<ProductWrapper> products(ProductType productType) => productIds
      .map(
        (productId) =>
            ProductWrapper(productId: productId, productType: productType),
      )
      .toList(growable: false);
  try {
    return await Future.wait(<Future<ProductDetailsResponseWrapper>>[
      manager.runWithClient(
        (client) => client.queryProductDetails(
          productList: products(ProductType.inapp),
        ),
      ),
      manager.runWithClient(
        (client) =>
            client.queryProductDetails(productList: products(ProductType.subs)),
      ),
    ]);
  } on PlatformException catch (error) {
    // Keep the native code, as the plugin's own query reports it.
    throw _pluginError(
      IAPError(
        source: kIAPSource,
        code: error.code,
        message: error.message ?? '',
        details: error.details,
      ),
      operation: 'product_query',
    );
  }
}

/// _guard converts plugin and platform exceptions into redacted stable failures.
Future<T> _guard<T>({
  required String operation,
  required Future<T> Function() callback,
}) async {
  try {
    return await callback();
  } on GooglePlayIapStackException {
    rethrow;
  } catch (error) {
    throw GooglePlayIapStackException(
      code: 'google_play_${operation}_failed',
      message: 'Google Play $operation failed',
      cause: error,
    );
  }
}

/// _googlePlayProduct converts official plugin details into the public safe model.
GooglePlayProduct _googlePlayProduct(GooglePlayProductDetails details) {
  final native = details.productDetails;
  if (native.productType == ProductType.inapp) {
    final offer = native.oneTimePurchaseOfferDetails;
    if (offer == null) {
      throw const GooglePlayIapStackException(
        code: 'invalid_plugin_response',
        message: 'Google Play returned an incomplete one-time product',
      );
    }
    return GooglePlayProduct(
      id: details.id,
      kind: GooglePlayProductKind.nonConsumable,
      title: details.title,
      description: details.description,
      price: offer.formattedPrice,
      priceMicros: offer.priceAmountMicros,
      currencyCode: offer.priceCurrencyCode,
    );
  }
  final offers = native.subscriptionOfferDetails;
  final index = details.subscriptionIndex;
  if (native.productType != ProductType.subs ||
      offers == null ||
      index == null ||
      index < 0 ||
      index >= offers.length ||
      offers[index].pricingPhases.isEmpty) {
    throw const GooglePlayIapStackException(
      code: 'invalid_plugin_response',
      message: 'Google Play returned an incomplete subscription offer',
    );
  }
  final offer = offers[index];
  final phases = offer.pricingPhases
      .map(_googlePlayPricingPhase)
      .toList(growable: false);
  final firstPhase = phases.first;
  return GooglePlayProduct(
    id: details.id,
    kind: GooglePlayProductKind.subscription,
    title: details.title,
    description: details.description,
    price: firstPhase.formattedPrice,
    priceMicros: firstPhase.priceMicros,
    currencyCode: firstPhase.currencyCode,
    offerToken: offer.offerIdToken,
    basePlanId: offer.basePlanId,
    offerId: offer.offerId,
    offerTags: offer.offerTags,
    pricingPhases: phases,
  );
}

/// _googlePlayPricingPhase preserves exact Billing pricing without double conversion.
GooglePlayPricingPhase _googlePlayPricingPhase(PricingPhaseWrapper phase) {
  if (phase.billingCycleCount < 0 ||
      phase.billingPeriod.isEmpty ||
      phase.formattedPrice.isEmpty ||
      phase.priceAmountMicros < 0 ||
      !RegExp(r'^[A-Z]{3}$').hasMatch(phase.priceCurrencyCode)) {
    throw const GooglePlayIapStackException(
      code: 'invalid_plugin_response',
      message: 'Google Play returned an invalid subscription pricing phase',
    );
  }
  return GooglePlayPricingPhase(
    billingCycleCount: phase.billingCycleCount,
    billingPeriod: phase.billingPeriod,
    formattedPrice: phase.formattedPrice,
    priceMicros: phase.priceAmountMicros,
    currencyCode: phase.priceCurrencyCode,
    recurrence: switch (phase.recurrenceMode) {
      RecurrenceMode.finiteRecurring => GooglePlayPricingRecurrence.finite,
      RecurrenceMode.infiniteRecurring => GooglePlayPricingRecurrence.infinite,
      RecurrenceMode.nonRecurring => GooglePlayPricingRecurrence.nonRecurring,
    },
  );
}

/// _googlePlayPurchase converts one official purchase update without logging evidence.
GooglePlayPurchase _googlePlayPurchase(PurchaseDetails details) {
  if (details is! GooglePlayPurchaseDetails) {
    // The plugin emits this row when Play returns no purchases: canceled for
    // USER_CANCELED, error for other non-OK billing results.
    if (_isEmptyBillingResult(details) &&
        (details.status == PurchaseStatus.canceled ||
            details.status == PurchaseStatus.error)) {
      return GooglePlayPurchase(
        purchaseToken: '',
        productIds: const <String>[],
        status: _purchaseStatus(details.status),
        isAcknowledged: false,
        errorCode: _purchaseErrorCode(details.error),
      );
    }
    throw const GooglePlayIapStackException(
      code: 'invalid_plugin_response',
      message: 'The purchase plugin returned a non-Google purchase',
    );
  }
  final purchase = details.billingClientPurchase;
  final productIds = purchase.products.isEmpty
      ? <String>[details.productID]
      : purchase.products;
  return GooglePlayPurchase(
    purchaseToken: purchase.purchaseToken,
    productIds: productIds,
    status: _purchaseStatus(details.status),
    obfuscatedAccountId: purchase.obfuscatedAccountId,
    isAcknowledged: purchase.isAcknowledged,
    errorCode: _purchaseErrorCode(details.error),
  );
}

/// Whether [details] is the plain row the plugin builds for a Billing result
/// without purchase rows: Google Play source, no product, id, or token.
bool _isEmptyBillingResult(PurchaseDetails details) =>
    details is! GooglePlayPurchaseDetails &&
    details.verificationData.source == kIAPSource &&
    details.productID.isEmpty &&
    (details.purchaseID?.isEmpty ?? true) &&
    details.verificationData.serverVerificationData.isEmpty &&
    details.verificationData.localVerificationData.isEmpty;

/// _purchaseErrorCode recovers the Billing result of a failed purchase update.
///
/// The plugin reports every non-OK result as `purchase_error` and keeps the
/// response only as `BillingResponse.<name>` in [IAPError.message]. Other
/// plugin codes, and messages that name no known response, pass through.
String? _purchaseErrorCode(IAPError? error) {
  if (error == null || error.code != kPurchaseErrorCode) {
    return error?.code;
  }
  for (final response in BillingResponse.values) {
    if (error.message == response.toString()) {
      return _billingErrorCode(response);
    }
  }
  return error.code;
}

/// Play Billing `BillingResponseCode` values for the plugin's response enum.
const Map<BillingResponse, int> _billingResponseCodes = <BillingResponse, int>{
  BillingResponse.serviceTimeout: -3,
  BillingResponse.featureNotSupported: -2,
  BillingResponse.serviceDisconnected: -1,
  BillingResponse.ok: 0,
  BillingResponse.userCanceled: 1,
  BillingResponse.serviceUnavailable: 2,
  BillingResponse.billingUnavailable: 3,
  BillingResponse.itemUnavailable: 4,
  BillingResponse.developerError: 5,
  BillingResponse.error: 6,
  BillingResponse.itemAlreadyOwned: 7,
  BillingResponse.itemNotOwned: 8,
  BillingResponse.networkError: 12,
};

/// _billingErrorCode names a non-OK Billing result with the Android SDK codes.
String _billingErrorCode(BillingResponse response) => switch (response) {
  BillingResponse.userCanceled => 'purchase_cancelled',
  BillingResponse.serviceDisconnected => 'billing_service_disconnected',
  _ => 'billing_error_${_billingResponseCodes[response] ?? response.name}',
};

/// _purchaseStatus maps official plugin states into a stable companion contract.
GooglePlayPurchaseStatus _purchaseStatus(PurchaseStatus status) {
  switch (status) {
    case PurchaseStatus.pending:
      return GooglePlayPurchaseStatus.pending;
    case PurchaseStatus.purchased:
      return GooglePlayPurchaseStatus.purchased;
    case PurchaseStatus.restored:
      return GooglePlayPurchaseStatus.restored;
    case PurchaseStatus.canceled:
      return GooglePlayPurchaseStatus.cancelled;
    case PurchaseStatus.error:
      return GooglePlayPurchaseStatus.failed;
  }
}

/// _pluginError creates a safe failure from an official plugin error code.
GooglePlayIapStackException _pluginError(
  IAPError error, {
  required String operation,
}) => GooglePlayIapStackException(
  code: error.code.isEmpty ? 'google_play_${operation}_failed' : error.code,
  message: 'Google Play $operation failed',
  cause: error,
);
