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
  /// Checks whether the current Huawei account and APK can use the sandbox.
  Future<HuaweiSandboxStatus> sandboxStatus();

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
