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
    this.message = 'Choose an operation to exercise the SDK.',
    this.entitlements = const <Entitlement>[],
  });

  /// Current operation status.
  final ExampleStatus status;

  /// Safe operation summary.
  final String message;

  /// Latest authoritative entitlement snapshot.
  final List<Entitlement> entitlements;
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
  })  : _backend = backend,
        _huawei = huawei,
        super(const ExampleState());

  final IapStackClient _backend;
  final HuaweiIapStack _huawei;

  /// Current external customer binding.
  final String externalCustomerId;

  /// Product exercised by purchase.
  final String productId;

  /// Product type exercised by purchase.
  final HuaweiProductKind productKind;

  /// Opens Huawei purchase UI and verifies the returned evidence.
  Future<void> purchase() => _run(() async {
        final result = await _huawei.purchaseAndVerify(
          externalCustomerId: externalCustomerId,
          productId: productId,
          productKind: productKind,
        );
        return result.entitlements;
      }, successMessage: 'Purchase verified by IAPStack.');

  /// Restores current Huawei purchases and reloads the full snapshot.
  Future<void> restore() => _run(() async {
        await _huawei.restorePurchases(externalCustomerId: externalCustomerId);
        final snapshot = await _huawei.getEntitlements(externalCustomerId);
        return snapshot.entitlements;
      }, successMessage: 'Huawei purchases restored.');

  /// Loads the current IAPStack projection without contacting Huawei.
  Future<void> refresh() => _run(() async {
        final snapshot = await _huawei.getEntitlements(externalCustomerId);
        return snapshot.entitlements;
      }, successMessage: 'Entitlements refreshed.');

  Future<void> _run(
    Future<List<Entitlement>> Function() operation, {
    required String successMessage,
  }) async {
    if (state.status == ExampleStatus.loading) {
      return;
    }
    emit(ExampleState(
        status: ExampleStatus.loading,
        message: 'Working…',
        entitlements: state.entitlements));
    try {
      final entitlements = await operation();
      emit(ExampleState(
        status: ExampleStatus.success,
        message: successMessage,
        entitlements: entitlements,
      ));
    } on HuaweiIapStackException catch (error) {
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: error.userCancelled
            ? 'Purchase cancelled.'
            : 'Huawei error: ${error.code}',
        entitlements: state.entitlements,
      ));
    } on IapStackApiException catch (error) {
      final requestSuffix =
          error.requestId == null ? '' : ' Request ID: ${error.requestId}';
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: 'IAPStack error: ${error.code}.$requestSuffix',
        entitlements: state.entitlements,
      ));
    } on IapStackException catch (error) {
      emit(ExampleState(
        status: ExampleStatus.failure,
        message: error.message,
        entitlements: state.entitlements,
      ));
    }
  }

  @override
  Future<void> close() async {
    _backend.close();
    await super.close();
  }
}
