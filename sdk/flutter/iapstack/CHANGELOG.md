# Changelog

Versions follow the shared IAPStack SDK release train. The full cross-SDK notes
are in [sdk/CHANGELOG.md](https://github.com/imariman/iapstack/blob/main/sdk/CHANGELOG.md).

## 0.1.0-sdk.3

First pub.dev release. Earlier `0.1.0-dev.N` builds were only available from
the repository.

### Breaking

- A later restore batch failure that is not an `IapStackException` is wrapped in
  `IapStackIncompleteRestoreException`. Catch that type and read `cause`.
  `IapStackApiException`, `IapStackTransportException`,
  `IapStackTimeoutException` and `IapStackProtocolException` keep their types.
  A failure before any batch succeeds is rethrown unchanged.
- `IapStackException` is sealed. An exhaustive `switch` on it needs a branch for
  `IapStackIncompleteRestoreException`, or a `default` branch.

### Added

- `committedRestoreResults` and `restoreSkips` on `IapStackException`, set only
  after a restore batch has already succeeded, and `restorePageLimitReached`.
- `RestoreResult.skipped` and `RestoreResult.pageLimitReached`.
- `RestoreSkipCounts.unsigned`, included in `total`.
