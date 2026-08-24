# IAPStack Dart/Flutter SDK

Provider-neutral client for the application-facing IAPStack v1 API. The SDK
keeps the application bearer in memory, applies bounded timeouts and retries,
and exposes stable API error codes with request IDs.

```dart
final client = IapStackClient(
  IapStackConfig(
    baseUri: Uri.parse('https://iap.example.com'),
    applicationId: 'my-application',
    applicationKey: const String.fromEnvironment('IAPSTACK_APPLICATION_KEY'),
  ),
);

final snapshot = await client.getEntitlements('customer-123');
final premium = snapshot.entitlements.where((item) => item.key == 'premium');
final hasPremium = premium.any((item) => item.grantsAccess);
```

Do not persist the application key in `SharedPreferences`, source control, logs,
or analytics. Inject it using the host application's runtime configuration. A
mobile application bearer must be treated as an application identifier rather
than a user secret; customer authorization remains the host application's
responsibility.

Huawei apps should also depend on the sibling `iapstack_huawei` package, which
collects exact signed HMS purchase evidence and maps it into these contracts.
