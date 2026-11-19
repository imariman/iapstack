# Changelog

Versions follow the shared IAPStack SDK release train and depend on the exact
same `iapstack` version. The full cross-SDK notes are in
[sdk/CHANGELOG.md](https://github.com/imariman/iapstack/blob/main/sdk/CHANGELOG.md).

## 0.1.0-sdk.3

First pub.dev release. Earlier `0.1.0-dev.N` builds were only available from
the repository.

### Breaking

- Requires `in_app_purchase_storekit` 0.4.16 or later, Dart 3.11 and
  Flutter 3.41.
- `completePurchase` and `verifyPurchase` throw `AppleIapStackException` with
  code `transaction_not_finishable` for a purchase that is pending completion
  but has no StoreKit transaction id.
- When every restore batch is accepted but StoreKit cannot finish a row,
  `restorePurchases` throws `IapStackIncompleteRestoreException` with
  `committedRestoreResults`.

### Fixed

- `restorePurchases` finishes unfinished StoreKit transactions after IAPStack
  accepts the batch that carried them. Restored rows can have
  `pendingCompletion == true`.
- `completePurchase` finishes a transaction by its StoreKit id when this
  platform did not cache the update.
- `restorePurchases` submits finished current entitlements, taking the JWS from
  `currentEntitlements()`. Finished rows without a JWS are counted on
  `skipped.unsigned`, and an unverifiable current entitlement is skipped
  instead of failing the restore.
