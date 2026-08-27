import 'package:iapstack_apple/src/errors.dart';
import 'package:iapstack_apple/src/platform.dart';
import 'package:iapstack_apple/src/product_kind.dart';
import 'package:in_app_purchase/in_app_purchase.dart';
import 'package:in_app_purchase_storekit/in_app_purchase_storekit.dart';
import 'package:in_app_purchase_storekit/store_kit_2_wrappers.dart';

/// Production bridge to Flutter's official StoreKit 2 implementation.
final class ApplePluginPlatform implements AppleIapPlatform {
  /// Creates a bridge around the process-wide official IAP plugin instance.
  ApplePluginPlatform({InAppPurchase? plugin})
    : _plugin = plugin ?? InAppPurchase.instance;

  final InAppPurchase _plugin;
  final Map<String, ProductDetails> _queriedProducts =
      <String, ProductDetails>{};
  final Map<String, PurchaseDetails> _pendingTransactions =
      <String, PurchaseDetails>{};

  @override
  Stream<ApplePurchase> get purchaseUpdates => _plugin.purchaseStream
      .expand((purchases) => purchases)
      .map(_applePurchase);

  @override
  Future<bool> isAvailable() =>
      _guard(operation: 'availability', callback: _plugin.isAvailable);

  @override
  Future<AppleProductQuery> queryProducts(Set<String> productIds) => _guard(
    operation: 'product_query',
    callback: () async {
      final response = await _plugin.queryProductDetails(productIds);
      final error = response.error;
      if (error != null) {
        throw _pluginError(error, operation: 'product_query');
      }
      _queriedProducts.removeWhere((id, _) => productIds.contains(id));
      final products = <AppleProduct>[];
      for (final details in response.productDetails) {
        if (details is! AppStoreProduct2Details) {
          throw const AppleIapStackException(
            code: 'storekit_2_required',
            message: 'The purchase plugin did not return a StoreKit 2 product',
          );
        }
        final product = _appleProduct(details);
        _queriedProducts[product.id] = details;
        products.add(product);
      }
      return AppleProductQuery(
        products: products,
        notFoundProductIds: response.notFoundIDs.toSet(),
      );
    },
  );

  @override
  Future<void> launchPurchase({
    required AppleProduct product,
    required String appAccountToken,
  }) => _guard(
    operation: 'purchase',
    callback: () async {
      final details = _queriedProducts[product.id];
      if (details == null) {
        throw const AppleIapStackException(
          code: 'product_not_queried',
          message: 'Query this App Store product before purchase',
        );
      }
      final launched = await _plugin.buyNonConsumable(
        purchaseParam: Sk2PurchaseParam(
          productDetails: details,
          applicationUserName: appAccountToken,
        ),
      );
      if (!launched) {
        throw const AppleIapStackException(
          code: 'purchase_not_launched',
          message: 'StoreKit did not launch purchase UI',
        );
      }
    },
  );

  @override
  Future<List<ApplePurchase>> restorePurchases() => _guard(
    operation: 'restore',
    callback: () async {
      await SK2Transaction.restorePurchases();
      final transactions = await SK2Transaction.transactions();
      return transactions.map(_restoredPurchase).toList(growable: false);
    },
  );

  @override
  Future<void> completePurchase(ApplePurchase purchase) => _guard(
    operation: 'completion',
    callback: () async {
      final details = _pendingTransactions[purchase.transactionId];
      if (details == null || !purchase.pendingCompletion) {
        return;
      }
      await _plugin.completePurchase(details);
      _pendingTransactions.remove(purchase.transactionId);
    },
  );

  /// _applePurchase converts one official update and retains only its completion handle.
  ApplePurchase _applePurchase(PurchaseDetails details) {
    if (details is! SK2PurchaseDetails) {
      throw const AppleIapStackException(
        code: 'storekit_2_required',
        message: 'The purchase plugin returned a non-StoreKit 2 transaction',
      );
    }
    final transactionId = details.purchaseID ?? '';
    if (details.pendingCompletePurchase && transactionId.isNotEmpty) {
      _pendingTransactions[transactionId] = details;
    }
    return ApplePurchase(
      transactionId: transactionId,
      productId: details.productID,
      signedTransaction: details.verificationData.serverVerificationData,
      appAccountToken: details.appAccountToken?.toLowerCase(),
      status: _purchaseStatus(details.status),
      pendingCompletion: details.pendingCompletePurchase,
      errorCode: details.error?.code,
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
  } on AppleIapStackException {
    rethrow;
  } catch (error) {
    throw AppleIapStackException(
      code: 'apple_${operation}_failed',
      message: 'Apple $operation failed',
      cause: error,
    );
  }
}

/// _appleProduct converts StoreKit 2 details into the public safe model.
AppleProduct _appleProduct(AppStoreProduct2Details details) {
  final kind = switch (details.sk2Product.type) {
    SK2ProductType.nonConsumable => AppleProductKind.nonConsumable,
    SK2ProductType.autoRenewable => AppleProductKind.subscription,
    SK2ProductType.consumable ||
    SK2ProductType.nonRenewable => throw const AppleIapStackException(
      code: 'unsupported_product_kind',
      message: 'IAPStack does not support this App Store product kind',
    ),
  };
  return AppleProduct(
    id: details.id,
    kind: kind,
    title: details.title,
    description: details.description,
    price: details.price,
    rawPrice: details.rawPrice,
    currencyCode: details.currencyCode,
  );
}

/// _restoredPurchase converts a StoreKit 2 history row into signed evidence.
ApplePurchase _restoredPurchase(SK2Transaction transaction) => ApplePurchase(
  transactionId: transaction.id,
  productId: transaction.productId,
  signedTransaction: transaction.receiptData ?? '',
  appAccountToken: transaction.appAccountToken?.toLowerCase(),
  status: ApplePurchaseStatus.restored,
  pendingCompletion: false,
);

/// _purchaseStatus maps official plugin states into a stable companion contract.
ApplePurchaseStatus _purchaseStatus(PurchaseStatus status) {
  switch (status) {
    case PurchaseStatus.pending:
      return ApplePurchaseStatus.pending;
    case PurchaseStatus.purchased:
      return ApplePurchaseStatus.purchased;
    case PurchaseStatus.restored:
      return ApplePurchaseStatus.restored;
    case PurchaseStatus.canceled:
      return ApplePurchaseStatus.cancelled;
    case PurchaseStatus.error:
      return ApplePurchaseStatus.failed;
  }
}

/// _pluginError creates a safe failure from an official plugin error code.
AppleIapStackException _pluginError(
  IAPError error, {
  required String operation,
}) => AppleIapStackException(
  code: error.code.isEmpty ? 'apple_${operation}_failed' : error.code,
  message: 'Apple $operation failed',
  cause: error,
);
