# IAPStack Google Play Flutter SDK

## Breaking changes in 0.1.0-sdk.2

`GooglePlayPurchase.canVerify` is false for `GooglePlayPurchaseStatus.pending`.
A listener that verified every update, including pending ones, now skips them.
`verifyPurchase` still submits a pending purchase when you call it. Do not
unlock access until `grantsAccess` is true.

Google Play Billing companion for the provider-neutral `iapstack` package. It
uses Flutter's official `in_app_purchase` implementation, maps Play Billing
products and purchase updates into redacted models, binds checkout to an opaque
application customer, and submits only the purchase token required by IAPStack.

The current slice supports subscriptions and durable non-consumable one-time
products. Consumables, multi-product purchases, subscription replacement, and
client-owned acknowledgement are intentionally outside the package contract.

## Install

Use the provider-neutral package and this companion together:

```yaml
dependencies:
  iapstack: 0.1.0-sdk.3
  iapstack_google_play: 0.1.0-sdk.3
```

Use the exact version: `^0.1.0` does not match an `-sdk.N` prerelease.
Upgrade all IAPStack packages together, because each companion depends on the
exact same `iapstack` version.

The Android host must use API level 24 or newer and complete the standard Play
Console, product, testing-track, and Play Billing setup for its package.

## Dependency on in_app_purchase_android

This package requires `in_app_purchase_android` `>=0.5.2 <0.5.6`, every release
from 0.5.2 to 0.5.5. The cap exists because two things the package needs are
outside the plugin's semver:

- Product queries read Play Billing response codes through
  `InAppPurchaseAndroidPlatform.billingClientManager`, a `@visibleForTesting`
  field. The plugin's own `queryProductDetails` turns a non-OK result into an
  empty catalog.
- Purchase updates recover the Billing result from `IAPError.message`, which
  the plugin sets to `BillingResponse.<name>` for a failed purchase.

What host apps can expect: pub always resolves a release this package was
tested against. An app that needs a later `in_app_purchase_android` gets a pub
version conflict until the cap moves, instead of a build against a release
nobody checked. Raising the cap keeps the 0.5.2 lower bound.

How a bump is verified:

1. When a release ships, Dependabot opens a pull request for this directory.
   Because the package is `publish_to: none`, Dependabot rewrites the
   constraint as a caret range, for example `^0.5.6`.
2. CI resolves the new release (the package has no lockfile) and runs
   `test/in_app_purchase_android_drift_test.dart` with the rest of the suite.
   These tests drive the real plugin: `registerPlatform` must install an
   `InAppPurchaseAndroidPlatform`, `billingClientManager` must be the manager
   the plugin queries Play with, and every non-OK purchase result must reach
   `GooglePlayPurchase.errorCode` as the Android SDK code. A guard test fails
   while the constraint is a caret range or ends on a minor boundary.
3. If the drift tests pass, read the plugin changelog for behaviour changes and
   cap just above the checked release in the same pull request, for example
   `>=0.5.2 <0.5.7` after checking 0.5.6. Run the suite once after
   `flutter pub downgrade` in a scratch copy to confirm the lower bound still
   works.
4. If they fail, keep the cap and adapt `lib/src/plugin_platform.dart` before
   the next bump.

A supported route to Billing codes, either an upstream plugin API or a small
native plugin like the Kotlin SDK's, would remove the need for the cap.

## Initialize and listen early

Create one backend client, define the expected Play product kind for every
provider product ID, and subscribe to purchase updates during application
startup. The account identifier must be stable for the application, opaque,
non-PII, and the exact value used as the IAPStack `external_customer_id`.

