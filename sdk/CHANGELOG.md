# SDK changelog

## 0.1.0-sdk.2

### Breaking

Huawei restore no longer throws `restore_page_limit`. It submits the pages
already read and returns `pageLimitReached` instead. That result cannot be
resumed; raise `maxRestorePagesPerKind` to include older rows.
