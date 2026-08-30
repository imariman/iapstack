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

## IAPStack hosted sandbox

The repository sandbox is configured for:

- API: `https://iapstack-sandbox.onrender.com`
- application: `ios-sandbox`
- bundle ID: `com.imariman.iapstack.example`
- non-consumable: `premium_lifetime`
- test customer: `663c43ca-1022-4750-b997-ccbb56957abc`

The durable application bearer remains in macOS Keychain under the service
`IAPStack Sandbox Application Key`. Connect and trust a physical iPhone, then
run the helper from this directory:

```sh
bash tool/run_app_store_sandbox.sh
```

The helper reads the durable bearer from Keychain, exchanges it for a fresh
15-minute customer session, and passes only that short-lived session to the
Flutter process. It never prints or writes either bearer. You can pass a
specific Flutter device ID as the first argument.

Run the helper through Flutter rather than Xcode's default `Runner` launch
action. The Xcode action intentionally keeps `Configuration.storekit` attached
for local StoreKit testing; Flutter's device launch does not attach that local
configuration and therefore reaches the real App Store sandbox.
