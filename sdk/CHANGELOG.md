# SDK changelog

## 0.1.0-sdk.2

### Breaking

- Huawei restore no longer throws `restore_page_limit`. It submits the pages
  already read and returns `pageLimitReached` instead. That result cannot be
  resumed; raise `maxRestorePagesPerKind` to include older rows.
- Swift: a later-batch restore failure now throws `IAPStackIncompleteRestoreError`
  instead of the original error. That type does not exist on main, so a
  `catch` of `IAPStackSDKError` (or another original error) no longer matches.
  Catch `IAPStackIncompleteRestoreError` and read `underlying` for the original
  error. `committedResults` are the batches the server already accepted.
  A failure before any batch succeeds is still the original error.
- Kotlin: a later-batch failure that is not an `IapStackException` is wrapped
  in `IapStackIncompleteRestoreException`. That type does not exist on main, so
  a `catch` of the original exception no longer matches. Catch
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

### Added

- `committedRestoreResults` and `restoreSkips` on Kotlin and Flutter
  `IapStackException`, set only after a restore batch has already succeeded.
  `restorePageLimitReached` is set on that same error when Huawei had already
  hit its page cap. Swift exposes the same information as `committedResults`
  and `skipped` on `IAPStackIncompleteRestoreError`.
- `RestoreResult.skipped` (`unbound`, `differentCustomer`, `unknownProduct`,
  `multipleProducts`) and `RestoreResult.pageLimitReached`. Direct HTTP
  `fromJson` still reads only `results`.
