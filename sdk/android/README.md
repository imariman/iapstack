# IAPStack Android SDK

Provider-neutral Kotlin HTTP client plus native Google Play and Huawei adapters:

- `core` — JVM client: verify, restore, entitlements, timeouts, retries, request IDs
- `google-play` — Android AAR: Play Billing 8.3.0, product/offer queries, checkout, updates, restore
- `huawei` — Android AAR: HMS IAP 6.13.0.300, readiness/sign-in, sandbox checks, checkout, paginated restore
- [`example`](example/README.md) — native Android app and a trusted host session endpoint

Requires Android API 26+, Java 17, Android SDK 35. `core` stays a JVM library;
its `java.time` use is supported by the companions' API 26 minimum.
Store price micro-units are `Long`, matching the native SDKs.
Maven publication remains tracked in [#141](https://github.com/imariman/iapstack/issues/141).
Flutter remains the only v0.1 mobile release gate. Native Android is post-v0.1;
CI builds and tests it independently, without asserting live store certification.

## Authentication

Authenticate the user in your trusted host backend, then mint a short-lived
customer session there with the durable application bearer. Return only that
customer token, the opaque external customer ID, API URL, and application ID to
the mobile app. Never ship the durable application bearer in a mobile binary,
and never persist or log the customer bearer or signed purchase evidence.
Refresh expired sessions through the host and recreate the client/coordinator.

```kotlin
val client = IapStackClient(IapStackConfig(
  baseUri = URI(session.baseUrl),
  applicationId = session.applicationId,
  customerToken = session.token,
))
```

## Google Play

```kotlin
val catalog = mapOf("premium_monthly" to GooglePlayProductKind.SUBSCRIPTION)
val platform = BillingClientGooglePlayPlatform(applicationContext, catalog) { currentActivity }
val play = GooglePlayIapStack(client, catalog, platform)

// Start one collector at app/session startup. Use a lifecycle-owned coroutine scope.
scope.launch {
  play.purchaseUpdates.collect { purchase ->
    when (purchase.status) {
      GooglePlayPurchaseStatus.PURCHASED, GooglePlayPurchaseStatus.RESTORED -> {
        // Handle network errors per update so the collector stays alive; restore retries later.
        try {
          play.verifyPurchase(session.externalCustomerId, purchase)
        } catch (cancelled: CancellationException) {
          throw cancelled
        } catch (error: Exception) {
          // Show a safe retry message. Do not log the purchase or raw error body.
        }
      }
      GooglePlayPurchaseStatus.PENDING -> { /* Wait for payment; do not grant access. */ }
      GooglePlayPurchaseStatus.CANCELLED -> { /* Dismiss checkout. */ }
      GooglePlayPurchaseStatus.FAILED -> { /* Show a safe retry message. */ }
    }
  }
}
val offers = play.queryProducts(catalog.keys)
// Display returned prices, base plans, offer IDs, and pricing phases for selection.
play.launchPurchase(session.externalCustomerId, offers.products.first())
play.restorePurchases(session.externalCustomerId)
val snapshot = play.getEntitlements(session.externalCustomerId)
```

The exact opaque customer ID (max 64 characters, no email or other PII) becomes
`obfuscatedAccountId`. Subscription and one-time offers retain their offer tokens.
Query immediately before selection/checkout; do not persist product details.
`purchaseUpdates` queues callbacks received before collection and has one consumer.
Own one adapter per active session. Supply the current foreground Activity;
call `platform.close()` when the session ends and `client.close()` when done.
A billing disconnect fails in-flight work with `billing_service_disconnected`;
the next operation reconnects. Catalog queries and checkout selection are serialized;
failed queries never publish partial selections. Unfetched products with `PRODUCT_NOT_FOUND`
or `NO_ELIGIBLE_OFFER` are unavailable for this query, while other statuses throw
`product_query_failed_<status>` so callers can report or retry the failure.
SDK request waits are bounded to 30 seconds and report `billing_timeout`;
caller cancellation (including a caller's shorter timeout) remains cancellation. Re-query/restore when the app returns to the foreground
to recover completed pending payments or interrupted verification.

**Play purchases are never acknowledged or consumed on device.** IAPStack
acknowledges through Android Publisher after commit. Only server entitlements
control access; neither checkout success nor `isAcknowledged` grants access.

## Huawei

```kotlin
val platform = HmsHuaweiIapPlatform(activity)
val huawei = HuaweiIapStack(client, mapOf(
  "premium_monthly" to HuaweiProductKind.SUBSCRIPTION,
), platform)

huawei.isAvailable() // False for an unsupported account region; sign-in/errors throw.
val sandbox = huawei.sandboxStatus()
val products = huawei.queryProducts(setOf("premium_monthly"))
huawei.purchaseAndVerify(session.externalCustomerId, products.products.first())
huawei.restorePurchases(session.externalCustomerId)
val snapshot = huawei.getEntitlements(session.externalCustomerId)
```

Forward the Activity callback (reserve request code `41380`, or supply another
unused code to the constructor):

```kotlin
override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
  if (!platform.onActivityResult(requestCode, resultCode, data)) {
    super.onActivityResult(requestCode, resultCode, data)
  }
}
```

On `hms_sign_in_required`, offer a user action calling
`platform.resolveEnvironment()` to launch HMS sign-in/resolution and recheck readiness.
Availability and environment resolution return false for an unsupported account region.
Checkout in that region throws `hms_environment_unavailable`.
Other errors include `purchase_cancelled`
(`userCancelled = true`), and `hms_error_<provider-code>`.
A sandbox check reports account and APK eligibility independently.

Scope this adapter to its Activity and call `close()` on destruction. Cancel
caller coroutines with the Activity lifecycle. A cancelled coroutine cannot
dismiss HMS UI; another checkout remains blocked until the old result arrives.
After recreation or process death, create a new adapter and restore purchases.
SDK task waits are bounded to 30 seconds and report `hms_timeout`; caller cancellation
is preserved. User interaction is not timed out. Checkout/sign-in reserve the UI slot
before any readiness or intent request suspends. A failure or cancellation before UI
opens releases the slot; after UI opens, only its Activity result or `close()` releases it.

The customer ID becomes `developerPayload`. The adapter forwards the exact
signed `InAppPurchaseData` string and detached signature, including original
whitespace and escapes. Validation may inspect JSON but never rewrites evidence.
Owned queries preserve continuation tokens, normalize an empty terminal token,
and reject mismatched data/signature arrays. The coordinator bounds pagination,
filters customer/catalog mismatches, and submits batches to the server.

## Build and tests

```sh
cd sdk/android
./gradlew :core:test :google-play:testDebugUnitTest :huawei:testDebugUnitTest :example:testDebugUnitTest \
  :google-play:assembleRelease :huawei:assembleRelease :example:assembleDebug \
  :google-play:lintDebug :huawei:lintDebug :example:lintDebug
python3 -m unittest discover -s example/trusted-host -p 'test_*.py'
```

AARs are under each companion's `build/outputs/aar/`; the example APK is under
`example/build/outputs/apk/debug/`. Unit tests use Robolectric and mocked real
SDK boundaries; no emulator, store account, signing credentials, or live payment
is required. Device store validation follows the [example guide](example/README.md).

SDK references: [Play integration](https://developer.android.com/google/play/billing/integrate),
[HMS IapClient](https://developer.huawei.com/consumer/en/doc/HMSCore-References-V5/iapclient-0000001050137587-V5).
