# IAPStack Dart/Flutter SDK

Provider-neutral client for the application-facing IAPStack v1 API. The SDK
keeps a short-lived customer bearer in memory, applies bounded timeouts and retries,
and exposes stable API error codes with request IDs.

```dart
final client = IapStackClient(
  IapStackConfig(
    baseUri: Uri.parse('https://iap.example.com'),
    applicationId: 'my-application',
    customerToken: customerSession.token,
  ),
);

final snapshot = await client.getEntitlements('customer-123');
final premium = snapshot.entitlements.where((item) => item.key == 'premium');
final hasPremium = premium.any((item) => item.grantsAccess);
```

Authenticate the user in your trusted host backend, then call
`POST /v1/applications/{application_id}/customer-sessions` there with the durable
application bearer. Return only the resulting short-lived customer token to the
mobile application. Never ship the durable application bearer in a mobile binary
or persist either bearer in `SharedPreferences`, source control, logs, or analytics.

Huawei apps should also depend on the sibling `iapstack_huawei` package, which
collects exact signed HMS purchase evidence and maps it into these contracts.
