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
leaves the transaction unfinished so StoreKit can redeliver it.

## Restore

`restorePurchases` calls App Store sync, reads StoreKit 2 transaction history,
deduplicates transaction IDs, and sends bounded batches to IAPStack. Each
restored transaction must contain the expected `appAccountToken` UUID.

See `example/` for an iOS simulator harness with a checked-in StoreKit
Configuration file.
