# Changelog

Versions follow the shared IAPStack SDK release train and depend on the exact
same `iapstack` version. The full cross-SDK notes are in
[sdk/CHANGELOG.md](https://github.com/imariman/iapstack/blob/main/sdk/CHANGELOG.md).

## 0.1.0-sdk.3

First pub.dev release. Earlier `0.1.0-dev.N` builds were only available from
the repository.

- Depends on `iapstack` 0.1.0-sdk.3. Restore failures after an accepted batch
  are reported through `IapStackIncompleteRestoreException`; see the `iapstack`
  changelog.
