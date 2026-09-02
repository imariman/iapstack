import 'dart:async';

import 'package:iapstack_google_play/src/errors.dart';
import 'package:iapstack_google_play/src/platform.dart';
import 'package:iapstack_google_play/src/product.dart';
import 'package:iapstack_google_play/src/product_kind.dart';
import 'package:in_app_purchase/in_app_purchase.dart';
import 'package:in_app_purchase_android/billing_client_wrappers.dart';
import 'package:in_app_purchase_android/in_app_purchase_android.dart';

/// Production bridge to Flutter's official Google Play Billing implementation.
final class GooglePlayPluginPlatform implements GooglePlayIapPlatform {
  /// Creates a bridge around the process-wide official IAP plugin instance.
  GooglePlayPluginPlatform({InAppPurchase? plugin})
    : _plugin = plugin ?? InAppPurchase.instance;

  final InAppPurchase _plugin;
  final Map<String, ProductDetails> _queriedProducts =
      <String, ProductDetails>{};

  @override
  Stream<GooglePlayPurchase> get purchaseUpdates => _plugin.purchaseStream
      .expand((purchases) => purchases)
      .map(_googlePlayPurchase);

  @override
  Future<bool> isAvailable() =>
      _guard(operation: 'availability', callback: _plugin.isAvailable);

  @override
  Future<GooglePlayProductQuery> queryProducts(Set<String> productIds) =>
      _guard(
        operation: 'product_query',
        callback: () async {
          final response = await _plugin.queryProductDetails(productIds);
          final error = response.error;
          if (error != null) {
            throw _pluginError(error, operation: 'product_query');
          }
          _queriedProducts.removeWhere(
            (_, product) => productIds.contains(product.id),
          );
          final products = <GooglePlayProduct>[];
          for (final details in response.productDetails) {
            if (details is! GooglePlayProductDetails) {
              throw const GooglePlayIapStackException(
                code: 'invalid_plugin_response',
                message: 'The purchase plugin returned a non-Google product',
              );
            }
            final product = _googlePlayProduct(details);
            _queriedProducts[product.selectionKey] = details;
            products.add(product);
          }
          return GooglePlayProductQuery(
            products: products,
            notFoundProductIds: response.notFoundIDs.toSet(),
          );
        },
      );

  @override
  Future<void> launchPurchase({
    required GooglePlayProduct product,
    required String obfuscatedAccountId,
  }) => _guard(
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
    errorCode: details.error?.code,
  );
}

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
