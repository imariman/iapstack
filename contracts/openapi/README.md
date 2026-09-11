# IAPStack OpenAPI contracts

`v1.yaml` is the canonical machine-readable contract for the self-hosted IAPStack HTTP API.
It covers the administrator control plane, Apple, Google Play, and Huawei purchase and entitlement operations,
plus Huawei lifecycle notifications and authenticated Google Play Pub/Sub RTDN ingestion.

## Change policy

- Additive, backward-compatible fields remain optional until every first-party client accepts them.
- Removing or renaming an operation, field, enum value, status code, or authentication requirement
  is a breaking change and requires a new API version.
- Provider credentials and purchase evidence remain store-specific schemas inside the shared v1
  envelope. Store fields must not leak into the provider-neutral entitlement response.
- Secrets are request-only and use `writeOnly`; operational read models must never expose protected
  payloads, ciphertext, fingerprints, tokens, or signing secrets.

## Validation

Run the server and route compatibility checks from the repository root:

```sh
go test -count=1 ./internal/transport/httpapi ./dashboard
```

Run the provider-neutral SDK compatibility check from `sdk/flutter/iapstack`:

```sh
dart pub get
dart test test/openapi_contract_test.dart
```

Run the trusted-host Go SDK compatibility check from `sdk/go`:

```sh
go test -count=1 -run TestOpenAPIDeclaresHostOperations ./...
```

Run the trusted-host TypeScript SDK compatibility check from `sdk/typescript`:

```sh
bun install --frozen-lockfile
bun test test/contract.test.ts
```

The full CI pipeline executes these checks and rejects undocumented routes or incompatible client
schema changes.
