# SDK changelog

## 0.1.0-sdk.3

### Breaking

- React Native: a product that grants nothing, and an unresolved purchase, emit
  `pending` and never `verified`. That includes an empty entitlement list and a
  purchase that stays unresolved while another entitlement already grants
  access. An app that waits only for `verified` can hang. Wait with a timeout,
  or read `grantsAccess` before unlocking.
- TypeScript host: aborting after a parsed 429 or 5xx body, before the backoff
  wait, rejects with `AbortError` instead of `APIError`. A `catch` of
  `APIError` no longer matches that cancel. Catch `AbortError`.

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
