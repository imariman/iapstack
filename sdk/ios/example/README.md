# Swift Apple SDK example (reference)

This directory documents the integration flow for an iOS app:

1. Fetch a short-lived `customerToken` from your trusted host using your app bearer.
2. Create an `IAPStackClient` with `IAPStackConfig`.
3. Use `AppleIAPStack` with a map of configured product IDs and `AppleProductKind`.
4. Convert your canonical lower-case customer UUID to `appAccountToken`.
5. Launch StoreKit flow (`launchPurchase`) and forward emitted `ApplePurchase` rows to
   `verifyPurchase`.

Only keep `signedTransaction` tokens in memory and never persist them.
