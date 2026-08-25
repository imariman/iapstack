# IAPStack Google Play Internal-Testing Harness

This Android application exercises the official Flutter Play Billing plugin,
IAPStack verification and acknowledgement, restore batching, and authoritative
entitlement reads. It is a manual provider test harness, not a production app.

## 1. Configure Google Play and IAPStack

1. Change the Android `applicationId` and Kotlin namespace if the default
   `com.iapstack.example.iapstack_google_play_example` is not the package under
   test.
2. Configure that exact package in Play Console. Create an active subscription
   base plan and/or a non-consumable one-time product, then add the tester to a
   license-testing account and an internal testing track.
3. Upload a signed app bundle with a unique version code and install the build
   from Google Play's internal-testing link. A locally sideloaded debug build can
   exercise the UI but normally cannot complete real Play Billing flows.
4. Configure an IAPStack Google Play application whose
   `provider_application_id` equals the Android package. Add matching catalog
   mappings, the Android Publisher service-account credential, the customer,
   and a customer session minted by the trusted host backend.
5. Use an opaque, stable, non-PII customer identifier of at most 64 characters.
   The same value must be sent to Play Billing and IAPStack; do not use an email
   address or Google ID.

Use a public HTTPS IAPStack URL for a Play-installed device. Android emulator
debug builds can reach a host API at `http://10.0.2.2:8080`, but that is only a
local smoke-test path.

## 2. Run the harness

Pass configuration without committing it to source:

```sh
flutter run \
  --dart-define=IAPSTACK_BASE_URL=https://iap.example.com \
  --dart-define=IAPSTACK_APPLICATION_ID=application-1 \
  --dart-define=IAPSTACK_CUSTOMER_TOKEN=replace-at-runtime \
  --dart-define=IAPSTACK_EXTERNAL_CUSTOMER_ID=opaque-customer-1 \
  --dart-define=IAPSTACK_GOOGLE_PLAY_SUBSCRIPTION_ID=premium_monthly \
  --dart-define=IAPSTACK_GOOGLE_PLAY_NON_CONSUMABLE_ID=premium_lifetime
```

At least one product define is required. Mint a short-lived customer session
immediately before the run and compile only that token into this test build.
Never place an application or administrator bearer in a mobile application.

The UI never renders the customer session or purchase token and does not use
local persistence. Request IDs are shown so an operator can correlate safe
client observations with redacted server logs.

## 3. Exercise provider flows

Verify each assertion against both the harness and the IAPStack dashboard or
webhook receiver:

- Billing connects and every configured product resolves with the intended
  kind, localized price, base plan, and offer.
- A new non-consumable purchase grants the expected entitlement exactly once.
- A new subscription grants access and is acknowledged only after the IAPStack
  durable transaction succeeds.
- A pending purchase remains unresolved and is not acknowledged.
- Cancellation of the checkout UI creates no entitlement write.
- Restore repeats the same logical results without increasing entitlement
  versions or webhook event counts.
- A purchase bound to another opaque customer is rejected before evidence is
  sent to IAPStack.
- Renewal, cancellation, grace period, account hold, pause, expiration, refund,
  and revocation converge through Android Publisher and RTDN reconciliation.

Do not record purchase tokens, Billing payloads, customer sessions, service
account values, or customer identifiers in screenshots, CI logs, issues, or
release evidence.

## Local checks

```sh
flutter pub get
dart format --output=none --set-exit-if-changed lib test
flutter analyze
flutter test
flutter build apk --debug
```
