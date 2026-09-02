import 'dart:collection';

import 'package:iapstack/iapstack.dart';
import 'package:iapstack_huawei/src/errors.dart';
import 'package:iapstack_huawei/src/evidence.dart';
import 'package:iapstack_huawei/src/platform.dart';
import 'package:iapstack_huawei/src/plugin_platform.dart';
import 'package:iapstack_huawei/src/product.dart';
import 'package:iapstack_huawei/src/product_kind.dart';

/// High-level Huawei purchase and restore flows backed by IAPStack.
final class HuaweiIapStack {
  /// Creates a Huawei flow coordinator with an injectable device bridge.
  HuaweiIapStack({
    required IapStackClient client,
    required Map<String, HuaweiProductKind> productKinds,
    HuaweiIapPlatform platform = const HuaweiPluginPlatform(),
    this.maxRestorePagesPerKind = 20,
  })  : _client = client,
        _platform = platform,
        _productKinds = Map<String, HuaweiProductKind>.unmodifiable(
          productKinds.map(
            (id, kind) => MapEntry<String, HuaweiProductKind>(id.trim(), kind),
          ),
        ) {
    if (maxRestorePagesPerKind < 1 || maxRestorePagesPerKind > 100) {
      throw ArgumentError.value(
        maxRestorePagesPerKind,
        'maxRestorePagesPerKind',
        'must be between 1 and 100',
      );
    }
    if (_productKinds.isEmpty ||
        _productKinds.keys.any((productId) => productId.isEmpty) ||
        _productKinds.length != productKinds.length) {
      throw ArgumentError.value(
        productKinds,
        'productKinds',
        'must contain unique, non-empty product IDs',
      );
    }
  }

  final IapStackClient _client;
  final HuaweiIapPlatform _platform;
  final Map<String, HuaweiProductKind> _productKinds;
  final Map<String, HuaweiProduct> _queriedProducts = <String, HuaweiProduct>{};

  /// Maximum provider pages accepted for each product kind.
  final int maxRestorePagesPerKind;

  /// Checks whether Huawei IAP is available for the current account region.
  Future<bool> isAvailable() => _platform.isAvailable();

  /// Checks whether the current Huawei account and APK can use sandbox IAP.
  Future<HuaweiSandboxStatus> sandboxStatus() => _platform.sandboxStatus();

  /// Loads configured AppGallery products and caches validated purchase choices.
  Future<HuaweiProductQuery> queryProducts(Set<String> productIds) async {
    if (productIds.isEmpty || productIds.any((id) => id.trim().isEmpty)) {
      throw ArgumentError.value(
        productIds,
        'productIds',
        'must contain at least one non-empty product ID',
      );
    }
    final normalized = productIds.map((id) => id.trim()).toSet();
    final unconfigured = normalized.difference(_productKinds.keys.toSet());
    if (unconfigured.isNotEmpty) {
      throw HuaweiIapStackException(
        code: 'unconfigured_product',
        message: 'Huawei product is not configured: ${unconfigured.first}',
      );
    }

    final found = <String, HuaweiProduct>{};
    for (final kind in HuaweiProductKind.values) {
      final ids = normalized
          .where((productId) => _productKinds[productId] == kind)
          .toList(growable: false);
      if (ids.isEmpty) {
        continue;
      }
      final products = await _platform.queryProducts(
        productIds: ids,
        productKind: kind,
      );
      for (final product in products) {
        if (!ids.contains(product.id) || product.kind != kind) {
          throw const HuaweiIapStackException(
            code: 'invalid_plugin_response',
            message: 'Huawei returned an unexpected product',
          );
        }
        if (found.containsKey(product.id)) {
          throw const HuaweiIapStackException(
            code: 'invalid_plugin_response',
            message: 'Huawei returned a duplicate product',
          );
        }
        found[product.id] = product;
      }
    }
    _queriedProducts.addAll(found);
    return HuaweiProductQuery(
      products: normalized
          .where(found.containsKey)
          .map((id) => found[id]!)
          .toList(growable: false),
      notFoundProductIds: normalized.difference(found.keys.toSet()),
    );
  }

