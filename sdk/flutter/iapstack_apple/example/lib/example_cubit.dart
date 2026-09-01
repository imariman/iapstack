import 'dart:async';

import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:flutter/services.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_apple/iapstack_apple.dart';
import 'package:iapstack_apple_example/sandbox_tools.dart';

/// Async status for one manual StoreKit operation.
enum ExampleStatus { initial, loading, ready, failure }

/// Immutable state shown by the StoreKit 2 harness.
final class ExampleState {
  /// Creates example state.
  const ExampleState({
    this.status = ExampleStatus.initial,
    this.message = 'Checking StoreKit availability.',
    this.storeAvailable = false,
    this.products = const <AppleProduct>[],
    this.entitlements = const <Entitlement>[],
    this.requestId,
  });

  /// Current operation status.
  final ExampleStatus status;

  /// Safe operation summary.
  final String message;

  /// Whether StoreKit reports purchases are available.
  final bool storeAvailable;

  /// Current localized App Store products.
  final List<AppleProduct> products;

  /// Latest authoritative entitlement projection.
  final List<Entitlement> entitlements;

  /// Correlation ID for the latest IAPStack request.
  final String? requestId;
}

/// Keeps StoreKit and IAPStack operations outside the widget tree.
final class ExampleCubit extends Cubit<ExampleState> {
  /// Creates the example coordinator and immediately listens for transactions.
  ExampleCubit({
    required IapStackClient backend,
    required AppleIapStack apple,
    required SandboxTools sandboxTools,
    required this.externalCustomerId,
    required this.subscriptionProductId,
    required Set<String> productIds,
    String Function(String operation)? requestIdFactory,
  }) : _backend = backend,
       _apple = apple,
       _sandboxTools = sandboxTools,
       productIds = Set<String>.unmodifiable(productIds),
       _requestIdFactory = requestIdFactory ?? _newRequestId,
       super(const ExampleState()) {
    _purchaseSubscription = _apple.purchaseUpdates.listen(
      _enqueuePurchase,
      onError: _handlePurchaseStreamError,
    );
  }

  final IapStackClient _backend;
  final AppleIapStack _apple;
  final SandboxTools _sandboxTools;
  final String Function(String operation) _requestIdFactory;
  late final StreamSubscription<ApplePurchase> _purchaseSubscription;
  Future<void> _purchaseQueue = Future<void>.value();

  /// Canonical UUID shared with StoreKit and IAPStack.
  final String externalCustomerId;

  /// Auto-renewable product used by subscription management and refund tests.
  final String subscriptionProductId;

  /// Provider products queried at initialization.
  final Set<String> productIds;

  /// Checks StoreKit availability and loads current products.
  Future<void> initialize() async {
    _emitLoading('Connecting to StoreKit 2…');
    try {
      final available = await _apple.isAvailable();
      if (!available) {
        emit(
          const ExampleState(
            status: ExampleStatus.failure,
            message: 'StoreKit purchases are unavailable for this build.',
          ),
        );
        return;
      }
      final query = await _apple.queryProducts(productIds);
      final missing = query.notFoundProductIds.isEmpty
          ? ''
          : ' Missing: ${query.notFoundProductIds.join(', ')}.';
      emit(
        ExampleState(
          status: ExampleStatus.ready,
          message: 'StoreKit 2 is ready.$missing',
          storeAvailable: true,
          products: query.products,
        ),
      );
    } on AppleIapStackException catch (error) {
      _emitFailure('StoreKit initialization failed: ${error.code}.');
    }
  }

  /// Opens StoreKit purchase UI for one queried product.
  Future<void> purchase(AppleProduct product) async {
    if (_operationBlocked()) {
      return;
    }
    _emitLoading('Opening StoreKit purchase UI…');
    try {
      await _apple.launchPurchase(
        externalCustomerId: externalCustomerId,
        product: product,
      );
      _emitReady('Purchase UI opened. Waiting for a StoreKit update.');
    } on AppleIapStackException catch (error) {
      _emitFailure('StoreKit purchase failed: ${error.code}.');
    }
  }

  /// Synchronizes App Store ownership and verifies restored transactions.
  Future<void> restore() async {
    if (_operationBlocked()) {
      return;
    }
    final requestId = _requestIdFactory('restore');
    _emitLoading('Synchronizing App Store purchases…');
    try {
      await _apple.restorePurchases(
        externalCustomerId: externalCustomerId,
        requestId: requestId,
      );
      final snapshot = await _apple.getEntitlements(
        externalCustomerId,
        requestId: '$requestId-refresh',
      );
      _emitReady(
        'StoreKit history restored and verified by IAPStack.',
        entitlements: snapshot.entitlements,
        requestId: requestId,
      );
    } on AppleIapStackException catch (error) {
      _emitFailure('StoreKit restore failed: ${error.code}.');
    } on IapStackApiException catch (error) {
      _emitFailure(
        'IAPStack restore failed: ${error.code}.',
        requestId: error.requestId ?? requestId,
      );
    } on IapStackException catch (error) {
      _emitFailure(error.message, requestId: requestId);
    }
  }

