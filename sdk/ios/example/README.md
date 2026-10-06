# Runnable Swift / StoreKit 2 example

This iOS 15+ SwiftUI app uses the local `IAPStackApple` package. It includes a
shared Xcode project, StoreKit configuration with a non-consumable and an
auto-renewable subscription, and a testable purchase/session coordinator.
Requires Xcode 16.3+ (Swift 6.1); no third-party package or project generator is needed.

## Open and run

1. Open `sdk/ios/example/IAPStackExample.xcodeproj` in Xcode.
2. Choose the shared **IAPStackExample** scheme and an existing simulator/device.
3. Run. The catalog loads without credentials: `premium_lifetime` and
   `premium_monthly` are in `IAPStackExample/Configuration.storekit`.
4. To connect purchase verification, enter your **HTTPS trusted-host session URL**
   and a temporary **host login token** in the app, then start a customer session.

Do not place credentials in the scheme, build settings, source files, launch
arguments, screenshots or logs. The login field clears after successful connection.
Customer tokens and signed transactions stay only in process memory. Disconnecting
cancels the update listener, closes the HTTP client and drops session/access state.
After expiry, obtain a new host login and reconnect for the same customer; StoreKit
replays unfinished purchases to the new listener. No application bearer is accepted
as configuration by the app.

The app queries products even when payments are restricted. Purchase buttons need
an authenticated session and StoreKit payment availability; restore remains usable
for an authenticated customer when purchases are restricted.

## Trusted host

The sample uses the same host protocol as the
[Android example's tested Python host](../../android/example/README.md#trusted-host).
Run `sdk/android/example/trusted-host/server.py` on a trusted machine behind HTTPS.
The host requires these environment variables:

- `IAPSTACK_BASE_URL`: final HTTPS IAPStack URL, optionally with a path prefix.
- `IAPSTACK_APPLICATION_ID`: your Apple application ID in IAPStack.
- `IAPSTACK_APPLICATION_TOKEN`: durable application bearer, **host only**.
- `EXAMPLE_EXTERNAL_CUSTOMER_ID`: canonical lowercase UUID for the tester.
- `EXAMPLE_LOGIN_TOKEN`: separate temporary tester login; never the application bearer.

The reusable host listens on `127.0.0.1:8099`; put an HTTPS proxy in front of it.
The iOS app refuses plain HTTP session URLs, credentials in URLs, redirects and
oversized responses. It uses an ephemeral, uncached session without cookies.
The host derives customer identity from the authenticated login and POSTs to
`/v1/applications/{application_id}/customer-sessions`. The app sends `{}` to
`POST /session` with `Authorization: Bearer <temporary-host-login>` and expects:

```json
{
  "base_url": "https://iap.example",
  "application_id": "your-apple-application",
  "external_customer_id": "3f2504e0-4f89-41d3-9a0c-0305e82c3301",
  "token": "short-lived-customer-session",
  "expires_at": "2026-10-06T18:15:00Z"
}
```

Use a freshly minted expiry; the timestamp above illustrates the wire format.
Replace the single-tester host's login mechanism with your own authenticated
customer identity for a real app. The host must never trust a customer ID chosen
by an untrusted mobile request.

## Local StoreKit versus Apple sandbox

The **IAPStackExample** scheme selects the bundled local StoreKit configuration.
It exercises catalog, checkout, user cancellation, Ask to Buy and transaction
updates. Xcode signs local StoreKit transactions with its testing certificate;
those are **not Apple sandbox evidence** and a normal IAPStack Apple verifier will
reject them. The app intentionally keeps rejected transactions unfinished. It
does not add a fake-verification or finish-without-verification switch.

Use **IAPStackExample-Sandbox** for full verification against IAPStack. Its scheme
has no local StoreKit configuration. Set your own signing team and App Store
Connect bundle ID, create matching `premium_lifetime` / `premium_monthly` products,
and configure their IAPStack catalog/entitlement mapping. If you use other product
IDs, update `PurchaseStore.catalog` and the local StoreKit configuration together.
Run with an Apple sandbox tester and your sandbox-configured IAPStack application.

Apple documents the separation in [Testing at all stages of development with
Xcode and the sandbox](https://developer.apple.com/documentation/storekit/testing-at-all-stages-of-development-with-xcode-and-the-sandbox).

## Integration behavior

- An authenticated session starts `purchaseUpdates` immediately, before the first
  entitlement request. Renewals, approvals and earlier unfinished transactions
  use the same verification path as the direct checkout result.
- `launchPurchase` returns the purchase to verify directly. A pending result waits
  for the listener, and user cancellation is shown without an API submission.
- `AppleIAPStack.verifyPurchase` submits the signed JWS to IAPStack and only calls
  StoreKit `finish()` after a successful, valid response. Failure retains the
  purchase in memory for **Retry verification**; a later launch replays it too.
- The listener survives an individual verification failure. Transactions for
  another customer or an unconfigured product are never forwarded or finished.
- **Restore purchases** performs StoreKit sync and bounded server verification,
  finishing only verified unfinished rows. It then fetches a complete entitlement
  snapshot, including when the history is empty. Access comes from server results.
- **Refresh entitlements** fetches server access independently of purchasing.

## Verification

From the repository root:

```sh
swift test --package-path sdk/ios
swift test --package-path sdk/ios/example
xcodebuild \
  -project sdk/ios/example/IAPStackExample.xcodeproj \
  -scheme IAPStackExample \
  -configuration Debug \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath /tmp/iapstack-ios-example \
  CODE_SIGNING_ALLOWED=NO build
```

The generic simulator build does not boot or modify an existing simulator. The
flow tests run on macOS without any simulator or store account, injecting a store
adapter and HTTP responses while exercising the real Swift companion/client.
They cover cancellation, pending approvals, failed verification remaining
unfinished, retry, continuous listening, customer filtering, restore and
entitlement refresh. Session tests cover authentication, HTTPS configuration,
expiry, fractional timestamps and bounded response handling. The SDK tests use a
loopback-only server to verify actual URLSession redirect behavior.

Manual sandbox acceptance (record results for the exact candidate commit):

1. Query both products and buy each; confirm server entitlements and finish.
2. Cancel checkout; confirm there is no verification or access grant.
3. Make verification fail; confirm the transaction stays unfinished. Restore
   service and use Retry verification; confirm access and finish.
4. Enable Ask to Buy in local StoreKit, approve later, and confirm the listener
   handles the event. Use actual sandbox renewals for end-to-end server evidence.
5. Relaunch and reconnect as the same customer; restore and refresh entitlements.
6. Repeat restore with empty history and failed verification. Neither should
   invent access or finish a transaction whose verification failed.
7. Exercise subscription expiration/refund/revocation in the sandbox and confirm
   the refreshed server projection removes access.

Automated flow tests and compilation are not a substitute for the real-store
release gate in `docs/releases/v0.1.0-rc.1.md`.
