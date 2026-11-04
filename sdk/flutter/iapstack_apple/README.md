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
leaves the transaction unfinished so StoreKit can redeliver it. StoreKit
finishes a transaction by its id, so any `AppleIapStack` can verify an update.
A pending update with no transaction id cannot be finished, so `verifyPurchase`
throws `AppleIapStackException` with code `transaction_not_finishable` before
it contacts IAPStack.

## Restore

`restorePurchases` runs the plugin's StoreKit 2 restore, which re-emits current
entitlements on `purchaseUpdates` and does not call `AppStore.sync()`. It then
reads StoreKit 2 transaction history, current entitlements, and unfinished
transactions, deduplicates transaction IDs, and sends bounded batches to
IAPStack. Rows without the expected `appAccountToken`, rows for another
customer, and unknown products are counted on `RestoreResult.skipped` and are
not submitted. Another customer's transactions are never verified. If a later
batch fails, the original IAPStack error carries `committedRestoreResults`.

`in_app_purchase_storekit` returns history rows without a JWS. Restore takes
the JWS from the current entitlements and unfinished reads, so every current
entitlement carries one. Other finished rows, such as earlier subscription
renewals and expired purchases, have no JWS. They are counted on
`RestoreResult.skipped.unsigned` and are not submitted. The Swift SDK submits
them. A current entitlement that StoreKit cannot verify is left out and does
not fail the restore, as in the Swift SDK.

Rows that StoreKit has not finished yet are finished after IAPStack accepts the
batch that carried them. Rows in a failed batch, and in the batches after it,
stay unfinished, and the next restore submits them again. If StoreKit cannot
finish an accepted row, restore still finishes the other accepted rows. When
every batch was accepted, it then throws `IapStackIncompleteRestoreException`
with `committedRestoreResults` and the completion error as `cause`. When a
batch failed, the original IAPStack error is thrown as before.

See `example/` for an iOS simulator harness with a checked-in StoreKit
Configuration file.
