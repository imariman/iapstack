import 'dart:async';

import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_google_play/iapstack_google_play.dart';

/// Async status for one manual Google Play operation.
enum ExampleStatus { initial, loading, ready, failure }

/// Immutable state shown by the internal-testing harness.
final class ExampleState {
  /// Creates example state.
  const ExampleState({
    this.status = ExampleStatus.initial,
    this.message = 'Checking Google Play Billing availability.',
    this.billingAvailable = false,
    this.products = const <GooglePlayProduct>[],
    this.entitlements = const <Entitlement>[],
    this.requestId,
  });

  /// Current operation status.
  final ExampleStatus status;

  /// Safe operation summary.
  final String message;

  /// Whether the official plugin can connect to Google Play Billing.
  final bool billingAvailable;

  /// Current localized provider product and offer rows.
  final List<GooglePlayProduct> products;

  /// Latest authoritative entitlement projection.
  final List<Entitlement> entitlements;

  /// Correlation ID for the latest IAPStack request.
  final String? requestId;
}

/// Keeps Google Play Billing and IAPStack operations outside the widget tree.
final class ExampleCubit extends Cubit<ExampleState> {
  /// Creates the example coordinator and subscribes to purchase updates.
  ExampleCubit({
    required IapStackClient backend,
    required GooglePlayIapStack googlePlay,
    required this.externalCustomerId,
    required Set<String> productIds,
    String Function(String operation)? requestIdFactory,
  }) : _backend = backend,
       _googlePlay = googlePlay,
       productIds = Set<String>.unmodifiable(productIds),
       _requestIdFactory = requestIdFactory ?? _newRequestId,
       super(const ExampleState()) {
    _purchaseSubscription = _googlePlay.purchaseUpdates.listen(
      _enqueuePurchase,
      onError: _handlePurchaseStreamError,
    );
  }

  final IapStackClient _backend;
  final GooglePlayIapStack _googlePlay;
  final String Function(String operation) _requestIdFactory;
  late final StreamSubscription<GooglePlayPurchase> _purchaseSubscription;
  Future<void> _purchaseQueue = Future<void>.value();

  /// Opaque current customer binding shared with Billing and IAPStack.
  final String externalCustomerId;

  /// Provider products queried at initialization.
  final Set<String> productIds;

  /// Checks Billing availability and loads current product offers.
  Future<void> initialize() async {
    _emitLoading('Connecting to Google Play Billing…');
    try {
      final available = await _googlePlay.isAvailable();
      if (!available) {
        emit(
          const ExampleState(
            status: ExampleStatus.failure,
            message:
                'Google Play Billing is unavailable for this installed build.',
          ),
        );
        return;
      }
      final query = await _googlePlay.queryProducts(productIds);
      final missingSuffix = query.notFoundProductIds.isEmpty
          ? ''
          : ' Missing: ${query.notFoundProductIds.join(', ')}.';
      emit(
        ExampleState(
          status: ExampleStatus.ready,
          message: 'Google Play Billing is ready.$missingSuffix',
          billingAvailable: true,
          products: query.products,
        ),
      );
    } on GooglePlayIapStackException catch (error) {
      _emitFailure('Google Play initialization failed: ${error.code}.');
    }
  }

  /// Opens Google Play purchase UI for one queried product offer.
  Future<void> purchase(GooglePlayProduct product) async {
    if (_operationBlocked()) {
      return;
    }
    _emitLoading('Opening Google Play purchase UI…');
    try {
      await _googlePlay.launchPurchase(
        externalCustomerId: externalCustomerId,
        product: product,
      );
      _emitReady('Purchase UI opened. Waiting for a Billing update.');
    } on GooglePlayIapStackException catch (error) {
      _emitFailure('Google Play purchase failed: ${error.code}.');
    }
  }

