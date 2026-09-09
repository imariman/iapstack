# IAPStack React Native SDK

The React Native SDK implements the IAPStack v1 API client and provides structured
helpers for wrapping purchase evidence emitted by native store integrations.

## Scope

- API methods for customer-bound verification and entitlement lookup.
- Retry, timeout, and response-size limits aligned with the provider-neutral Flutter
  client.
- Evidence helpers for Apple StoreKit 2, Google Play Billing, and Huawei IAP payloads.

## Install

```sh
npm install @iapstack/react-native
```

## Usage

```ts
import {
  IapStackClient,
  IapStackConfig,
  PurchaseSubmission,
  ProductKind,
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
```

## Evidence helpers

```ts
import { appleEvidence, googlePlayEvidence, huaweiEvidence } from '@iapstack/react-native';

const apple = appleEvidence({
  signedTransaction: '<StoreKit signed transaction>',
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

## Roadmap

- Add a thin native bridge layer around the app's selected store SDK for automatic
  evidence extraction.
- Add React Native examples and end-to-end verification flows.
- Publish package metadata and release artifacts for consumption via npm.
