# Huawei sandbox and Cloud Debugging example

This Android example is the manual device harness for IAPStack release
candidates. It never writes the customer session or purchase payloads to local
storage, logs, or analytics.

Use AppGallery Connect **Cloud Debugging**, not Cloud Testing, for the interactive
purchase gate. Cloud Debugging provides a remote real Huawei device; Cloud
Testing is useful later for automated compatibility and performance checks but
cannot prove the checkout, tester, renewal, restore, or refund flows.

## One-time setup

1. Register the Android package name in AppGallery Connect. The checked-in
   example defaults to `com.iapstack.example.iapstack_huawei_example`. The
   helper passes your configured package to Gradle through
   `IAPSTACK_HUAWEI_PACKAGE_NAME`; the Kotlin namespace does not need to change.
2. Enable Huawei IAP and configure the non-consumable/subscription product IDs.
3. Download `agconnect-services.json` and place it at `android/app/`. The file
   is gitignored. A release build now fails closed when this file is absent.
4. Copy the local operator template and edit its non-secret identifiers:

   ```sh
   cp scripts/huawei-sandbox.env.example scripts/huawei-sandbox.env
   ```

5. Use the signing certificate registered with AppGallery Connect. For a new
   sandbox-only app, let the helper create a dedicated key with a random
   password stored only in macOS Keychain:

   ```sh
   ./scripts/huawei-sandbox keychain generate-signing-key
   ```

   The command refuses to overwrite an existing keystore or `key.properties`.
   Back up the sandbox key and retain access to its Keychain entries for the
   lifetime of this AppGallery test app.

   For an existing keystore, copy and edit the signing template instead:

   ```sh
   cp sdk/flutter/iapstack_huawei/example/android/key.properties.example \
     sdk/flutter/iapstack_huawei/example/android/key.properties
   ./scripts/huawei-sandbox keychain set-signing-passwords
   ```

   Both local configuration files, `agconnect-services.json`, and keystores are
   gitignored. `key.properties` must contain only the absolute keystore path and
   alias; plaintext passwords are rejected.
6. Store the IAPStack application key in macOS Keychain. If an existing
   AppGallery keystore was supplied, its signing passwords were stored in the
   previous step:

   ```sh
   ./scripts/huawei-sandbox keychain set-application-key
   ./scripts/huawei-sandbox keychain status
   ```

7. Add a dedicated, non-admin HUAWEI ID as the sandbox tester. Do not use the
   developer-account owner for checkout.
8. Configure the public IAP V2 callback URL documented in
   `docs/releases/v0.1.0-rc.1-huawei.md`.

## Cloud Debugging smoke test

Run the local checks once, then warm the release compiler cache without minting
a real customer session:

```sh
./scripts/huawei-sandbox preflight
./scripts/huawei-sandbox warmup
```

The preflight prints the certificate SHA-256 fingerprint so it can be compared
with AppGallery Connect without revealing a password. The fingerprint is also
available before the IAPStack application key exists:

```sh
./scripts/huawei-sandbox signing-fingerprint
```

Then:

1. Open AppGallery Connect's [Cloud Debugging](https://developer.huawei.com/consumer/en/agconnect/cloud-adjust/)
   page and reserve a 30-minute HMS-capable device.
2. Sign into the dedicated tester with Huawei's remote HUAWEI ID login flow.
   Never paste developer/admin credentials into the remote device.
3. Build the selected product immediately after the device is ready:

   ```sh
   ./scripts/huawei-sandbox build subscription
   # For the lifetime product:
   ./scripts/huawei-sandbox build non-consumable
   ```

4. Upload the private temporary APK path printed by the helper. Its customer
   session lasts only 15 minutes and is compiled into that temporary artifact;
   upload it immediately and delete it after the session.
5. First perform a go/no-go check: the app must report Huawei IAP available, the
   product purchasable, `Test account: Eligible`, and `Sandbox APK: Eligible`.
   Checkout must also show Huawei's sandbox notice. Stop before confirming if
   any condition is absent because a real charge may otherwise occur.

AppGallery Connect documents [reserving a Cloud Debugging device](https://developer.huawei.com/consumer/en/doc/appgallery-connect-Guides/agc-clouddebug-debugequip-0000001122212029)
and its [login/time-limit behavior](https://developer.huawei.com/consumer/jp/doc/AppGallery-connect-Guides/agc-clouddebug-faq-0000001058073610).
Huawei currently describes a free debugging allowance and 30-minute, one-hour,
or two-hour reservations; confirm the portal values when starting the session.

If HMS Toolkit exposes the remote device through ADB and `flutter devices`
lists it as a physical Android device, skip the upload path and run:

```sh
./scripts/huawei-sandbox run subscription
./scripts/huawei-sandbox run non-consumable
```

Each build/run mints a fresh 15-minute customer session. The temporary runtime
file and token-bearing Flutter build tree are removed when the command exits;
the upload APK is intentionally retained only at the printed private `/tmp`
path so the operator can upload it. Signed builds disable the persistent Gradle
daemon so signing passwords are not left in a long-running build process.

## Required observations

The app calls `isEnvReady`, `isSandboxActivated`, and `obtainProductInfo` at
startup. Do not start a purchase unless Huawei IAP is available, the configured
product is shown as purchasable, the screen reports both `Test account: Eligible`
and `Sandbox APK: Eligible`, and Huawei checkout displays its sandbox notice.
Purchase remains disabled until every condition is active.

Exercise purchase, restore, and refresh while recording only server request IDs.
Huawei shortens subscription periods in the sandbox; verify the current timing
in the provider documentation before execution. Use a fresh tester/product state
where renewal history matters, and never copy signed purchase data, tokens,
application credentials, or remote-login material into screenshots or notes.

Follow the complete [v0.1.0-rc.1 release runbook](../../../../docs/releases/v0.1.0-rc.1.md)
for cancellation, expiration, grace, refund, revocation, duplicate, negative,
reconciliation, and production webhook assertions.
