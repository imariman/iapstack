import 'dart:collection';

import 'package:iapstack/iapstack.dart';
import 'package:iapstack_google_play/src/errors.dart';
import 'package:iapstack_google_play/src/evidence.dart';
import 'package:iapstack_google_play/src/platform.dart';
import 'package:iapstack_google_play/src/plugin_platform.dart';
import 'package:iapstack_google_play/src/product.dart';
import 'package:iapstack_google_play/src/product_kind.dart';

/// High-level Google Play purchase and restore flows backed by IAPStack.
final class GooglePlayIapStack {
  /// Creates a Google Play coordinator with an explicit provider product catalog.
  GooglePlayIapStack({
    required IapStackClient client,
    required Map<String, GooglePlayProductKind> productKinds,
    GooglePlayIapPlatform? platform,
  }) : _client = client,
       _productKinds = UnmodifiableMapView<String, GooglePlayProductKind>(
         productKinds.map(
           (id, kind) =>
               MapEntry<String, GooglePlayProductKind>(id.trim(), kind),
         ),
       ),
       _platform = platform ?? GooglePlayPluginPlatform() {
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
  final Map<String, GooglePlayProductKind> _productKinds;
  final GooglePlayIapPlatform _platform;
  final Map<String, GooglePlayProduct> _queriedProducts =
      <String, GooglePlayProduct>{};

  /// Emits Play Billing changes that the host should handle from app startup.
  Stream<GooglePlayPurchase> get purchaseUpdates => _platform.purchaseUpdates;

  /// Reports whether Google Play Billing is available for the installed build.
  Future<bool> isAvailable() => _platform.isAvailable();

  /// Queries localized details and validates provider kinds against local catalog configuration.
  Future<GooglePlayProductQuery> queryProducts(Set<String> productIds) async {
    if (productIds.isEmpty ||
        productIds.any((productId) => productId.trim().isEmpty)) {
      throw ArgumentError.value(
        productIds,
        'productIds',
        'must contain at least one non-empty product ID',
      );
    }
    final normalized = productIds.map((id) => id.trim()).toSet();
    final unconfigured = normalized.difference(_productKinds.keys.toSet());
    if (unconfigured.isNotEmpty) {
      throw GooglePlayIapStackException(
        code: 'unconfigured_product',
        message: 'Google Play product is not configured: ${unconfigured.first}',
      );
    }
    _queriedProducts.removeWhere(
      (_, product) => normalized.contains(product.id),
    );
    final result = await _platform.queryProducts(normalized);
    if (!normalized.containsAll(result.notFoundProductIds)) {
      throw const GooglePlayIapStackException(
        code: 'invalid_plugin_response',
        message: 'Google Play reported an unexpected missing product',
      );
    }
    final foundIds = <String>{};
    final foundSelections = <String>{};
    final validatedProducts = <String, GooglePlayProduct>{};
    for (final product in result.products) {
      final expectedKind = _productKinds[product.id];
      if (!normalized.contains(product.id) ||
          expectedKind == null ||
          expectedKind != product.kind ||
          !product.isPurchasable) {
        throw const GooglePlayIapStackException(
          code: 'invalid_plugin_response',
          message: 'Google Play returned an unexpected product offer',
        );
      }
      if (!foundSelections.add(product.selectionKey)) {
        throw const GooglePlayIapStackException(
          code: 'invalid_plugin_response',
          message: 'Google Play returned a duplicate product offer',
        );
      }
      foundIds.add(product.id);
      validatedProducts[product.selectionKey] = product;
    }
    if (foundIds.intersection(result.notFoundProductIds).isNotEmpty ||
        !foundIds.union(result.notFoundProductIds).containsAll(normalized)) {
      throw const GooglePlayIapStackException(
        code: 'invalid_plugin_response',
        message: 'Google Play product query was incomplete',
      );
    }
    _queriedProducts.addAll(validatedProducts);
    return GooglePlayProductQuery(
      products: result.products,
      notFoundProductIds: result.notFoundProductIds,
    );
  }

  /// Opens Play Billing UI with the customer binding required by server verification.
  Future<void> launchPurchase({
    required String externalCustomerId,
    required GooglePlayProduct product,
  }) async {
    _validateExternalCustomerId(externalCustomerId);
    final expectedKind = _productKinds[product.id];
    if (expectedKind == null || expectedKind != product.kind) {
      throw const GooglePlayIapStackException(
        code: 'product_kind_mismatch',
        message: 'Google Play product does not match the configured catalog',
      );
    }
    if (_queriedProducts[product.selectionKey] != product) {
      throw const GooglePlayIapStackException(
        code: 'product_not_queried',
        message: 'Query and select the Google Play offer before purchase',
      );
    }
    if (!product.isPurchasable) {
      throw const GooglePlayIapStackException(
        code: 'product_unavailable',
        message: 'Google Play product offer cannot start a purchase',
      );
    }
    await _platform.launchPurchase(
      product: product,
      obfuscatedAccountId: externalCustomerId,
    );
  }

  /// Verifies one pending, completed, or restored purchase with authoritative server state.
  Future<VerificationResult> verifyPurchase({
    required String externalCustomerId,
    required GooglePlayPurchase purchase,
    String? requestId,
  }) async {
    final submission = _submission(
      externalCustomerId: externalCustomerId,
      purchase: purchase,
    );
    return _client.verifyPurchase(submission, requestId: requestId);
  }

  /// Restores currently owned completed purchases in bounded IAPStack batches.
  Future<RestoreResult> restorePurchases({
    required String externalCustomerId,
    String? requestId,
  }) async {
    _validateExternalCustomerId(externalCustomerId);
    final purchases = await _platform.ownedPurchases(
      obfuscatedAccountId: externalCustomerId,
    );
    final submissions = <PurchaseSubmission>[];
    final observedTokens = <String>{};
    for (final purchase in purchases) {
      if (purchase.status != GooglePlayPurchaseStatus.purchased &&
          purchase.status != GooglePlayPurchaseStatus.restored) {
        continue;
      }
      if (!observedTokens.add(purchase.purchaseToken)) {
        continue;
      }
      submissions.add(
        _submission(externalCustomerId: externalCustomerId, purchase: purchase),
      );
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
    return RestoreResult(results: results);
  }

  /// Loads the current IAPStack projection without contacting Google Play.
  Future<EntitlementSnapshot> getEntitlements(
    String externalCustomerId, {
    String? requestId,
  }) => _client.getEntitlements(externalCustomerId, requestId: requestId);

  /// _submission validates provider scope and creates one opaque backend submission.
  PurchaseSubmission _submission({
    required String externalCustomerId,
    required GooglePlayPurchase purchase,
  }) {
    _validateExternalCustomerId(externalCustomerId);
    if (!purchase.canVerify) {
      throw GooglePlayIapStackException(
        code: purchase.status == GooglePlayPurchaseStatus.cancelled
            ? 'purchase_cancelled'
            : 'purchase_failed',
        message: purchase.status == GooglePlayPurchaseStatus.cancelled
            ? 'Google Play purchase was cancelled'
            : 'Google Play purchase cannot be verified',
        userCancelled: purchase.status == GooglePlayPurchaseStatus.cancelled,
      );
    }
    if (purchase.obfuscatedAccountId != externalCustomerId) {
      throw const GooglePlayIapStackException(
        code: 'customer_binding_mismatch',
        message: 'Google Play purchase does not match the current customer',
      );
    }
    if (purchase.productIds.length != 1) {
      throw const GooglePlayIapStackException(
        code: 'unsupported_multi_product_purchase',
        message:
            'IAPStack currently requires one Google Play product per purchase',
      );
    }
    final productId = purchase.productIds.single;
    final productKind = _productKinds[productId];
    if (productKind == null) {
      throw const GooglePlayIapStackException(
        code: 'unknown_product',
        message: 'Google Play purchase product is not configured',
      );
    }
    return GooglePlayPurchaseEvidence(
      purchaseToken: purchase.purchaseToken,
      productKind: productKind,
    ).toSubmission(
      externalCustomerId: externalCustomerId,
      productId: productId,
    );
  }
}

/// _validateExternalCustomerId enforces an exact non-empty obfuscated account identifier.
void _validateExternalCustomerId(String externalCustomerId) {
  if (externalCustomerId.trim().isEmpty ||
      externalCustomerId.trim() != externalCustomerId ||
      externalCustomerId.length > 64) {
    throw ArgumentError.value(
      externalCustomerId,
      'externalCustomerId',
      'must be non-empty, at most 64 characters, and have no surrounding whitespace',
    );
  }
}

/// _batchRequestId creates stable suffixes for bounded restore API calls.
String? _batchRequestId(String? requestId, int batchIndex) {
  if (requestId == null || requestId.trim().isEmpty) {
    return null;
  }
  final normalized = requestId.trim();
  return batchIndex == 0 ? normalized : '$normalized-${batchIndex + 1}';
}
