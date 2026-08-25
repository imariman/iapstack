# IAPStack StoreKit 2 test harness

This iOS-only example exercises the Apple companion with Xcode StoreKit
Testing. The shared Runner scheme already selects
`ios/Runner/Configuration.storekit`, which defines:

- `premium_monthly`, an auto-renewable monthly subscription.
- `premium_lifetime`, a non-consumable lifetime product.

The local StoreKit configuration creates locally signed test transactions.
IAPStack's server adapter requires Apple certificate chains, so full backend
verification must use an App Store sandbox account and matching sandbox
application credentials. The local configuration remains useful for product,
listener, cancellation, restore, and unfinished-transaction UI testing.

Run the harness with runtime-only configuration:

```sh
flutter run \
  --dart-define=IAPSTACK_BASE_URL=https://iap.example.com \
  --dart-define=IAPSTACK_APPLICATION_ID=ios-sandbox \
  --dart-define=IAPSTACK_CUSTOMER_TOKEN=replace-at-runtime \
  --dart-define=IAPSTACK_EXTERNAL_CUSTOMER_ID=018f59d0-a200-7000-8000-000000000001
```

The customer value must be a canonical lowercase UUID because StoreKit uses it
as `appAccountToken`. The customer session is never rendered or persisted.

For App Store sandbox testing, replace the product identifiers with the values
configured in App Store Connect by adding
`IAPSTACK_APPLE_SUBSCRIPTION_ID` and
`IAPSTACK_APPLE_NON_CONSUMABLE_ID` dart defines.
