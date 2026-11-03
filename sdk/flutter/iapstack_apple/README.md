# IAPStack Apple companion

`iapstack_apple` connects Flutter's official StoreKit 2 implementation to the
provider-neutral `iapstack` client. It supports product queries, purchases,
transaction listening, restore, signed JWS submission, and authoritative
entitlement refreshes for non-consumables and auto-renewable subscriptions.

The companion requires the IAPStack external customer ID to be a canonical
lowercase UUID. The same UUID is sent to StoreKit as `appAccountToken` and to
IAPStack as `external_customer_id`.

## Purchase flow

Create the listener before querying products so unfinished transactions are not
missed. Never persist or log the signed transaction JWS or customer session.

```dart
final backend = IapStackClient(
  IapStackConfig(
    baseUri: Uri.parse('https://iap.example.com'),
    applicationId: 'ios-production',
    customerToken: runtimeCustomerToken,
  ),
);
final apple = AppleIapStack(
  client: backend,
  productKinds: const <String, AppleProductKind>{
    'premium_monthly': AppleProductKind.subscription,
    'premium_lifetime': AppleProductKind.nonConsumable,
  },
);

final subscription = apple.purchaseUpdates.listen((purchase) async {
  if (purchase.canVerify) {
    await apple.verifyPurchase(
      externalCustomerId: customerUuid,
      purchase: purchase,
    );
  }
});
```

`verifyPurchase` finishes a pending StoreKit transaction only after IAPStack
returns a successful authoritative verification. A provider or network failure
leaves the transaction unfinished so StoreKit can redeliver it. Verify each
update on the `AppleIapStack` whose `purchaseUpdates` delivered it, or on one
that shares its `platform`. A different platform has no StoreKit handle for the
transaction, so completion throws `AppleIapStackException` with code
`transaction_not_finishable` after IAPStack has verified the purchase.

## Restore

`restorePurchases` calls App Store sync, reads StoreKit 2 transaction history,
deduplicates transaction IDs, and sends bounded batches to IAPStack. Rows
without the expected `appAccountToken`, rows for another customer, and unknown
products are counted on `RestoreResult.skipped` and are not submitted. Another
customer's transactions are never verified. If a later batch fails, the
original IAPStack error carries `committedRestoreResults`.

Rows that StoreKit has not finished yet are finished after IAPStack accepts the
batch that carried them. Rows in a failed batch, and in the batches after it,
stay unfinished, and the next restore submits them again. If StoreKit cannot
finish an accepted row, restore throws that `AppleIapStackException`.

See `example/` for an iOS simulator harness with a checked-in StoreKit
Configuration file.
