# SDK changelog

## 0.1.0-sdk.3

### Breaking

- Huawei restore no longer throws `restore_page_limit`. It submits the pages
  already read and returns `pageLimitReached` instead. That result cannot be
  resumed; raise `maxRestorePagesPerKind` to include older rows.
- Swift: a later-batch restore failure now throws `IAPStackIncompleteRestoreError`
  instead of the original error. That type did not exist in 0.1.0-sdk.2, so a
  `catch` of `IAPStackSDKError` (or another original error) no longer matches.
  Catch `IAPStackIncompleteRestoreError` and read `underlying` for the original
  error. `committedResults` are the batches the server already accepted.
  A failure before any batch succeeds is still the original error.
- Kotlin: a later-batch failure that is not an `IapStackException` is wrapped
  in `IapStackIncompleteRestoreException`. That type did not exist in
  0.1.0-sdk.2, so a `catch` of the original exception no longer matches. Catch
  `IapStackIncompleteRestoreException` and read `cause`. `IapStackException`
  failures, including `IapStackApiException`, are still thrown as that same
  type. A failure before any batch succeeds is still the original error.
- Flutter: a later-batch failure that is not an `IapStackException` is wrapped
  in `IapStackIncompleteRestoreException`. Catch that type and read `cause`.
  `IapStackApiException`, `IapStackTransportException`, `IapStackTimeoutException`,
  and `IapStackProtocolException` stay those types, so an existing
  `on IapStackApiException` catch still matches. A failure before any batch
  succeeds is rethrown unchanged.
- Kotlin and Flutter: `IapStackException` is sealed (`IapStackErrors.kt`,
  `errors.dart`). `IapStackIncompleteRestoreException` is a new subtype, so an
  exhaustive `when` or `switch` on `IapStackException` with no `else` or
  `default` fails to compile. Add a branch for
  `IapStackIncompleteRestoreException`, or an `else` / `default` branch.
- React Native: a product that grants nothing, and an unresolved purchase, emit
  `pending` and never `verified`. That includes an empty entitlement list and a
  purchase that stays unresolved while another entitlement already grants
  access. An app that waits only for `verified` can hang. Wait with a timeout,
  or read `grantsAccess` before unlocking.
- TypeScript host: aborting after a parsed 429 or 5xx body, before the backoff
  wait, rejects with `AbortError` instead of `APIError`. A `catch` of
  `APIError` no longer matches that cancel. Catch `AbortError`.
- Flutter Apple: `completePurchase` throws `AppleIapStackException` with code
  `transaction_not_finishable` when `pendingCompletion` is true and the
  purchase has no StoreKit transaction id. `verifyPurchase` throws the same
  error for such a purchase before it contacts IAPStack. Before, completion
  returned without finishing and StoreKit kept the transaction open.
- Flutter Apple: when every restore batch is accepted but StoreKit cannot
  finish a row, `restorePurchases` throws `IapStackIncompleteRestoreException`
  with `committedRestoreResults`, as Swift throws
  `IAPStackIncompleteRestoreError`.
- Flutter Apple requires `in_app_purchase_storekit` 0.4.16, so the minimum is
  now Dart 3.11 and Flutter 3.41. That version adds
  `SK2Transaction.currentEntitlements()`, which restore needs.

### Fixed

- Flutter Apple `restorePurchases` finishes unfinished StoreKit transactions
  after IAPStack accepts the batch that carried them, as Swift does. Rows in a
  failed batch stay unfinished. Restored rows can now have
  `pendingCompletion == true`.
- Flutter Apple `completePurchase` finishes a transaction by its StoreKit id
  when this platform did not cache the update, for example one delivered to
  another `ApplePluginPlatform`. Before, it returned without finishing.
- Flutter Apple `restorePurchases` submits finished current entitlements. The
  plugin returns history rows without a JWS, so on a real device restore
  submitted only unfinished rows and dropped the rest without counting them.
  Restore now takes the JWS from `currentEntitlements()`. Finished rows that
  still have no JWS, such as earlier renewals, are counted on
  `skipped.unsigned`. Swift still submits them.

### Added

- `committedRestoreResults` and `restoreSkips` on Kotlin and Flutter
  `IapStackException`, set only after a restore batch has already succeeded.
  `restorePageLimitReached` is set on that same error when Huawei had already
  hit its page cap. Swift exposes the same information as `committedResults`
  and `skipped` on `IAPStackIncompleteRestoreError`.
- `RestoreResult.skipped` (`unbound`, `differentCustomer`, `unknownProduct`,
  `multipleProducts`) and `RestoreResult.pageLimitReached`. Direct HTTP
  `fromJson` still reads only `results`.
- Flutter: `RestoreSkipCounts.unsigned`, included in `total`. Flutter Apple
  counts restore rows with no JWS there. Swift and Kotlin have no such count.

## 0.1.0-sdk.2

Released with #223 and #226. These notes stay with that version.

### Breaking

- Google Play: `GooglePlayPurchase.canVerify` is false while Play reports
  pending. An automatic listener should wait for purchased or restored.
  `verifyPurchase` still accepts a pending purchase when the caller submits it.
  Do not unlock access until `grantsAccess` is true.
- `GooglePlayIapStackException.retryable` is new and defaults to false. A Play
  Billing disconnect sets it to true and starts one reconnect. The in-flight
  call still fails.
- React Native: `StoreEvent` includes `{type: 'pending'}`. A TypeScript
  `switch` that was exhaustive on `verified` and `error` does not compile until
  it handles `pending`. A pending Play purchase, and a verification the server
  leaves unresolved, arrives as `pending` instead of `verified`.
- TypeScript host: a caller abort rejects with `AbortError`, including an abort
  during retry backoff. `instanceof TransportError` does not match it. The
  client's own attempt deadline is still `TimeoutError`.

### Fixed

- Go retry cancellation returns `context.Canceled` instead of the previous
  API, timeout, or transport error.
- Flutter `getOffering` clears its in-memory body on HTTP 404 and returns the
  fallback, so a removed offering is not shown again.
- StoreKit purchase listeners share one `Transaction.updates` pass. Each new
  pass reads `Transaction.unfinished` once and dedupes that replay.
- Huawei `purchaseAndVerify` rejects a customer id with surrounding whitespace
  before the store purchase UI opens.
- React Native keeps store events until a listener attaches, and delivers a
  replay before any event that arrives during it.