```dart
final backend = IapStackClient(
  IapStackConfig(
    baseUri: Uri.parse('https://iap.example.com'),
    applicationId: 'my-google-play-application',
    customerToken: runtimeCustomerToken,
  ),
);

final googlePlay = GooglePlayIapStack(
  client: backend,
  productKinds: const <String, GooglePlayProductKind>{
    'premium_monthly': GooglePlayProductKind.subscription,
    'premium_lifetime': GooglePlayProductKind.nonConsumable,
  },
);

final purchaseSubscription = googlePlay.purchaseUpdates.listen((purchase) async {
  if (purchase.status == GooglePlayPurchaseStatus.cancelled ||
      purchase.status == GooglePlayPurchaseStatus.failed) {
    // Play may report these without a purchase row, so purchaseToken and
    // productIds can be empty. errorCode names the Billing result, for
    // example purchase_cancelled or billing_error_7 (ITEM_ALREADY_OWNED).
    renderCheckoutProblem(purchase.status, purchase.errorCode);
    return;
  }
  if (!purchase.canVerify) {
    // Pending payments are not access. Wait for purchased.
    return;
  }
  final result = await googlePlay.verifyPurchase(
    externalCustomerId: currentOpaqueCustomerId,
    purchase: purchase,
    requestId: currentCheckoutRequestId,
  );
  renderAuthoritativeEntitlements(result.entitlements);
});
```

Keep the subscription alive for the host application's lifetime and close it
when the owning application service is disposed. Do not log a
`GooglePlayPurchase`, its native source data, or backend submissions.

## Query and purchase

Product kinds are checked against the values returned by Play Billing before
checkout can open. Every requested ID must be returned as an offer or explicitly
reported missing, and checkout accepts only an immutable offer from that successful
query. A non-OK Billing result fails the query with the same code as the Android
SDK, for example `billing_error_2` for SERVICE_UNAVAILABLE. Subscription base plans and offers are returned as separate
`GooglePlayProduct` rows with distinct in-memory selection keys.

```dart
if (!await googlePlay.isAvailable()) {
  return;
}

final query = await googlePlay.queryProducts(<String>{
  'premium_monthly',
  'premium_lifetime',
});

await googlePlay.launchPurchase(
  externalCustomerId: currentOpaqueCustomerId,
  product: query.products.first,
);
```

Each product retains Google Play's exact `priceMicros` value. Subscription rows
also expose `basePlanId`, optional `offerId`, offer tags, and the complete ordered
pricing-phase schedule with ISO 8601 periods and recurrence modes. Use localized
formatted values for display; client-side pricing never grants entitlement.

The official plugin sends `externalCustomerId` as Google Play's
`obfuscatedAccountId`. Never pass an email address, Google account, developer
identifier, or other clear-text identity. Generate the opaque binding in the
application's trusted account system, keep it within Google Play's 64-character
limit, and keep it stable across purchase, verification, and restore.

## Restore

Restore queries current subscription and in-app ownership, deduplicates
purchase tokens, and sends IAPStack restore batches of at most 100. Rows for
another customer, an unbound legacy purchase, an unknown product, or more than
one product id are counted on `RestoreResult.skipped` and are not submitted.
Another customer's purchases are never verified. If a later batch fails, the
original IAPStack error carries `committedRestoreResults`.

```dart
await googlePlay.restorePurchases(
  externalCustomerId: currentOpaqueCustomerId,
  requestId: 'restore-session-123',
);

final snapshot = await googlePlay.getEntitlements(
  currentOpaqueCustomerId,
);
```

Never grant access from the device purchase status. Only the entitlement
projection returned by IAPStack is authoritative.

## Acknowledgement ownership

Do not call Flutter's `completePurchase` for flows owned by this companion.
IAPStack acknowledges a completed Google Play purchase through Android
Publisher only after evidence, observation, entitlement projection, and the
outbox event commit durably. Keeping one acknowledgement owner prevents a
client-side success from racing or masking a failed server transaction.

The same ordering applies to authenticated RTDN processing. IAPStack stores the
normalized RTDN as protected reconciliation evidence, resolves its purchase token
to the previously verified customer and product, re-queries Android Publisher, and
only then runs any required acknowledgement action.

If verification fails, leave the purchase unacknowledged and retry the same
token through IAPStack. Duplicate verification and restore submissions are
idempotent. Pending purchases are recorded without granting access and are not
acknowledged until Google reports a completed purchase.

## Testing

The package tests the high-level catalog, account-binding, evidence, batching,
and redaction rules as well as the production bridge to Flutter's official
plugin models. `test/in_app_purchase_android_drift_test.dart` checks the plugin
internals described under
[Dependency on in_app_purchase_android](#dependency-on-in_app_purchase_android).
The Android app under [`example`](example/README.md) is the manual Play Console
internal-testing harness.
