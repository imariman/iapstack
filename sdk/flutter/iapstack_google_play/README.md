# IAPStack Google Play Flutter SDK

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
  iapstack:
    path: ../iapstack
  iapstack_google_play:
    path: ../iapstack_google_play
```

The Android host must use API level 24 or newer and complete the standard Play
Console, product, testing-track, and Play Billing setup for its package.

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
query. Subscription base plans and offers are returned as separate
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

Restore queries current subscription and in-app ownership, requires the exact
account binding on every returned purchase, deduplicates purchase tokens, and
sends IAPStack restore batches of at most 100:

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
plugin models. The Android app under [`example`](example/README.md) is the
manual Play Console internal-testing harness.
