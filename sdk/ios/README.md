# IAPStack Apple SDK (Swift)

This package provides:

- Provider-neutral `IAPStackClient` for the IAPStack v1 API:
  - `verifyPurchase`
  - `restorePurchases`
  - `getEntitlements`
- `AppleIAPStack` companion flow for StoreKit 2 catalog and checkout orchestration.
- Strong retry/backoff, per-attempt timeouts, and contract-safe JSON decoding.
- SPM distribution with dedicated unit tests under `Tests/IAPStackAppleTests`.

```swift
import IAPStackApple

let config = IAPStackConfig(
  baseUri: URL(string: "https://iap.example")!,
  applicationId: "my-application",
  customerToken: "short-lived-customer-token",
)

let client = IAPStackClient(config: config)
let snapshot = try await client.getEntitlements("customer-uuid")
```

## StoreKit companion

`AppleIAPStack` couples StoreKit 2 updates with provider-neutral verification:

```swift
let stack = AppleIAPStack(
  client: client,
  productKinds: [
    "premium_monthly": .subscription,
    "premium_lifetime": .nonConsumable,
  ],
)

let productQuery = try await stack.queryProducts(["premium_monthly", "premium_lifetime"])
let updates = stack.purchaseUpdates

for await update in updates {
  if update.canVerify {
    _ = try await stack.verifyPurchase(
      externalCustomerId: "customer-uuid",
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