  /// Runs two independent restores and proves logical projections stay unchanged.
  Future<void> restoreTwice() async {
    if (_operationBlocked()) {
      return;
    }
    final firstRequestId = _requestIdFactory('restore-idempotency-1');
    final secondRequestId = _requestIdFactory('restore-idempotency-2');
    _emitLoading('Running consecutive restore idempotency check…');
    try {
      final firstRestore = await _apple.restorePurchases(
        externalCustomerId: externalCustomerId,
        requestId: firstRequestId,
      );
      final firstSnapshot = await _apple.getEntitlements(
        externalCustomerId,
        requestId: '$firstRequestId-refresh',
      );
      final secondRestore = await _apple.restorePurchases(
        externalCustomerId: externalCustomerId,
        requestId: secondRequestId,
      );
      final secondSnapshot = await _apple.getEntitlements(
        externalCustomerId,
        requestId: '$secondRequestId-refresh',
      );
      if (firstRestore.results.isEmpty || secondRestore.results.isEmpty) {
        _emitFailure(
          'Restore idempotency check needs at least one owned App Store transaction.',
          requestId: secondRequestId,
        );
        return;
      }
      if (firstRestore.results.length != secondRestore.results.length ||
          !sameLogicalEntitlementProjections(
            firstSnapshot.entitlements,
            secondSnapshot.entitlements,
          )) {
        _emitFailure(
          'Restore idempotency failed: the second restore changed a logical projection.',
          requestId: secondRequestId,
        );
        return;
      }
      final versions = secondSnapshot.entitlements
          .map((entitlement) => '${entitlement.key}=v${entitlement.version}')
          .join(', ');
      _emitReady(
        'PASS: two consecutive restores preserved ${firstRestore.results.length} transaction result(s) and projection versions${versions.isEmpty ? '' : ' ($versions)'}.',
        entitlements: secondSnapshot.entitlements,
        requestId: secondRequestId,
      );
    } on AppleIapStackException catch (error) {
      _emitFailure(
        'Apple restore idempotency check failed: ${error.code}.',
        requestId: secondRequestId,
      );
    } on IapStackApiException catch (error) {
      _emitFailure(
        'IAPStack restore idempotency check failed: ${error.code}.',
        requestId: error.requestId ?? secondRequestId,
      );
    } on IapStackException catch (error) {
      _emitFailure(error.message, requestId: secondRequestId);
    }
  }

  /// Loads authoritative IAPStack projections without contacting StoreKit.
  Future<void> refresh() async {
    if (_operationBlocked()) {
      return;
    }
    final requestId = _requestIdFactory('refresh');
    _emitLoading('Refreshing IAPStack entitlements…');
    try {
      final snapshot = await _apple.getEntitlements(
        externalCustomerId,
        requestId: requestId,
      );
      _emitReady(
        'Entitlements refreshed.',
        entitlements: snapshot.entitlements,
        requestId: requestId,
      );
    } on IapStackApiException catch (error) {
      _emitFailure(
        'IAPStack refresh failed: ${error.code}.',
        requestId: error.requestId ?? requestId,
      );
    } on IapStackException catch (error) {
      _emitFailure(error.message, requestId: requestId);
    }
  }

  /// Opens Apple's system subscription management sheet for cancellation tests.
  Future<void> manageSubscription() async {
    if (_operationBlocked()) {
      return;
    }
    _emitLoading('Opening App Store subscription management…');
    try {
      await _sandboxTools.showManageSubscriptions();
      _emitReady(
        'Subscription management closed. Run restore or refresh after Apple processes the change.',
      );
    } on PlatformException catch (error) {
      _emitFailure('Subscription management failed: ${error.code}.');
    }
  }

  /// Opens Apple's sandbox refund sheet for the configured subscription.
  Future<void> requestSubscriptionRefund() async {
    if (_operationBlocked()) {
      return;
    }
    _emitLoading('Opening App Store sandbox refund request…');
    try {
      final status = await _sandboxTools.beginRefundRequest(
        subscriptionProductId,
      );
      _emitReady(
        'Refund request sheet closed with status: $status. Wait for the REFUND notification, then restore or refresh.',
      );
    } on PlatformException catch (error) {
      _emitFailure('Refund request failed: ${error.code}.');
    }
  }

