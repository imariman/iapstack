# ADR-0002: Isolate stores behind a store-neutral core

- Status: Accepted
- Date: 2026-08-23

## Context

Apple, Google, Huawei, Amazon, and other stores expose different product identifiers, receipt formats, authentication schemes, lifecycle states, and notification protocols. Applications should not have to understand those differences to decide what a customer may access.

Using one store's vocabulary as the shared model would make every later adapter a partial or misleading translation.

## Decision

IAPStack will define store-neutral domain concepts for customers, catalog products, transactions, lifecycle evidence, and entitlements.

Each store adapter owns:

- Store authentication and remote API calls
- Signature and payload validation
- Store-specific identifiers and raw evidence
- Translation from store results into normalized transaction and lifecycle facts
- Reconciliation queries and notification decoding

The core owns:

- Customer identity and application boundaries
- Product-to-entitlement mapping
- Idempotency and conflict rules
- Entitlement evaluation
- Persistence orchestration and outbound domain events

Store SDK or API response types must not cross the adapter boundary. Raw store data may be retained for audit, but core decisions use normalized facts with traceable source evidence.

## Consequences

- Adding a store does not change the public entitlement lookup contract.
- Store fixtures can run against a shared conformance test suite.
- Some store-specific nuance cannot be flattened; normalized facts therefore retain a store, source ID, observed time, and raw evidence reference.
- Adapter translation rules become security-critical and require contract tests.

## Alternatives considered

### Shared model based on the first Huawei payloads

Rejected because Huawei field names and lifecycle states are not a durable cross-store contract.

### Expose each store directly to application backends

Rejected because it moves verification, restore, and entitlement complexity back into every consuming application.
