import 'package:flutter_bloc/flutter_bloc.dart';
import 'package:iapstack/iapstack.dart';
import 'package:iapstack_huawei/iapstack_huawei.dart';

/// Async status for one sandbox operation.
enum ExampleStatus { initial, loading, success, failure }

/// Immutable state shown by the sandbox example.
final class ExampleState {
  /// Creates an example state.
  const ExampleState({
    this.status = ExampleStatus.initial,
    this.message = 'Checking Huawei IAP readiness.',
    this.entitlements = const <Entitlement>[],
    this.environmentAvailable,
    this.sandboxStatus,
    this.product,
    this.requestId,
  });

  /// Current operation status.
  final ExampleStatus status;

  /// Safe operation summary.
  final String message;

  /// Latest authoritative entitlement snapshot.
  final List<Entitlement> entitlements;

  /// Whether Huawei IAP supports the current account region.
  final bool? environmentAvailable;

  /// Current device-side Huawei sandbox eligibility, when checked.
  final HuaweiSandboxStatus? sandboxStatus;

  /// Product returned by AppGallery for the configured product ID.
  final HuaweiProduct? product;

  /// Correlation ID supplied to the latest successful IAPStack request.
  final String? requestId;
}

/// Keeps Huawei and IAPStack operations outside the widget tree.
final class ExampleCubit extends Cubit<ExampleState> {
  /// Creates the example coordinator.
  ExampleCubit({
    required IapStackClient backend,
    required HuaweiIapStack huawei,
    required this.externalCustomerId,
    required this.productId,
    required this.productKind,
    String Function(String operation)? requestIdFactory,
  })  : _backend = backend,
        _huawei = huawei,
        _requestIdFactory = requestIdFactory ?? _newRequestId,
        super(const ExampleState());

  final IapStackClient _backend;
  final HuaweiIapStack _huawei;
  final String Function(String operation) _requestIdFactory;

  /// Current external customer binding.
  final String externalCustomerId;

  /// Product exercised by purchase.
  final String productId;

  /// Product type exercised by purchase.
  final HuaweiProductKind productKind;

