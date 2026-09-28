# IAPStack Apple SDK (Swift)

Provider-neutral Swift client plus a StoreKit 2 companion. The package follows
the Flutter split:

- `IAPStackClient` — verify, restore, entitlements, abortable timeouts, retries, request IDs
- `AppleIAPStack` — StoreKit 2 catalog, `appAccountToken` binding, compact JWS evidence,
  finish-after-verify, unfinished-transaction listening, restore batching

Requires the Swift 6.1 toolchain (Xcode 16.3 or later). The package builds in
Swift 5 language mode and supports iOS 15+ and macOS 12+.

Authenticate the user in your trusted host backend, then mint a short-lived
customer session there with the durable application bearer. Return only that
customer token to the mobile app. Never ship the durable application bearer in
a mobile binary, and never persist or log the customer bearer.

Finish StoreKit transactions only after IAPStack verification succeeds. Keep
signed JWS strings in memory; do not write them to disk, logs, or analytics.

This drop is the HTTP client and StoreKit companion. A full sample app with a
StoreKit Configuration file is follow-up work; this package does not yet close
issue #91.

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

See `example/README.md` for a minimal integration layout.

## Testing

Run `swift test` from this directory:

```sh
cd sdk/ios
swift test
```
