# IAPStack React Native SDK

Native StoreKit 2, Google Play Billing and Huawei IAP purchase flows, plus the
customer-facing IAPStack HTTP client. The native bridge delegates evidence,
verification, completion and restore to the canonical Swift/Kotlin SDKs. Signed
payloads are never reconstructed in JavaScript. Huawei is Android-only.

## Install

```sh
npm install @iapstack/react-native@next
cd ios && pod install
```

Prerelease version: `0.1.0-sdk.1`. The package is ready for release; until it is
published, use the tarball produced by `npm pack` in this directory.

Requires React Native 0.76+, iOS 15.1+, Swift 6.1 / Xcode 16.3+, Android API 26+,
Java 17, Kotlin 1.9.24+ and an HTTPS IAPStack deployment. The runnable sample uses React Native 0.84.1
with the New Architecture. This library uses RN's legacy module interop, not a
codegen TurboModule. Rebuild native apps after installing; Expo Go cannot load
this native module (use a development build/prebuild).

Autolinking registers `IAPStackStore` on both platforms. On Android, add
`https://developer.huawei.com/repo/` for the `com.huawei.*` group to your app's
Gradle repositories (`dependencyResolutionManagement` when enabled, otherwise
the root `allprojects.repositories` block; see the runnable example). Configure
AppGallery's app ID/signing certificate and Play Console products as described
in the [Android SDK](../android/README.md). StoreKit products and bundle identity
must match App Store Connect. Only non-consumables and subscriptions are supported.

## Purchase flow

Authenticate your user on your trusted backend. That backend selects their
external customer ID and mints a short-lived customer bearer using the durable
application bearer. Only the customer bearer enters the app; keep it in memory.
Never bundle the application bearer or persist customer bearers in AsyncStorage,
Keychain, preferences, logs, analytics, or crash reports.

For hosts implementing the example `/session` contract, the optional
`IapStackStore.requestCustomerSession(httpsEndpoint, testerLoginToken)` helper
performs an HTTPS POST with a separate login bearer through the Swift and Kotlin
SDKs' session loaders. They reject redirects, limit the response to 16 KiB and the
request to 15 seconds without progress or 20 seconds overall, and return a
validated session with an `expiresAt` date. Pass a user authentication credential,
never the durable application bearer. Other host login flows can use your existing
authenticated session transport.

```ts
import {IapStackStore} from '@iapstack/react-native/store';

// Subscribe before configure, including pending purchases completed outside the app.
const subscription = IapStackStore.addListener(event => {
  if (event.type === 'verified') {
    showAccess(event.result.entitlements.some(e => e.key === 'premium' && e.grantsAccess));
  } else {
    showRecoverableError(event.code); // No raw evidence or bearer in events.
  }
});

const session = await authenticatedHostSession();
await IapStackStore.configure({
  storefront: 'apple', // 'google_play' or 'huawei' on Android
  baseUri: session.base_url,
  applicationId: session.application_id,
  customerToken: session.token,
  externalCustomerId: session.external_customer_id,
  productKinds: {premium_monthly: 'subscription'},
});
const available = await IapStackStore.isAvailable();
const query = await IapStackStore.queryProducts(['premium_monthly']);
// Display query.products and let the user choose; Play offers have distinct selectionKey values.
const result = await IapStackStore.purchase(query.products[0].selectionKey);
if (result) showAccess(result.entitlements.some(e => e.grantsAccess));
const restored = await IapStackStore.restore();
const snapshot = await IapStackStore.getEntitlements();
// Sign-out / account switch / token renewal:
await IapStackStore.dispose();
subscription.remove();
```

Apple customer IDs must be lowercase UUIDs; Play IDs must be at most 64
characters. For one cross-platform identity, use a host-owned lowercase UUID.
The catalog explicitly selects the server's product verification route.

Apple/Huawei `purchase()` resolve after server verification. Apple returns
`null` for approval-pending transactions. Google Play resolves `null` after
opening checkout and reports verification/cancellation/failure through the
listener. Always grant access from verified `entitlements`, never from checkout
success. Play acknowledgement remains server-owned; Apple transactions are
finished only after successful verification by the Swift companion.

`restore()` verifies bounded, customer-bound batches in native code. Refresh
entitlements when the app resumes. Restore after a network failure, cancelled
session, missed update, or process restart. When a bearer expires, authenticate
again, `dispose()`, then configure with a fresh bearer and restore. Configuration
rejects an existing session to prevent accidental customer switching. In-flight
work from a disposed native session cannot emit results into the next session.

For Huawei sign-in errors, call `resolveHuaweiEnvironment()` from an explicit
user action. HMS activity results are forwarded automatically. Activity destruction
invalidates the HMS session; obtain/configure a new session and restore.
`isAvailable() === false` disables purchasing, not entitlement lookup or restore.

## HTTP-only client

```ts
import {IapStackClient, IapStackConfig, googlePlayEvidence} from '@iapstack/react-native';
const client = new IapStackClient(new IapStackConfig({
  baseUri: session.base_url,
  applicationId: session.application_id,
  customerToken: session.token,
}));
const result = await client.verifyPurchase({
  externalCustomerId: session.external_customer_id,
  claimedProducts: ['premium_monthly'],
  evidence: googlePlayEvidence({purchaseToken: exactNativeToken, productKind: 'subscription'}),
});
```

The React Native export uses the Swift/Kotlin HTTP clients, including their
bounded response bodies, retries, timeouts and redirect rejection. RN's ordinary
`fetch` implementation does not reliably enforce redirect policy. The default
Node/test export remains fetch-based and requests `redirect: 'error'`; do not
bypass Metro's `react-native` condition on mobile. Errors retain API code,
status, retryability and request ID while omitting provider evidence from native
error messages. HTTP verification alone does not finish StoreKit transactions;
prefer `IapStackStore` for complete native purchase flows.

Evidence helpers (`appleEvidence`, `googlePlayEvidence`, `huaweiEvidence`) preserve
exact signed strings. They assemble only the HTTP envelope and are not purchase
collectors. Native store APIs never expose signed evidence to JavaScript.

## Runnable examples and checks

The [example](example/README.md) contains checked-in iOS and Android applications,
trusted-host setup, purchase/restore UI and store sandbox steps. Store credentials,
App Store/Play/AppGallery sandbox accounts and real test purchases are required
for real-store end-to-end evidence; compilation and mocked tests do not assert
that a purchase was made.

```sh
bun install --frozen-lockfile
bun run typecheck
bun run test
bun run build
bun run prepare:native
npm pack
```

`prepack` typechecks/builds JS and copies the canonical `sdk/ios` and
`sdk/android` companion source into the tarball with the Apache license. Generated
`native/` snapshots are intentionally not committed; run `prepare:native` before
native development. Consumers need only the npm tarball. Do not additionally
link the standalone native SDKs in the same app; their sources are already bundled.

Integration references: [React Native native module interop](https://reactnative.dev/docs/legacy/native-modules-intro),
[Android native modules](https://reactnative.dev/docs/0.84/legacy/native-modules-android),
and [integration with existing apps](https://reactnative.dev/docs/integration-with-existing-apps).
