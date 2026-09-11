# IAPStack React Native SDK

Provider-neutral HTTP client for the application-facing IAPStack v1 API. This
package mirrors the Flutter `iapstack` split: verify, restore, entitlements,
retries, timeouts, and request IDs. StoreKit, Play Billing, and HMS evidence
collection stay in the native iOS and Android SDKs; a thin native bridge is
follow-up work once those packages land.

Authenticate the user in your trusted host backend, then mint a short-lived
customer session there with the durable application bearer. Return only that
customer token to the mobile app. Never ship the durable application bearer in
a mobile binary, and never persist the customer bearer.

This package is not published to npm yet. Depend on the repository path
`sdk/react-native`.

## Scope

- API methods for customer-bound verification and entitlement lookup.
- Retry, timeout, and streamed response-size limits aligned with the Flutter client.
- Optional helpers that wrap native-signed strings into the v1 evidence object
  without rewriting the signed payload.

## Usage

```ts
import {
  IapStackClient,
  IapStackConfig,
  PurchaseSubmission,
  googlePlayEvidence,
} from '@iapstack/react-native';

const config = new IapStackConfig({
  baseUri: 'https://iapstack.example.com',
  applicationId: 'my-application',
  customerToken: 'short-lived-customer-session-token',
  timeoutMs: 10_000,
});

const client = new IapStackClient(config);
const payload: PurchaseSubmission = {
  externalCustomerId: 'customer-123',
  claimedProducts: ['premium_annual'],
  evidence: googlePlayEvidence({
    purchaseToken: 'purchase-token-from-your-google-play-module',
    productKind: 'subscription',
  }),
};

const result = await client.verifyPurchase(payload);
const currentEntitlements = await client.getEntitlements('customer-123');
const hasPremium = currentEntitlements.entitlements.some(
  (item) => item.key === 'premium' && item.grantsAccess,
);
```

## Evidence helpers

These helpers only assemble the v1 evidence envelope. Pass the exact native
string or token; do not rebuild StoreKit JWS, Play tokens, or Huawei
`InAppPurchaseData` in JavaScript. `productKind` is required because it selects
the server verification route.

```ts
import { appleEvidence, googlePlayEvidence, huaweiEvidence } from '@iapstack/react-native';

const apple = appleEvidence({
  signedTransaction: '<StoreKit compact JWS>',
  productKind: 'non_consumable',
});

const play = googlePlayEvidence({
  purchaseToken: '<Google Play purchase token>',
  productKind: 'subscription',
});

const huawei = huaweiEvidence({
  purchaseData: '<Huawei purchase JSON string>',
  signature: '<Huawei signature>',
  productKind: 'subscription',
});
```

Huawei remains Android-only. Fail closed on iOS rather than sending HMS
payloads from an iOS binary.

## Roadmap

- Add a thin native module around the iOS and Android SDKs so signed payloads
  cross the bridge unaltered.
- Add React Native examples and end-to-end verification flows.
- Publish package metadata and release artifacts for consumption via npm.
