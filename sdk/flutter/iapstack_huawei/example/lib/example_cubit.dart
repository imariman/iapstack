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
    this.message = 'Checking Huawei sandbox eligibility.',
    this.entitlements = const <Entitlement>[],
    this.sandboxStatus,
    this.requestId,
  });

  /// Current operation status.
  final ExampleStatus status;

  /// Safe operation summary.
  final String message;

  /// Latest authoritative entitlement snapshot.
  final List<Entitlement> entitlements;

  /// Current device-side Huawei sandbox eligibility, when checked.
  final HuaweiSandboxStatus? sandboxStatus;

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

  /// Verifies that both the signed-in Huawei account and APK use the sandbox.
  Future<void> checkSandbox() async {
    if (state.status == ExampleStatus.loading) {
      return;
    }
    emit(ExampleState(
      status: ExampleStatus.loading,
      message: 'Checking Huawei sandbox eligibility…',
      entitlements: state.entitlements,
      sandboxStatus: state.sandboxStatus,
    ));
    try {
      final status = await _huawei.sandboxStatus();
      emit(ExampleState(
        status: status.isActive ? ExampleStatus.success : ExampleStatus.failure,
        message: status.isActive
            ? 'Huawei sandbox is active for this account and APK.'
            : 'Sandbox is not active for both the Huawei account and APK. Do not start a purchase.',
        entitlements: state.entitlements,
        sandboxStatus: status,
      ));
    } on HuaweiIapStackException catch (error) {
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: 'Huawei sandbox check failed: ${error.code}',
        entitlements: state.entitlements,
      ));
    }
  }

  /// Opens Huawei purchase UI and verifies the returned evidence.
  Future<void> purchase() {
    if (!_sandboxEligible()) {
      return Future<void>.value();
    }
    final requestId = _requestIdFactory('purchase');
    return _run(() async {
      final result = await _huawei.purchaseAndVerify(
        externalCustomerId: externalCustomerId,
        productId: productId,
        productKind: productKind,
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
        sandboxStatus: state.sandboxStatus));
    try {
      final entitlements = await operation();
      emit(ExampleState(
        status: ExampleStatus.success,
        message: successMessage,
        entitlements: entitlements,
        sandboxStatus: state.sandboxStatus,
        requestId: requestId,
      ));
    } on HuaweiIapStackException catch (error) {
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: error.userCancelled
            ? 'Purchase cancelled.'
            : 'Huawei error: ${error.code}',
        entitlements: state.entitlements,
        sandboxStatus: state.sandboxStatus,
      ));
    } on IapStackApiException catch (error) {
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: 'IAPStack error: ${error.code}.',
        entitlements: state.entitlements,
        sandboxStatus: state.sandboxStatus,
        requestId: error.requestId ?? requestId,
      ));
    } on IapStackException catch (error) {
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: error.message,
        entitlements: state.entitlements,
        sandboxStatus: state.sandboxStatus,
      ));
    }
  }

  bool _sandboxEligible() {
    if (state.sandboxStatus?.isActive == true) {
      return true;
    }
    emit(ExampleState(
      status: ExampleStatus.failure,
      message: 'Confirm Huawei sandbox eligibility before this operation.',
      entitlements: state.entitlements,
      sandboxStatus: state.sandboxStatus,
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
