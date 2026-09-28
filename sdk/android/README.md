# IAPStack Android SDK

Provider-neutral Kotlin client plus Google Play and Huawei companions. The
modules follow the Flutter package split:

- `core` — verify, restore, entitlements, abortable timeouts, retries, request IDs
- `google-play` — catalog checks, `obfuscatedAccountId` binding, purchase-token evidence
- `huawei` — exact signed `InAppPurchaseData`, `developerPayload`, restore pagination

Authenticate the user in your trusted host backend, then mint a short-lived
customer session there with the durable application bearer. Return only that
customer token to the mobile app. Never ship the durable application bearer in
a mobile binary, and never persist or log the customer bearer.

Play Billing acknowledgement stays on the IAPStack server after commit. Do not
call `acknowledgePurchase` on device. Huawei evidence must be the exact signed
payload string, not a parsed rewrite.

This drop is the HTTP client and companion coordinators behind injectable
`GooglePlayIapPlatform` / `HuaweiIapPlatform` boundaries. Production
BillingClient and HMS adapters and Android library packaging are tracked in
[#138](https://github.com/imariman/iapstack/issues/138); Maven
publication is tracked in [#141](https://github.com/imariman/iapstack/issues/141).

## Build

```sh
cd sdk/android
./gradlew test
```
