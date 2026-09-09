# IAPStack React Native SDK (WIP)

This package is the starting point for the React Native wrapper work requested in
[#88](https://github.com/imariman/iapstack/issues/88).

Issue context in the repository roadmap:

- Add a React Native wrapper over the native SDKs

## Planned behavior

- Keep the durable application bearer on a trusted backend and only use short-lived
  customer sessions in mobile apps.
- Reuse the existing native SDK shape where possible while exposing a common JS API.
- Support Apple App Store, Google Play, and Huawei AppGallery flows.
- Map store evidence into the IAPStack entitlement and transaction model used by the
  server API.

## Progress

- [ ] Native bridge bootstrapping (iOS)
- [ ] Native bridge bootstrapping (Android)
- [ ] JS API shape and TypeScript types
- [ ] Example app integration documentation
- [ ] E2E smoke verification against the IAPStack API contracts

## Local note

Implementation work for this SDK is intentionally incomplete in this
branch. The source file exports a placeholder API to make early integration and API
shape exploration possible while the bridge implementation is finalized.
