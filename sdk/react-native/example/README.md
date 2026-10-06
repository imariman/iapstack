# React Native store example

One checked-in React Native 0.84.1 app runs on iOS (StoreKit 2) and Android
(Play Billing or Huawei). It authenticates with a trusted host, keeps the short-lived
customer session in memory, queries offers, purchases, restores and displays
server entitlements. Sign out cancels native work and releases the bearer.

## Prepare

From `sdk/react-native`:

```sh
bun install --frozen-lockfile
bun run build
bun run prepare:example
cd example
bun install --frozen-lockfile
bun run typecheck
```

Create store products matching the subscription ID entered in the app and map
them to an entitlement in IAPStack. Set your registered identifiers in
`android/app/build.gradle` and the Xcode target's signing settings. The default
`com.iapstack.reactnative.example` Android identifier is a sample, not a registered
Play/AppGallery app. Configure the Huawei app ID in Android manifest metadata
for AppGallery builds; never put server credentials in it. Apple identities
must use a lowercase UUID for the store account binding.

## Trusted host

Reuse the single-tester host in `sdk/android/example/trusted-host/server.py`.
It authenticates a separate tester login token, selects `EXAMPLE_EXTERNAL_CUSTOMER_ID`
on the server and mints a short-lived customer token. It never accepts a customer
identity from the client. Set these on your server only:

- `IAPSTACK_BASE_URL` — HTTPS IAPStack deployment.
- `IAPSTACK_APPLICATION_ID` and `IAPSTACK_APPLICATION_TOKEN` — application scope and durable bearer.
- `EXAMPLE_EXTERNAL_CUSTOMER_ID` — lowercase UUID shared with StoreKit/Play/Huawei.
- `EXAMPLE_LOGIN_TOKEN` — a separate tester authentication credential.

Run the Python host behind your HTTPS reverse proxy. Enter its direct `/session`
URL and the tester token in the app. The host response includes `token`,
`expires_at`, `base_url`, `application_id` and `external_customer_id`. The sample
clears the login field after success and never writes either token to storage.
The example's native session bootstrap rejects redirects, permits HTTPS only,
caps responses at 16 KiB and requests at 15 seconds, and rejects expired sessions.
Production apps can use their existing authenticated host login/session transport.
Never enter the durable application bearer in the app.

## Build without launching devices

```sh
# iOS, from example/ios:
pod install
xcodebuild -workspace IAPStackExample.xcworkspace -scheme IAPStackExample \
  -configuration Debug -sdk iphonesimulator -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath /tmp/iapstack-rn-ios CODE_SIGNING_ALLOWED=NO build

# Android, from example/android (set ANDROID_HOME / JAVA_HOME as needed):
./gradlew :app:assembleDebug -PreactNativeArchitectures=arm64-v8a
```

For interactive testing start Metro with `bun start`, then use Xcode or Android
Studio to install on your selected existing test device. Do not use production
accounts or include purchase payloads in screenshots/logs.

## Sandbox flow

1. Use a store-distributed sandbox/internal-testing build with matching signing,
   package/bundle identity, registered tester and products. HMS requires a capable
   Huawei device and correctly configured AppGallery identity.
2. Sign in to the trusted host; choose the Android storefront before connecting.
3. Load products, inspect the localized offer and purchase. Play completion is
   delivered through native purchase events; Apple/Huawei verification returns
   directly. Entitlements, not checkout success, determine access.
4. Restore and refresh entitlements. Retry after restarting or reconnecting to
   recover transactions whose verification was interrupted.
5. Sign out, obtain a fresh session, and verify that another customer cannot
   restore the previous customer's purchases. Expired sessions should ask for
   sign-in; use Restore after renewal.
6. Exercise pending approval, cancellation, network interruption, subscription
   expiry/refund/revocation and store notification delivery. Save only secret-free
   evidence tied to the candidate commit as required by the release runbook.

These steps require external store setup; CI builds and mocked unit tests do not
replace the real-store release gates.

The native scaffolding is derived from the MIT-licensed official
`@react-native-community/template` 0.84.1; see `TEMPLATE-LICENSE`.
