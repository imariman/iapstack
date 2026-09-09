# IAPStack StoreKit 2 sandbox harness

This iOS-only example exercises the Apple companion with real App Store sandbox
transactions. The shared Runner scheme intentionally does not select the repository's
`ios/Runner/Configuration.storekit`; this lets a development-signed build receive
App Store-signed JWS transactions that the IAPStack backend can verify. The local
StoreKit file remains available for isolated Xcode UI tests, but those Xcode-signed
transactions do not satisfy the backend release gate.

The harness supports:

- localized subscription and non-consumable price lookup;
- purchase verification and transaction completion after the server commit;
- ordinary restore and an automated consecutive double-restore idempotency check;
- Apple's subscription-management sheet for disabling auto-renew;
- Apple's sandbox refund-request sheet for the configured subscription;
- current IAPStack entitlement and projection-version display.

## One-time operator setup

From the repository root, create the gitignored identifier file:

```sh
cp scripts/apple-sandbox.env.example scripts/apple-sandbox.env
```

Edit it with the real API origin, IAPStack application ID, canonical customer UUID,
App Store product IDs, bundle ID, issuer ID, and In-App Purchase key ID. It must not
contain private keys or bearer tokens.

Store the dedicated IAPStack sandbox application bearer in macOS Keychain:

```sh
./scripts/apple-sandbox keychain set-application-key
```

For automated App Store Server Notifications V2 tests, also import the matching
PKCS#8 `.p8` key once:

```sh
./scripts/apple-sandbox keychain set-app-store-key /absolute/path/AuthKey_ABCDEFGHIJ.p8
./scripts/apple-sandbox keychain status
```

The helper never prints either private value. The durable application bearer is used
only on the Mac to mint a fresh 15-minute customer session. The temporary Flutter
define file contains only non-secret configuration. The helper then builds and
installs the app without launching it, starts it once through Xcode's authenticated
paired-device channel, and supplies the customer session only to that process. This
is not a Flutter tool session: there is no hot reload or `flutter run` log stream.
Wait until the helper reports that the app has launched before interacting. The
native host removes the value from its environment immediately and allows Dart to
consume it once; it is not embedded in the application artifact.

## Run on a physical iPhone

Use one command for a normal manual session:

```sh
./scripts/apple-sandbox run
```

The runner selects the first connected physical iOS device, or the explicit
`IAPSTACK_IOS_DEVICE_ID` in `scripts/apple-sandbox.env`. `IAPSTACK_APPLE_BUNDLE_ID`
must match the installed Runner (`com.imariman.iapstack.example` in this example).
A new customer session is created on every run. Neither the application bearer nor
the short-lived customer session is embedded in the iPhone build. The customer
session necessarily remains in app memory while the harness makes authenticated
requests, so treat a running development process as sensitive until it expires.

To install and automatically perform two consecutive restore submissions with
distinct request IDs on that first launch:

```sh
./scripts/apple-sandbox restore-idempotency
```

The result is a pass only when both restores contain owned transactions, return the
same number of results, and preserve every entitlement's access, reason, effective
period, and projection version. The same action is available as **Run double-restore
idempotency test** in the app.

## Notifications V2 test

Configure this exact sandbox Version 2 URL in App Store Connect first:

```text
https://<public-iapstack-origin>/v1/providers/apple/projects/<project_id>/applications/<application_id>/notifications
```

Then send Apple's real sandbox `TEST` notification and poll its delivery status:

```sh
./scripts/apple-sandbox notification-test
```

The helper reads the `.p8` value from Keychain over stdin, creates five-minute ES256
authorization JWTs, calls Apple's sandbox `Request a Test Notification` endpoint, and
polls `Get Test Notification Status`. It reports only attempt time and result; the test
notification token and signed payload are never printed. Success requires Apple's
recorded result to be `SUCCESS`, meaning the configured IAPStack endpoint returned
HTTP 200.

## Subscription lifecycle sequence

Use a Sandbox Apple Account on the device and set its renewal rate to **Every 3
Minutes** for the shortest monthly-subscription cycle.

1. Purchase the subscription and confirm access is allowed with an authoritative end
   date and renewal enabled.
2. Wait for the accelerated renewal notification, then run **Restore App Store
   purchases** to force an authoritative status query. Confirm the effective period
   and projection version advance once.
3. Run **Manage subscription / disable renewal**, turn off auto-renew in Apple's sheet,
   then restore again. Confirm access remains allowed through the current period while
   renewal is disabled.
4. Wait past the accelerated period, restore again, and confirm access becomes denied
   for expiration.
5. For a fresh subscription state, run **Request sandbox subscription refund**, choose
   any reason other than the special decline case, and submit. Wait for the `REFUND`
   notification, restore again, and confirm refunded access is denied.

Use a fresh sandbox account or clear its purchase history between scenarios that would
otherwise contaminate each other. Never record customer sessions, application bearers,
`.p8` contents, JWS values, test-notification tokens, or transaction identifiers in
release evidence.
