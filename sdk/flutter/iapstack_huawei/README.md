# IAPStack Huawei Flutter SDK

Huawei AppGallery companion for the provider-neutral `iapstack` package. It
uses Huawei's official `huawei_iap` plugin, preserves exact signed purchase
strings, handles owned-purchase pagination, deduplicates provider results, and
batches restore requests to the IAPStack limit of 100.

```dart
final backend = IapStackClient(
  IapStackConfig(
    baseUri: Uri.parse('https://iap.example.com'),
    applicationId: 'my-application',
    applicationKey: const String.fromEnvironment('IAPSTACK_APPLICATION_KEY'),
  ),
);
final huawei = HuaweiIapStack(client: backend);

final verification = await huawei.purchaseAndVerify(
  externalCustomerId: 'customer-123',
  productId: 'premium_monthly',
  productKind: HuaweiProductKind.subscription,
);

final restored = await huawei.restorePurchases(
  externalCustomerId: 'customer-123',
);
```

The host Android app must complete Huawei's official HMS IAP and AppGallery
Connect setup, including `agconnect-services.json`. The SDK intentionally does
not consume purchases because IAPStack v0.1 excludes consumables.