  /// Opens Huawei purchase UI and verifies the resulting signed evidence.
  Future<VerificationResult> purchaseAndVerify({
    required String externalCustomerId,
    required HuaweiProduct product,
    String? requestId,
  }) async {
    if (externalCustomerId.trim().isEmpty) {
      throw ArgumentError.value(
        externalCustomerId,
        'externalCustomerId',
        'must not be empty',
      );
    }
    if (_queriedProducts[product.id] != product) {
      throw const HuaweiIapStackException(
        code: 'product_not_queried',
        message: 'Query and select the Huawei product before purchase',
      );
    }
    if (!product.isPurchasable) {
      throw const HuaweiIapStackException(
        code: 'product_unavailable',
        message: 'Huawei product is not available for a new purchase',
      );
    }
    final purchase = await _platform.purchase(
      productId: product.id,
      productKind: product.kind,
      developerPayload: externalCustomerId,
    );
    final evidence =
        HuaweiPurchaseEvidence.fromSignedPurchase(purchase, product.kind);
    return _client.verifyPurchase(
      evidence.toSubmission(
        externalCustomerId: externalCustomerId,
        expectedProductId: product.id,
      ),
      requestId: requestId,
    );
  }

  /// Restores all active non-consumables and subscriptions for one customer.
  Future<RestoreResult> restorePurchases({
    required String externalCustomerId,
    Set<HuaweiProductKind> productKinds = const <HuaweiProductKind>{
      HuaweiProductKind.nonConsumable,
      HuaweiProductKind.subscription,
    },
    String? requestId,
  }) async {
    if (externalCustomerId.trim().isEmpty) {
      throw ArgumentError.value(
          externalCustomerId, 'externalCustomerId', 'must not be empty');
    }
    if (productKinds.isEmpty) {
      return RestoreResult(results: const <VerificationResult>[]);
    }
    final submissions = <PurchaseSubmission>[];
    final evidenceKeys = <String>{};
    for (final productKind in productKinds) {
      String? continuationToken;
      final observedTokens = <String>{};
      var pageCount = 0;
      do {
        pageCount++;
        if (pageCount > maxRestorePagesPerKind) {
          throw const HuaweiIapStackException(
            code: 'restore_page_limit',
            message: 'Huawei restore exceeded the configured page limit',
          );
        }
        final page = await _platform.ownedPurchases(
          productKind: productKind,
          continuationToken: continuationToken,
        );
        for (final purchase in page.purchases) {
          final key =
              '${productKind.name}\u0000${purchase.signature}\u0000${purchase.purchaseData}';
          if (!evidenceKeys.add(key)) {
            continue;
          }
          final evidence =
              HuaweiPurchaseEvidence.fromSignedPurchase(purchase, productKind);
          submissions.add(
              evidence.toSubmission(externalCustomerId: externalCustomerId));
        }
        continuationToken = page.continuationToken;
        if (continuationToken != null &&
            !observedTokens.add(continuationToken)) {
          throw const HuaweiIapStackException(
            code: 'restore_pagination_cycle',
            message: 'Huawei restore returned a repeated continuation token',
          );
        }
      } while (continuationToken != null);
    }
    if (submissions.isEmpty) {
      return RestoreResult(results: const <VerificationResult>[]);
    }
    final results = <VerificationResult>[];
    for (var start = 0; start < submissions.length; start += 100) {
      final end = (start + 100).clamp(0, submissions.length);
      final batch = await _client.restorePurchases(
        submissions.sublist(start, end),
        requestId: _batchRequestId(requestId, start ~/ 100),
      );
      results.addAll(batch.results);
    }
    return RestoreResult(
        results: UnmodifiableListView<VerificationResult>(results));
  }

  /// Loads the current IAPStack projection without contacting Huawei.
  Future<EntitlementSnapshot> getEntitlements(
    String externalCustomerId, {
    String? requestId,
  }) =>
      _client.getEntitlements(externalCustomerId, requestId: requestId);
}

String? _batchRequestId(String? requestId, int batchIndex) {
  if (requestId == null || requestId.trim().isEmpty) {
    return null;
  }
  final normalized = requestId.trim();
  return batchIndex == 0 ? normalized : '$normalized-${batchIndex + 1}';
}
