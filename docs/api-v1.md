# IAPStack HTTP API v1

The canonical machine-readable contract is [`contracts/openapi/v1.yaml`](../contracts/openapi/v1.yaml).
CI validates the document, checks it against every registered Go route, validates representative
server responses, and verifies that the Flutter SDK and embedded dashboard only depend on declared
operations and fields. Public v1 changes must update the contract and affected compatibility tests
in the same pull request.

All request and response bodies are JSON. Error responses use:

```json
{"error":{"code":"invalid_request","message":"request values are invalid","request_id":"..."}}
```

Administration and application calls use `Authorization: Bearer <key>`. Stored keys are returned once and only an Argon2id verifier is retained.

## API key lifecycle

Administrator key rotation uses three endpoints:

- `GET /v1/admin/api-keys` returns at most 200 secret-free lifecycle records, newest
  first. Revoked records remain visible for audit context, and a stored key marks the
  record authenticating the request with `current: true`.
- `POST /v1/admin/api-keys` creates an administrator or application key and returns
  its bearer once. The bearer, salt, hash, and verifier are never returned by the list
  endpoint.
- `DELETE /v1/admin/api-keys/{key_id}` idempotently revokes a stored key. A stored
  administrator cannot revoke the key authenticating its current request, and the
  final active administrator key cannot be revoked.

Rotate a key by creating its replacement, storing the new bearer in the deployment
secret manager, connecting with the replacement, and only then revoking the old key.
Bootstrap authentication has no stored key identity, so it never appears as `current`;
remove the bootstrap secret from the runtime after durable administrator provisioning.

## Bootstrap sequence

1. Use `IAPSTACK_BOOTSTRAP_ADMIN_KEY` to create a stored admin key with `POST /v1/admin/api-keys` and `{"role":"admin"}`.
2. Create the project, provider application, entitlement, product, provider-product mapping, and customer using the idempotent `/v1/admin/projects/...` PUT endpoints.
3. Store the provider's versioned credential package and an HTTPS webhook configuration.
4. Create an application key with `{"role":"application","project_id":"...","application_id":"..."}`.

The Huawei credential uses schema version 1 and media type `application/vnd.iapstack.huawei-credentials+json`:

```json
{
  "client_id": "...",
  "client_secret": "...",
  "public_key": "-----BEGIN PUBLIC KEY-----...",
  "token_url": "https://oauth-login.cloud.huawei.com/oauth2/v3/token",
  "order_url": "https://<site-specific-order-service>/...",
  "subscription_url": "https://<site-specific-subscription-service>/..."
}
```

Huawei service roots vary by account site and API generation, so the applicable URLs are explicit protected application configuration. Confirm them against the current Huawei console and official documentation.

The initial Apple credential uses kind `apple_app_store_server_api`, schema version 1,
and media type `application/vnd.iapstack.apple-credentials+json`:

```json
{
  "issuer_id": "99b16628-15e4-4668-972b-eeff55eeff55",
  "key_id": "ABCDEFGHIJ",
  "bundle_id": "com.example.application",
  "app_apple_id": 123456789,
  "private_key": "-----BEGIN PRIVATE KEY-----...",
  "root_certificates": ["-----BEGIN CERTIFICATE-----..."]
}
```

