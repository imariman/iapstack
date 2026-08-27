import 'dart:collection';

import 'package:iapstack/iapstack.dart';
import 'package:iapstack_apple/src/errors.dart';
import 'package:iapstack_apple/src/evidence.dart';
import 'package:iapstack_apple/src/platform.dart';
import 'package:iapstack_apple/src/plugin_platform.dart';
import 'package:iapstack_apple/src/product_kind.dart';

/// High-level StoreKit 2 purchase and restore flows backed by IAPStack.
final class AppleIapStack {
  /// Creates an Apple coordinator with an explicit provider product catalog.
  AppleIapStack({
    required IapStackClient client,
    required Map<String, AppleProductKind> productKinds,
    AppleIapPlatform? platform,
  }) : _client = client,
       _productKinds = UnmodifiableMapView<String, AppleProductKind>(
         Map<String, AppleProductKind>.of(productKinds),
       ),
       _platform = platform ?? ApplePluginPlatform() {
    for (final productId in _productKinds.keys) {
      if (productId.trim().isEmpty) {
        throw ArgumentError.value(
          productKinds,
          'productKinds',
          'must not contain an empty product ID',
        );
      }
    }
  }

  final IapStackClient _client;
  final Map<String, AppleProductKind> _productKinds;
  final AppleIapPlatform _platform;

  /// Emits StoreKit 2 changes that the host should handle from app startup.
  Stream<ApplePurchase> get purchaseUpdates => _platform.purchaseUpdates;

  /// Reports whether StoreKit purchases are available for the installed build.
  Future<bool> isAvailable() => _platform.isAvailable();

  /// Queries localized details and validates provider kinds against local configuration.
  Future<AppleProductQuery> queryProducts(Set<String> productIds) async {
    if (productIds.any(
      (productId) =>
          productId.trim().isEmpty || !_productKinds.containsKey(productId),
    )) {
      throw ArgumentError.value(
        productIds,
        'productIds',
        'must contain only configured non-empty product IDs',
      );
    }
    if (productIds.isEmpty) {
      return AppleProductQuery(
        products: const <AppleProduct>[],
        notFoundProductIds: const <String>{},
      );
    }
    final result = await _platform.queryProducts(productIds);
    for (final product in result.products) {
      final expectedKind = _productKinds[product.id];
      if (expectedKind == null || expectedKind != product.kind) {
        throw AppleIapStackException(
          code: 'product_kind_mismatch',
          message: 'App Store product ${product.id} has an unexpected kind',
        );
      }
    }
    return result;
  }

  /// Opens StoreKit UI with the UUID customer binding required by server verification.
  Future<void> launchPurchase({
    required String externalCustomerId,
    required AppleProduct product,
  }) async {
    _validateAppAccountToken(externalCustomerId);
    final expectedKind = _productKinds[product.id];
    if (expectedKind == null || expectedKind != product.kind) {
      throw const AppleIapStackException(
        code: 'product_kind_mismatch',
        message: 'App Store product does not match the configured catalog',
      );
    }
    await _platform.launchPurchase(
      product: product,
      appAccountToken: externalCustomerId,
    );
  }

  /// Verifies one completed transaction, then finishes it only after server success.
  Future<VerificationResult> verifyPurchase({
    required String externalCustomerId,
    required ApplePurchase purchase,
    String? requestId,
  }) async {
    final submission = _submission(
      externalCustomerId: externalCustomerId,
      purchase: purchase,
    );
    final result = await _client.verifyPurchase(
      submission,
      requestId: requestId,
    );
    if (purchase.pendingCompletion) {
      await _platform.completePurchase(purchase);
    }
    return result;
  }

  /// Restores history and verifies each unique signed StoreKit transaction.
  Future<RestoreResult> restorePurchases({
    required String externalCustomerId,
    String? requestId,
  }) async {
    _validateAppAccountToken(externalCustomerId);
    final purchases = await _platform.restorePurchases();
    final submissions = <PurchaseSubmission>[];
    final observedTransactions = <String>{};
    for (final purchase in purchases) {
      if (!purchase.canVerify ||
          !observedTransactions.add(purchase.transactionId)) {
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

  /// Loads the current IAPStack projection without contacting StoreKit.
  Future<EntitlementSnapshot> getEntitlements(
    String externalCustomerId, {
    String? requestId,
  }) => _client.getEntitlements(externalCustomerId, requestId: requestId);

  /// _submission validates provider scope and creates one opaque backend submission.
  PurchaseSubmission _submission({
    required String externalCustomerId,
    required ApplePurchase purchase,
  }) {
    _validateAppAccountToken(externalCustomerId);
    if (!purchase.canVerify) {
      throw AppleIapStackException(
        code: purchase.status == ApplePurchaseStatus.cancelled
            ? 'purchase_cancelled'
            : 'purchase_not_verifiable',
        message: purchase.status == ApplePurchaseStatus.cancelled
            ? 'The App Store purchase was cancelled'
            : 'The App Store purchase cannot be verified yet',
        userCancelled: purchase.status == ApplePurchaseStatus.cancelled,
      );
    }
    if (purchase.appAccountToken?.toLowerCase() != externalCustomerId) {
      throw const AppleIapStackException(
        code: 'customer_binding_mismatch',
        message:
            'The App Store transaction does not match the current customer',
      );
    }
    final productKind = _productKinds[purchase.productId];
    if (productKind == null) {
      throw const AppleIapStackException(
        code: 'unknown_product',
        message: 'The App Store transaction product is not configured',
      );
    }
    return ApplePurchaseEvidence(
      signedTransaction: purchase.signedTransaction,
      productKind: productKind,
    ).toSubmission(
      externalCustomerId: externalCustomerId,
      productId: purchase.productId,
    );
  }
}

/// _validateAppAccountToken requires a canonical lowercase RFC 4122 UUID.
void _validateAppAccountToken(String externalCustomerId) {
  final canonicalUUID = RegExp(
    r'^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$',
  );
  if (!canonicalUUID.hasMatch(externalCustomerId)) {
    throw ArgumentError.value(
      externalCustomerId,
      'externalCustomerId',
      'must be a canonical lowercase RFC 4122 UUID for appAccountToken',
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