  /// Restores currently owned products and reloads the full projection.
  Future<void> restore() async {
    if (_operationBlocked()) {
      return;
    }
    final requestId = _requestIdFactory('restore');
    _emitLoading('Querying owned Google Play purchases…');
    try {
      await _googlePlay.restorePurchases(
        externalCustomerId: externalCustomerId,
        requestId: requestId,
      );
      final snapshot = await _googlePlay.getEntitlements(
        externalCustomerId,
        requestId: '$requestId-refresh',
      );
      _emitReady(
        'Owned purchases restored and verified by IAPStack.',
        entitlements: snapshot.entitlements,
        requestId: requestId,
      );
    } on GooglePlayIapStackException catch (error) {
      _emitFailure('Google Play restore failed: ${error.code}.');
    } on IapStackApiException catch (error) {
      _emitFailure(
        'IAPStack restore failed: ${error.code}.',
        requestId: error.requestId ?? requestId,
      );
    } on IapStackException catch (error) {
      _emitFailure(error.message, requestId: requestId);
    }
  }

  /// Loads authoritative IAPStack projections without contacting Google Play.
  Future<void> refresh() async {
    if (_operationBlocked()) {
      return;
    }
    final requestId = _requestIdFactory('refresh');
    _emitLoading('Refreshing IAPStack entitlements…');
    try {
      final snapshot = await _googlePlay.getEntitlements(
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

  void _enqueuePurchase(GooglePlayPurchase purchase) {
    _purchaseQueue = _purchaseQueue.then((_) => _handlePurchase(purchase));
    unawaited(_purchaseQueue);
  }

  Future<void> _handlePurchase(GooglePlayPurchase purchase) async {
    if (purchase.status == GooglePlayPurchaseStatus.cancelled) {
      _emitReady('Purchase cancelled.');
      return;
    }
    if (purchase.status == GooglePlayPurchaseStatus.failed) {
      final suffix = purchase.errorCode == null
          ? ''
          : ': ${purchase.errorCode}';
      _emitFailure('Google Play reported a purchase error$suffix.');
      return;
    }
    final requestId = _requestIdFactory('verify');
    _emitLoading('Verifying the Google Play purchase with IAPStack…');
    try {
      final result = await _googlePlay.verifyPurchase(
        externalCustomerId: externalCustomerId,
        purchase: purchase,
        requestId: requestId,
      );
      final message = purchase.status == GooglePlayPurchaseStatus.pending
          ? 'Pending purchase recorded; entitlement remains authoritative on IAPStack.'
          : 'Purchase verified and acknowledged by IAPStack.';
      _emitReady(
        message,
        entitlements: result.entitlements,
        requestId: requestId,
      );
    } on GooglePlayIapStackException catch (error) {
      _emitFailure(
        error.userCancelled
            ? 'Purchase cancelled.'
            : 'Google Play verification rejected: ${error.code}.',
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

  void _handlePurchaseStreamError(Object error, StackTrace stackTrace) {
    _emitFailure(
      'Google Play purchase stream failed. Restart the application.',
    );
  }

  bool _operationBlocked() =>
      state.status == ExampleStatus.loading || !state.billingAvailable;

  void _emitLoading(String message) {
    emit(
      ExampleState(
        status: ExampleStatus.loading,
        message: message,
        billingAvailable: state.billingAvailable,
        products: state.products,
        entitlements: state.entitlements,
      ),
    );
  }

  void _emitReady(
    String message, {
    List<Entitlement>? entitlements,
    String? requestId,
  }) {
    emit(
      ExampleState(
        status: ExampleStatus.ready,
        message: message,
        billingAvailable: state.billingAvailable,
        products: state.products,
        entitlements: entitlements ?? state.entitlements,
        requestId: requestId,
      ),
    );
  }

  void _emitFailure(String message, {String? requestId}) {
    emit(
      ExampleState(
        status: ExampleStatus.failure,
        message: message,
        billingAvailable: state.billingAvailable,
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

String _newRequestId(String operation) =>
    'play-$operation-${DateTime.now().toUtc().microsecondsSinceEpoch}';
