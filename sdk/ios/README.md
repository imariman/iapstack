# IAPStack Apple SDK (Swift)

Provider-neutral Swift client plus a StoreKit 2 companion. The package follows
the Flutter split:

- `IAPStackClient` — verify, restore, entitlements, abortable timeouts, retries, request IDs
- `AppleIAPStack` — StoreKit 2 catalog, `appAccountToken` binding, compact JWS evidence,
  finish-after-verify, unfinished-transaction listening, restore batching
- `IAPStackSessionLoader` — optional exchange of your app's login token for a customer
  session at a trusted host's `/session` endpoint

Requires the Swift 6.1 toolchain (Xcode 16.3 or later). The package builds in
Swift 5 language mode and supports iOS 15+ and macOS 12+.

Authenticate the user in your trusted host backend, then mint a short-lived
customer session there with the durable application bearer. Return only that
customer token to the mobile app. Never ship the durable application bearer in
a mobile binary, and never persist or log the customer bearer.

If your host implements the example `/session` contract, `IAPStackSessionLoader`
posts `{}` with your app's own login bearer and returns a validated
`IAPStackCustomerSession`. It sends that bearer only to the HTTPS endpoint, never
follows redirects, uses no cookies or caches, caps the response at 16 KiB, and
times out after 15 seconds without progress or 20 seconds overall. Failures are
`IAPStackSessionError` values with stable `code` strings.

```swift
let session = try await IAPStackSessionLoader().load(endpoint: sessionURL, loginToken: loginToken)
let client = try IAPStackClient(config: session.config)
```

Finish StoreKit transactions only after IAPStack verification succeeds. Keep
signed JWS strings in memory; do not write them to disk, logs, or analytics.

The runnable [SwiftUI example](example/README.md) includes a shared Xcode scheme,
a local StoreKit catalog, a sandbox scheme, and trusted-host customer sessions.
It demonstrates purchases, update listening, failed-verification retries, cancellation,
restore, and server entitlements. Swift Package distribution is described in the
[SDK distribution guide](../../docs/sdk-releases.md).

HTTP redirects are returned as non-retryable API errors, including when an existing
`URLSession` is injected. Credentials and signed evidence are never automatically
replayed at a redirect target. Configure the final HTTPS API origin directly.

```swift
import IAPStackApple

let config = IAPStackConfig(
  baseUri: URL(string: "https://iap.example")!,
  applicationId: "my-application",
  customerToken: "short-lived-customer-token",
)

let client = try IAPStackClient(config: config)
let snapshot = try await client.getEntitlements("customer-uuid")
let hasPremium = snapshot.entitlements.contains { $0.key == "premium" && $0.grantsAccess() }
```

## StoreKit companion

`AppleIAPStack` couples StoreKit 2 updates with provider-neutral verification.
`externalCustomerId` must be a lowercase UUID; it is sent as StoreKit's
`appAccountToken`.

```swift
let stack = try AppleIAPStack(
  client: client,
  productKinds: [
    "premium_monthly": .subscription,
    "premium_lifetime": .nonConsumable,
  ],
)

let productQuery = try await stack.queryProducts(["premium_monthly", "premium_lifetime"])

// Your signed-in customer's ID, as a lowercase UUID.
let customerId = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"

// Purchases made in this session are returned directly; StoreKit does not
// re-emit them on `purchaseUpdates`.
if let purchase = try await stack.launchPurchase(
  externalCustomerId: customerId,
  product: productQuery.products[0],
) {
  _ = try await stack.verifyPurchase(externalCustomerId: customerId, purchase: purchase)
}

// Renewals, Ask to Buy approvals, other devices, and unfinished transactions
// from earlier launches arrive here.
let updates = stack.purchaseUpdates

for await update in updates {
  if update.canVerify {
    _ = try await stack.verifyPurchase(
      externalCustomerId: customerId,
      purchase: update,
    )
  }
}
```

## Example

Open `example/IAPStackExample.xcodeproj` and select `IAPStackExample`. See the
[example guide](example/README.md) for trusted-host setup, sandbox testing and checks.

## Testing

The package manifest is the repository-root `Package.swift`. Run `swift test` from
the repository root:

```sh
swift test
```
