import 'package:iapstack_huawei/src/errors.dart';
import 'package:iapstack_huawei/src/product.dart';
import 'package:iapstack_huawei/src/product_kind.dart';

/// Safe device-side result of Huawei's sandbox activation check.
final class HuaweiSandboxStatus {
  /// Creates one immutable sandbox eligibility result.
  const HuaweiSandboxStatus({
    required this.isSandboxUser,
    required this.isSandboxApk,
    this.marketVersion,
    this.apkVersion,
  });

  /// Whether the signed-in HUAWEI ID is configured as a sandbox tester.
  final bool isSandboxUser;

  /// Whether the installed APK version is eligible for sandbox purchases.
  final bool isSandboxApk;

  /// Latest AppGallery version reported by Huawei, when available.
  final String? marketVersion;

  /// Installed APK version reported by Huawei, when available.
  final String? apkVersion;

  /// Whether both account and APK conditions permit sandbox testing.
  bool get isActive => isSandboxUser && isSandboxApk;
}

/// One exact detached Huawei signature and signed purchase string.
final class HuaweiSignedPurchase {
  /// Creates one exact signed purchase pair.
  const HuaweiSignedPurchase(
      {required this.purchaseData, required this.signature});

  /// Exact signed `InAppPurchaseData` JSON string.
  final String purchaseData;

  /// Detached signature returned by Huawei IAP.
  final String signature;
}

/// One page returned by Huawei `obtainOwnedPurchases`.
final class HuaweiOwnedPurchasesPage {
  /// Creates an immutable owned-purchases page.
  HuaweiOwnedPurchasesPage({
    required List<HuaweiSignedPurchase> purchases,
    this.continuationToken,
  }) : purchases = List<HuaweiSignedPurchase>.unmodifiable(purchases);

  /// Exact signed purchases in provider order.
  final List<HuaweiSignedPurchase> purchases;

  /// Provider token for the next page, or null when complete.
  final String? continuationToken;
}

/// Testable boundary around the official Huawei Flutter IAP plugin.
abstract interface class HuaweiIapPlatform {
  /// Checks whether Huawei IAP is available for the current account region.
  ///
  /// Returns false when IAP is not offered in the account's region (`60054`).
  /// When no HUAWEI ID is signed in, `huawei_iap` first opens the HMS sign-in
  /// screen and checks again after a successful sign-in. Throws
  /// [HuaweiIapStackException] when that sign-in is cancelled or fails (for
  /// example `ACTIVITY_RESULT_ERROR`, `ERR_CAN_NOT_LOG_IN` or `NO_RESOLUTION`)
  /// and for every other environment failure.
  Future<bool> isAvailable();

  /// Checks whether the current Huawei account and APK can use the sandbox.
  Future<HuaweiSandboxStatus> sandboxStatus();

  /// Loads products of one Huawei price type.
  Future<List<HuaweiProduct>> queryProducts({
    required List<String> productIds,
    required HuaweiProductKind productKind,
  });

  /// Opens Huawei purchase UI and returns exact signed evidence.
  Future<HuaweiSignedPurchase> purchase({
    required String productId,
    required HuaweiProductKind productKind,
    required String developerPayload,
  });

  /// Returns one page of currently owned products for restore.
  Future<HuaweiOwnedPurchasesPage> ownedPurchases({
    required HuaweiProductKind productKind,
    String? continuationToken,
  });
}
