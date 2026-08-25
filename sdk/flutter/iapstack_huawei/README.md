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

final sandbox = await huawei.sandboxStatus();
if (!sandbox.isActive) {
  throw StateError('Huawei account and APK are not sandbox eligible');
}

final verification = await huawei.purchaseAndVerify(
  externalCustomerId: 'customer-123',
  productId: 'premium_monthly',
  productKind: HuaweiProductKind.subscription,
  requestId: 'checkout-session-123',
);

final restored = await huawei.restorePurchases(
  externalCustomerId: 'customer-123',
  requestId: 'restore-session-123',
);
```

The host Android app must complete Huawei's official HMS IAP and AppGallery
Connect setup, including `agconnect-services.json`. The SDK intentionally does
not consume purchases because IAPStack v0.1 excludes consumables. The sandbox
status call delegates to Huawei's `isSandboxActivated` API. Treat both the test
account and APK flags as mandatory before opening purchase UI; the example app
enforces that boundary and displays request IDs for release evidence.