Set the Apple application's `provider_application_id` to its bundle ID. The private
key must be the PKCS#8 P-256 In-App Purchase key from App Store Connect. Production
credentials require `app_apple_id`; sandbox credentials may omit it. Download the
trusted roots from [Apple PKI](https://www.apple.com/certificateauthority/) and keep
the private key in the protected credential payload, never in a mobile client or
source control. IAPStack uses Apple's current production and sandbox
`api.storekit.apple.com` domains rather than accepting credential-controlled API URLs.

The initial Google Play credential uses kind `google_play_android_publisher`, schema
version 1, and media type `application/vnd.iapstack.google-play-credentials+json`:

```json
{
  "client_email": "iapstack@example-project.iam.gserviceaccount.com",
  "private_key_id": "0123456789abcdef",
  "private_key": "-----BEGIN PRIVATE KEY-----..."
}
```

Grant the service account access to the application in Play Console and set the
application's `provider_application_id` to its Android package name. IAPStack creates
a five-minute RS256 service-account assertion, exchanges it at Google's fixed OAuth
endpoint for the `androidpublisher` scope, and never accepts credential-controlled
OAuth or Publisher API URLs.

## Administrative read model

The initial dashboard uses two bounded, read-only administrator contracts:

- `GET /v1/admin/projects` lists up to 200 projects with application, customer, and product counts.
- `GET /v1/admin/projects/{project_id}/overview` returns up to 200 applications, products, and customers plus the 50 most recent normalized purchase observations and webhook delivery outcomes.

The overview also includes aggregate pending, completed, and failed counts for the
inbox, reconciliation, and outbox queues. These responses deliberately omit API key
verifiers, credential payloads, ciphertext, fingerprints, provider references,
purchase evidence, and webhook bodies. Unknown project scopes return the same
`not_found` envelope used by other scoped resources.

Application summaries expose only `credential_revision` and `webhook_revision` for
optimistic dashboard updates. A zero revision means the corresponding configuration
is absent. Revision values are not secrets; endpoint URLs, signing secrets, provider
credential fields, and protected values are never included in the read model.

## Application operations

- `POST /v1/applications/{application_id}/purchases:verify`
- `POST /v1/applications/{application_id}/purchases:restore`
- `GET /v1/applications/{application_id}/customers/{external_customer_id}/entitlements`

A purchase item contains an external customer ID, claimed products, optional customer bindings, and one provider evidence object. Huawei uses `application/vnd.iapstack.huawei-purchase+json`:

```json
{
  "purchase_data": "<exact InAppPurchaseData JSON string>",
  "signature": "<base64 RSA-SHA256 signature>",
  "product_kind": "subscription"
}
```

IAPStack verifies the device signature, queries Huawei for current authoritative state, verifies the server response signature, checks application/product/token consistency, and commits evidence, projection, and outbox changes atomically.

For v0.1 customer binding, the Huawei `developerPayload` must equal the authenticated request's `external_customer_id`. The API always supplies this as an expected signed binding; a valid purchase cannot be reassigned to a different application customer.

Apple uses `application/vnd.iapstack.apple-transaction+json`:

```json
{
  "signed_transaction": "eyJhbGciOiJFUzI1NiIsIng1YyI6Wy4uLl19...",
  "product_kind": "subscription"
}
```

The `signed_transaction` is the compact `JWSTransaction` returned by StoreKit 2.
For Apple applications, `external_customer_id` must be the same UUID supplied to
StoreKit as `appAccountToken`. IAPStack verifies the ES256 signature, the three-entry
`x5c` chain against configured Apple roots, the WWDR intermediate, bundle ID,
environment, product, transaction identity, and customer binding before calling
[Get Transaction Info](https://developer.apple.com/documentation/appstoreserverapi/get-transaction-info).
The authoritative response is verified again before persistence. The API client JWT
uses ES256, the required App Store Connect claims, and a five-minute lifetime.

The initial Apple server slice supports auto-renewable subscription and non-consumable
transaction verification, restore submissions, expiry, refund/revocation, ownership,
and idempotent projections. App Store Server Notifications, renewal-info/status
queries, billing retry, and grace-period reconciliation remain release work; do not
treat this slice alone as the Apple stable-release gate.

Google Play uses `application/vnd.iapstack.google-play-purchase+json`:

```json
{
  "purchase_token": "<token returned by Google Play Billing>",
  "product_kind": "subscription"
}
```

For Google Play applications, `external_customer_id` must be the same obfuscated
identifier supplied to BillingFlowParams as `obfuscatedAccountId`. IAPStack queries
the current `purchases.subscriptionsv2` or `purchases.productsv2` resource, verifies
the product and customer binding, and stores the purchase token only through the
protected evidence/reference boundaries. Subscription states including active,
pending, canceled, expired, paused, grace period, and account hold are normalized.

The initial Google Play server slice supports one current subscription line item or
one non-consumable line item per purchase token. Multi-line subscription add-ons,
acknowledgement/consumption commands, Real-time Developer Notifications, the Flutter
Billing companion, and the Google Play sandbox gate remain release work.

## Notifications and reconciliation

Huawei notification ingestion is `POST /v1/providers/huawei/applications/{application_id}/notifications` with the project identity in `X-IAPStack-Project-ID`. Its JSON envelope contains `external_customer_id`, `claimed_products`, and the signed `purchase` evidence above.

The notification signature and application/product scope are checked before protected inbox persistence and acknowledgement. The worker then performs the authoritative Huawei query. Successful verification schedules a new uniquely fingerprinted lifecycle check every 24 hours.

## Webhook verification

Outbound requests include `IAPStack-Event-ID`, `IAPStack-Timestamp`, and `IAPStack-Signature`. Verify `v1=<hex>` as HMAC-SHA-256 over:

```text
<unix_timestamp>.<exact_raw_body>
```

Reject timestamps outside the application's replay window and deduplicate by event ID.

## Flutter SDK

The provider-neutral Dart/Flutter package is in `sdk/flutter/iapstack`. It
implements application-scoped authentication, bounded response reads,
timeouts, transient retries, request ID propagation, v1 models, and stable
error envelopes. It does not persist or log the application bearer.

Huawei applications add `sdk/flutter/iapstack_huawei`. The companion uses the
official `huawei_iap` plugin, retains the exact signed `InAppPurchaseData` JSON
string, binds purchases through `developerPayload`, walks continuation tokens,
deduplicates repeated provider entries, and sends restore requests in batches
of at most 100. Consumables remain outside v0.1.

The runnable Android harness under `sdk/flutter/iapstack_huawei/example` takes
all IAPStack values through `--dart-define`; AppGallery Connect configuration
and signing files are deliberately gitignored. Follow its README for device
setup and never commit `agconnect-services.json`, keystores, or application
keys. It calls Huawei's sandbox activation API before enabling purchase or
restore and displays the request ID used to correlate secret-free release
evidence with server logs.

## Huawei sandbox release gate

Before a v0.1 release, run fixture and sandbox scenarios for lifetime purchase, initial subscription, renewal, cancellation-at-period-end, expiration, grace period, refund/revocation, duplicate purchase submission, duplicate notification, and a missed notification recovered by scheduled reconciliation. Record request IDs and verify that duplicates do not increase entitlement versions or create new logical webhook events.

The automated production-shaped fixture subset runs with `./deploy/e2e/run.sh`. It covers signed lifetime verification, duplicate purchase and notification submission, worker restart recovery, authoritative refund projection, and HMAC webhook delivery. Provider-managed subscription lifecycle scenarios still require the Huawei sandbox before a release is promoted from candidate to stable.

The complete real-device procedure and authoritative provider references are in
[`docs/releases/v0.1.0.md`](releases/v0.1.0.md). Release evidence uses the closed,
secret-free JSON contract shown in
[`v0.1.0-sandbox-evidence.example.json`](releases/v0.1.0-sandbox-evidence.example.json)
and must pass `go run ./cmd/iapstack-release <evidence.json>`.
