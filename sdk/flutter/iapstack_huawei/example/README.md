# Huawei sandbox example

This Android example is the manual device harness for IAPStack release
candidates. It never writes the customer session or purchase payloads to local
storage, logs, or analytics.

## AppGallery Connect setup

1. Register the Android package name in AppGallery Connect. The checked-in
   example uses `com.iapstack.example.iapstack_huawei_example`; change both the
   Gradle application ID and Kotlin package if your sandbox app differs.
2. Enable Huawei IAP and configure the non-consumable/subscription product IDs.
3. Download `agconnect-services.json` and place it at `android/app/`. The file
   is gitignored. The Gradle script applies AGCP only when this file exists.
4. Configure a signing certificate registered with AppGallery Connect. Do not
   commit keystores or passwords.
5. Add the Huawei account as a sandbox tester.

Run on a Huawei device with HMS Core:

```sh
flutter run \
  --dart-define=IAPSTACK_BASE_URL=https://iap.example.com \
  --dart-define=IAPSTACK_APPLICATION_ID=my-application \
  --dart-define=IAPSTACK_CUSTOMER_TOKEN=replace-at-runtime \
  --dart-define=IAPSTACK_EXTERNAL_CUSTOMER_ID=customer-123 \
  --dart-define=IAPSTACK_HUAWEI_PRODUCT_ID=premium_monthly \
  --dart-define=IAPSTACK_HUAWEI_PRODUCT_KIND=subscription
```

Use `non_consumable` for lifetime products. Exercise purchase, restore, and
refresh while recording the server request IDs for the sandbox release gate.

The app calls Huawei's `isSandboxActivated` API at startup. Do not start a
purchase unless the screen reports both `Test account: Eligible` and
`Sandbox APK: Eligible`, and Huawei checkout displays its sandbox notice. The
purchase and restore controls remain disabled until both flags are active.

Huawei shortens subscription periods in the sandbox: one week is three minutes,
one month is five minutes, two months is ten minutes, three months is fifteen
minutes, six months is thirty minutes, and one year is one hour. Automatic
renewal stops after at most six renewals. Use the visible request ID to correlate
each result without copying signed purchase data or application credentials.

Follow the complete [v0.1.0 release runbook](../../../../docs/releases/v0.1.0.md)
for cancellation, expiration, grace, refund, revocation, duplicate, negative,
reconciliation, and production webhook assertions.
