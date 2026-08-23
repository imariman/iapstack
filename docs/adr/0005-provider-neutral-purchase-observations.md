# ADR-0005: Normalize provider evidence into purchase observations

- Status: Accepted
- Date: 2026-08-23

## Context

In-app purchase providers expose similar outcomes through materially different identity and lifecycle models. Treating every response as a receipt with one transaction ID would make the first Huawei implementation easy, but would couple persistence and entitlement logic to assumptions that do not hold elsewhere.

The official provider contracts show the important differences:

| Provider | Transaction and purchase identity | Lifecycle characteristics |
| --- | --- | --- |
| Apple App Store | A subscription renewal creates a new `transactionId`; `originalTransactionId` identifies its purchase lineage. | Signed transaction and renewal JWS payloads separately describe effective dates, revocation, ownership, renewal state, grace period, and billing retry. |
| Google Play | The purchase token is the server query handle. Subscriptions can link to an earlier token after upgrade, downgrade, resubscribe, conversion, or top-up. A response can contain multiple line items and each item exposes its latest successful order ID. | Pending purchases may have neither a start time nor an order ID. Subscription states include active, paused, grace period, on hold, canceled, and expired. |
| Huawei AppGallery | An order ID identifies a receipt, while the purchase token identifies a product-to-user mapping and remains stable when a subscription renews. | The server response exposes expiration, renewal, cancellation, grace, retry, and validity information. |
| Amazon Appstore | A globally unique receipt ID is verified together with the Amazon user ID. A lapsed and later reactivated subscription can have a second receipt. | `cancelDate` means access has ended; `renewalDate`, grace period, auto-renewal, and fulfillment are separate fields. |
| Samsung Galaxy Store | A purchase ID identifies the subscription and server queries; payment transactions and the first purchase can have separate identifiers. | Cancel ends renewal but preserves access to period end, refund preserves the current subscription, and revoke removes access immediately. |

Provider fields and state vocabularies will continue to evolve. The durable domain boundary therefore needs stable semantics and an explicit escape hatch for provider-native evidence.

## Decision

### Scope every provider identifier

A configured application has an internal application ID and a store scope consisting of provider, environment, and provider application ID. Provider product IDs are mapped to internal products within that application boundary.

Provider names and product kinds are validated string value types. IAPStack defines built-in values, but adding a provider or a future product kind does not change the surrounding structures.

### Use role-based opaque references

Provider identifiers are opaque references with a role, kind, and private value. Initial roles are:

- transaction: an order, payment, transaction, or receipt identity
- lineage: the current purchase or subscription lineage
- linked lineage: a prior or related purchase lineage
- query: a token or identifier used to fetch authoritative state
- customer binding: a provider-side application customer association

An observation can contain multiple references for every role. This supports providers with several transaction identifiers without adding provider-shaped columns to the core model.

Reference values are redacted from normal string and structured log output. Callers must explicitly request the raw value for adapter or persistence operations.

### Separate evidence, observations, and entitlement policy

Client artifacts enter an adapter as an opaque, content-typed evidence envelope. The adapter owns its encoding and version. Adding a provider-specific signature, token, receipt, or composite JSON request does not change the verification port.

A successful adapter result contains:

- the verified timestamp;
- one or more verified raw artifacts for audit;
- one normalized purchase observation per provider line item.

An observation records provider-native state alongside normalized lifecycle state, access status and reason, effective interval, ownership, quantity, renewal information, and opaque references.

Normalized access says whether current store evidence permits service. It does not directly mutate an application entitlement. The entitlement projection remains a later core policy that also requires application, customer, catalog, and evidence consistency.

### Keep lifecycle and access orthogonal

Cancellation, refund, and access are not interchangeable:

- a canceled subscription can remain allowed until period end;
- a refund can preserve access on providers whose refund operation does not revoke service;
- revocation and expiration deny access;
- grace period can remain allowed;
- pending and unknown evidence remain unresolved and cannot grant access.

Allowed observations require a verified transaction reference, occurrence time, and effective period start. Allowed subscriptions also require an effective period end. Pending observations may omit a transaction when the provider has only issued a query token.

### Verify request-to-result consistency at the port boundary

Verification and reconciliation results are rejected before persistence when:

- application, provider application, or environment scope differs;
- claimed and observed product sets differ;
- a configured expected customer binding does not match;
- a reconciliation result does not carry the query reference that produced it;
- observation idempotency identities are duplicated;
- verified artifacts or required normalized fields are absent.

Verification may return multiple line items. Reconciliation accepts one or more query references instead of assuming a universal purchase token.

### Classify adapter failures

Adapters expose stable failure categories for invalid evidence, authentication, not found, conflict, throttling, temporary provider failure, and permanent provider failure. Only rate-limited and temporary failures are retryable by default. Provider error bodies remain wrapped for diagnostics but are not included in the public error string.

## Consequences

- Apple transaction lineage, Google linked tokens, Huawei stable purchase tokens, Amazon receipts, and Samsung purchase IDs fit the same persistence shape.
- New provider payloads do not require changes to verification or reconciliation request structures.
- Provider references will naturally use a child table rather than fixed identifier columns in PostgreSQL.
- Raw evidence can be audited or re-normalized after adapter improvements without making it part of entitlement policy.
- Adapters must do more explicit normalization and produce deterministic, non-sensitive observation IDs.
- Adding a genuinely new cross-provider semantic may still add an optional normalized field, but provider-only details remain in verified artifacts and provider-native state.

## Official references reviewed

- [Apple `JWSTransactionDecodedPayload`](https://developer.apple.com/documentation/appstoreserverapi/jwstransactiondecodedpayload)
- [Apple `JWSRenewalInfoDecodedPayload`](https://developer.apple.com/documentation/appstoreserverapi/jwsrenewalinfodecodedpayload)
- [Google Play `purchases.subscriptionsv2`](https://developers.google.com/android-publisher/api-ref/rest/v3/purchases.subscriptionsv2)
- [Google Play `purchases.productsv2`](https://developers.google.com/android-publisher/api-ref/rest/v3/purchases.productsv2)
- [Huawei `InAppPurchaseData`](https://developer.huawei.com/consumer/en/doc/HMSCore-References/inapppurchasedata-0000001050137635)
- [Amazon Receipt Verification Service](https://developer.amazon.com/docs/in-app-purchasing/iap-rvs-for-android-apps.html)
- [Samsung IAP Subscription API](https://developer.samsung.com/iap/api/iap-subscription-api.html)
