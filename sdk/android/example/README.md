# Native Android store harness

This app exercises query, offer selection, purchase, restore, and server
entitlements through the real Google Play and HMS adapters. It is a manual
testing harness, not a production login UI. Debug builds need no credentials;
actual store flows require your store configuration and eligible tester account.

## Trusted host

`trusted-host/server.py` is a single-tester backend example using Python's standard
library. Run it on your trusted server behind an HTTPS reverse proxy. It binds
only to `127.0.0.1:8099`. Supply these variables through your server's secret manager
or an ignored local environment file:

- `IAPSTACK_BASE_URL`: HTTPS API origin (or path prefix)
- `IAPSTACK_APPLICATION_ID`: the IAPStack application for the selected store
- `IAPSTACK_APPLICATION_TOKEN`: durable application bearer; **host only**
- `EXAMPLE_EXTERNAL_CUSTOMER_ID`: opaque ID bound to this authenticated tester
- `EXAMPLE_LOGIN_TOKEN`: a separate, temporary host login token for this tester

```sh
python3 trusted-host/server.py
```

Enter the HTTPS `/session` URL and temporary **host login token** in the app at
runtime. The host authenticates it, selects the configured customer identity,
and calls IAPStack's customer-session endpoint. Only the short-lived session
returns to Android. Requests cannot select another customer. The app and host
do not log or persist tokens. The app disables saved state, autofill, backups,
and screenshots for the credential form; it clears the login field after use.
Production hosts should replace the single tester token with their existing
user authentication, authorization, rate limits, and account-to-customer lookup.
Never enter the IAPStack application bearer in the Android app.

The response has `base_url`, `application_id`, `external_customer_id`, `token`,
and `expires_at`. Close/reopen the harness to obtain a new session after expiry
or change the store/customer. Rotate the temporary host login token after testing.

## Local store setup

Copy `store.properties.example` to **ignored** `store.properties`. Configure the
package identity registered with your store and, for HMS, the AppGallery app ID.
The manifest's `com.huawei.hms.client.appid` metadata provides HMS configuration;
this example does not need the AG Connect Gradle plugin. Register the certificate
fingerprint for the signing key in AppGallery Connect and enable IAP.
Keep any downloaded `agconnect-services.json` local (also ignored).

For signed release builds, set an absolute `storeFile` and `keyAlias` in the local
properties file, and supply passwords only via `IAPSTACK_ANDROID_STORE_PASSWORD`
and `IAPSTACK_ANDROID_KEY_PASSWORD`. Keep keystores outside git. No tester credentials,
service account files, durable application bearer, or signing password belongs in
Gradle properties, BuildConfig, source, or resources.

```sh
cd sdk/android
./gradlew :example:assembleDebug
# With local store identity and release signing configured:
./gradlew :example:bundleRelease   # Google Play internal testing
./gradlew :example:assembleRelease # Huawei sandbox APK
```

Google Play: upload the signed bundle to your internal testing track, configure
license testers, opt in and install from Play with the test account. Configure
matching products/base plans/offers and the IAPStack catalog. The server must
have Android Publisher access for post-commit acknowledgement.

Huawei: enable IAP for the registered package, configure products and sandbox
testers in AppGallery Connect, and install the correctly signed APK on an HMS
Core device. Sign in with the sandbox tester. Use **Resolve Huawei sign-in** when
needed, then **Check Huawei sandbox**; both account and APK must be eligible
before test checkout. Configure the matching IAPStack Huawei credentials on the server.

## Device acceptance run

1. Select the store, product ID and subscription/non-consumable kind; start a customer session.
2. Query products and select the displayed offer. Check local price/base-plan selection.
3. Cancel checkout and confirm the app stays usable and grants no access.
4. Complete a test purchase. Read entitlements and confirm the server grants access.
5. For Play pending payment, confirm no access until completion; return to the app
   and restore. Confirm acknowledgement is performed by the server.
6. Restart, obtain a fresh session for the same customer, and restore. Exercise both
   a non-consumable and subscription; confirm the server returns the same entitlement.
7. For Huawei, test signed-out/unsupported environment errors and multi-page owned
   results when available. Confirm a different customer cannot restore the purchase.
8. Interrupt connectivity/checkout and recreate the Activity; restore after recovery.

CI checks SDK calls, failure handling, payload preservation, and APK/AAR builds.
Live Play/internal-testing and Huawei sandbox results require your configured
devices and accounts and are not claimed by unit tests or the Flutter release gate.
