package com.iapstack.huawei

/**
 * Safe device-side result of Huawei's sandbox activation check.
 */
data class HuaweiSandboxStatus(
  val isSandboxUser: Boolean,
  val isSandboxApk: Boolean,
  val marketVersion: String? = null,
  val apkVersion: String? = null,
) {
  /**
   * Whether both account and APK conditions permit sandbox testing.
   */
  val isActive: Boolean
    get() = isSandboxUser && isSandboxApk
}

/**
 * One exact detached Huawei signature and signed purchase string.
 */
data class HuaweiSignedPurchase(
  val purchaseData: String,
  val signature: String,
)

/**
 * One page returned by Huawei owned-purchases query.
 */
data class HuaweiOwnedPurchasesPage(
  val purchases: List<HuaweiSignedPurchase>,
  val continuationToken: String? = null,
)

/**
 * Testable boundary around a Huawei IAP implementation.
 */
interface HuaweiIapPlatform {
  suspend fun isAvailable(): Boolean

  suspend fun sandboxStatus(): HuaweiSandboxStatus

  suspend fun queryProducts(
    productIds: List<String>,
    productKind: HuaweiProductKind,
  ): List<HuaweiProduct>

  suspend fun purchase(
    productId: String,
    productKind: HuaweiProductKind,
    developerPayload: String,
  ): HuaweiSignedPurchase

  suspend fun ownedPurchases(
    productKind: HuaweiProductKind,
    continuationToken: String? = null,
  ): HuaweiOwnedPurchasesPage
}