  /// _enqueuePurchase serializes transaction verification and completion.
  void _enqueuePurchase(ApplePurchase purchase) {
    _purchaseQueue = _purchaseQueue.then((_) => _handlePurchase(purchase));
    unawaited(_purchaseQueue);
  }

  /// _handlePurchase verifies successful updates and leaves failed evidence unfinished.
  Future<void> _handlePurchase(ApplePurchase purchase) async {
    if (purchase.status == ApplePurchaseStatus.pending) {
      _emitReady('Purchase is pending App Store approval.');
      return;
    }
    if (purchase.status == ApplePurchaseStatus.cancelled) {
      _emitReady('Purchase cancelled.');
      return;
    }
    if (purchase.status == ApplePurchaseStatus.failed) {
      final suffix = purchase.errorCode == null
          ? ''
          : ': ${purchase.errorCode}';
      _emitFailure('StoreKit reported a purchase error$suffix.');
      return;
    }
    final requestId = _requestIdFactory('verify');
    _emitLoading('Verifying the signed transaction with IAPStack…');
    try {
      final result = await _apple.verifyPurchase(
        externalCustomerId: externalCustomerId,
        purchase: purchase,
        requestId: requestId,
      );
      _emitReady(
        'Purchase verified and StoreKit transaction finished.',
        entitlements: result.entitlements,
        requestId: requestId,
      );
    } on AppleIapStackException catch (error) {
      _emitFailure(
        error.userCancelled
            ? 'Purchase cancelled.'
            : 'Apple verification rejected: ${error.code}.',
        requestId: requestId,
      );
    } on IapStackApiException catch (error) {
      _emitFailure(
        'IAPStack verification failed: ${error.code}.',
        requestId: error.requestId ?? requestId,
      );
    } on IapStackException catch (error) {
      _emitFailure(error.message, requestId: requestId);
    }
  }

  /// _handlePurchaseStreamError reports a safe restart instruction.
  void _handlePurchaseStreamError(Object error, StackTrace stackTrace) {
    _emitFailure('StoreKit transaction listener failed. Restart the app.');
  }

  /// _operationBlocked reports whether a new manual action is unsafe.
  bool _operationBlocked() =>
      state.status == ExampleStatus.loading || !state.storeAvailable;

  /// _emitLoading preserves safe state while an operation runs.
  void _emitLoading(String message) {
    emit(
      ExampleState(
        status: ExampleStatus.loading,
        message: message,
        storeAvailable: state.storeAvailable,
        products: state.products,
        entitlements: state.entitlements,
      ),
    );
  }

  /// _emitReady publishes one successful safe status.
  void _emitReady(
    String message, {
    List<Entitlement>? entitlements,
    String? requestId,
  }) {
    emit(
      ExampleState(
        status: ExampleStatus.ready,
        message: message,
        storeAvailable: state.storeAvailable,
        products: state.products,
        entitlements: entitlements ?? state.entitlements,
        requestId: requestId,
      ),
    );
  }

  /// _emitFailure publishes a redacted failure status.
  void _emitFailure(String message, {String? requestId}) {
    emit(
      ExampleState(
        status: ExampleStatus.failure,
        message: message,
        storeAvailable: state.storeAvailable,
        products: state.products,
        entitlements: state.entitlements,
        requestId: requestId,
      ),
    );
  }

  @override
  Future<void> close() async {
    await _purchaseSubscription.cancel();
    await _purchaseQueue;
    _backend.close();
    await super.close();
  }
}

/// _newRequestId creates one diagnostic-only correlation identifier.
String _newRequestId(String operation) =>
    'apple-$operation-${DateTime.now().toUtc().microsecondsSinceEpoch}';

/// Compares the logical fields whose equality proves a replay did not advance a projection.
bool sameLogicalEntitlementProjections(
  List<Entitlement> first,
  List<Entitlement> second,
) {
  if (first.length != second.length) {
    return false;
  }
  String identity(Entitlement entitlement) => <Object?>[
    entitlement.key,
    entitlement.access,
    entitlement.reason,
    entitlement.version,
    entitlement.effectiveStartsAt?.toUtc().toIso8601String(),
    entitlement.effectiveEndsAt?.toUtc().toIso8601String(),
  ].join('|');

  final firstIdentities = first.map(identity).toList()..sort();
  final secondIdentities = second.map(identity).toList()..sort();
  for (var index = 0; index < firstIdentities.length; index++) {
    if (firstIdentities[index] != secondIdentities[index]) {
      return false;
    }
  }
  return true;
}