  /// Checks environment and sandbox readiness, then loads the configured product.
  Future<void> initialize() async {
    if (state.status == ExampleStatus.loading) {
      return;
    }
    emit(ExampleState(
      status: ExampleStatus.loading,
      message: 'Checking Huawei IAP and loading the product…',
      entitlements: state.entitlements,
      environmentAvailable: state.environmentAvailable,
      sandboxStatus: state.sandboxStatus,
      product: state.product,
    ));
    try {
      final available = await _huawei.isAvailable();
      if (!available) {
        emit(ExampleState(
          status: ExampleStatus.failure,
          message:
              'Huawei IAP is not available for the current account region.',
          entitlements: state.entitlements,
          environmentAvailable: false,
        ));
        return;
      }
      final status = await _huawei.sandboxStatus();
      final query = await _huawei.queryProducts(<String>{productId});
      final product = query.products.isEmpty ? null : query.products.single;
      final ready = status.isActive && product?.isPurchasable == true;
      emit(ExampleState(
        status: ready ? ExampleStatus.success : ExampleStatus.failure,
        message: switch ((status.isActive, product)) {
          (false, _) =>
            'Sandbox is not active for both the Huawei account and APK. Do not start a purchase.',
          (true, null) =>
            'The configured product was not returned by AppGallery Connect.',
          (true, final value?) when !value.isPurchasable =>
            'The configured Huawei product is not available for a new purchase.',
          _ => 'Huawei IAP, sandbox, and product are ready.',
        },
        entitlements: state.entitlements,
        environmentAvailable: true,
        sandboxStatus: status,
        product: product,
      ));
    } on HuaweiIapStackException catch (error) {
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: 'Huawei initialization failed: ${error.code}',
        entitlements: state.entitlements,
        environmentAvailable: state.environmentAvailable,
        sandboxStatus: state.sandboxStatus,
        product: state.product,
      ));
    }
  }

  /// Re-runs the complete Huawei readiness check.
  Future<void> checkSandbox() => initialize();

  /// Opens Huawei purchase UI and verifies the returned evidence.
  Future<void> purchase() {
    if (!_sandboxEligible()) {
      return Future<void>.value();
    }
    final requestId = _requestIdFactory('purchase');
    return _run(() async {
      final product = state.product!;
      final result = await _huawei.purchaseAndVerify(
        externalCustomerId: externalCustomerId,
        product: product,
        requestId: requestId,
      );
      return result.entitlements;
    }, requestId: requestId, successMessage: 'Purchase verified by IAPStack.');
  }

  /// Restores current Huawei purchases and reloads the full snapshot.
  Future<void> restore() {
    if (!_sandboxEligible()) {
      return Future<void>.value();
    }
    final requestId = _requestIdFactory('restore');
    return _run(() async {
      await _huawei.restorePurchases(
        externalCustomerId: externalCustomerId,
        requestId: requestId,
      );
      final snapshot = await _huawei.getEntitlements(
        externalCustomerId,
        requestId: '$requestId-refresh',
      );
      return snapshot.entitlements;
    }, requestId: requestId, successMessage: 'Huawei purchases restored.');
  }

  /// Loads the current IAPStack projection without contacting Huawei.
  Future<void> refresh() {
    final requestId = _requestIdFactory('refresh');
    return _run(() async {
      final snapshot = await _huawei.getEntitlements(
        externalCustomerId,
        requestId: requestId,
      );
      return snapshot.entitlements;
    }, requestId: requestId, successMessage: 'Entitlements refreshed.');
  }

  Future<void> _run(
    Future<List<Entitlement>> Function() operation, {
    required String requestId,
    required String successMessage,
  }) async {
    if (state.status == ExampleStatus.loading) {
      return;
    }
    emit(ExampleState(
        status: ExampleStatus.loading,
        message: 'Working…',
        entitlements: state.entitlements,
        environmentAvailable: state.environmentAvailable,
        sandboxStatus: state.sandboxStatus,
        product: state.product));
    try {
      final entitlements = await operation();
      emit(ExampleState(
        status: ExampleStatus.success,
        message: successMessage,
        entitlements: entitlements,
        environmentAvailable: state.environmentAvailable,
        sandboxStatus: state.sandboxStatus,
        product: state.product,
        requestId: requestId,
      ));
    } on HuaweiIapStackException catch (error) {
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: error.userCancelled
            ? 'Purchase cancelled.'
            : 'Huawei error: ${error.code}',
        entitlements: state.entitlements,
        environmentAvailable: state.environmentAvailable,
        sandboxStatus: state.sandboxStatus,
        product: state.product,
      ));
    } on IapStackApiException catch (error) {
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: 'IAPStack error: ${error.code}.',
        entitlements: state.entitlements,
        environmentAvailable: state.environmentAvailable,
        sandboxStatus: state.sandboxStatus,
        product: state.product,
        requestId: error.requestId ?? requestId,
      ));
    } on IapStackException catch (error) {
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: error.message,
        entitlements: state.entitlements,
        environmentAvailable: state.environmentAvailable,
        sandboxStatus: state.sandboxStatus,
        product: state.product,
      ));
    }
  }

  bool _sandboxEligible() {
    if (state.environmentAvailable == true &&
        state.sandboxStatus?.isActive == true &&
        state.product?.isPurchasable == true) {
      return true;
    }
    emit(ExampleState(
      status: ExampleStatus.failure,
      message:
          'Complete Huawei environment, sandbox, and product checks first.',
      entitlements: state.entitlements,
      environmentAvailable: state.environmentAvailable,
      sandboxStatus: state.sandboxStatus,
      product: state.product,
    ));
    return false;
  }

  @override
  Future<void> close() async {
    _backend.close();
    await super.close();
  }
}

String _newRequestId(String operation) =>
    'sandbox-$operation-${DateTime.now().toUtc().microsecondsSinceEpoch}';
