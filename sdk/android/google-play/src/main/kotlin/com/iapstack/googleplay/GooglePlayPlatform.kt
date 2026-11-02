package com.iapstack.googleplay

import kotlinx.coroutines.flow.Flow

/**
 * Testable boundary around Google Play Billing implementations.
 */
interface GooglePlayIapPlatform {
  /**
   * Emits purchase updates and restored purchases in provider order.
   */
  val purchaseUpdates: Flow<GooglePlayPurchase>

  /**
   * Reports whether Google Play Billing is available for the installed app.
   */
  suspend fun isAvailable(): Boolean

  /**
   * Queries current localized details for provider product identifiers.
   */
  suspend fun queryProducts(productIds: Set<String>): GooglePlayProductQuery

  /**
   * Opens Google Play purchase UI for a previously queried product offer.
   */
  suspend fun launchPurchase(
    product: GooglePlayProduct,
    obfuscatedAccountId: String,
  )

  /**
   * Queries currently owned purchases.
   *
   * Return every owned row, including purchases with no account binding or a
   * different one. [GooglePlayIapStack.restorePurchases] counts those instead
   * of treating them as an empty restore. [obfuscatedAccountId] is the customer
   * the caller is restoring; it is not a reason to drop other rows.
   */
  suspend fun ownedPurchases(obfuscatedAccountId: String): List<GooglePlayPurchase>
}
