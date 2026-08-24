# IAPStack HTTP API v1

All request and response bodies are JSON. Error responses use:

```json
{"error":{"code":"invalid_request","message":"request values are invalid","request_id":"..."}}
```

Administration and application calls use `Authorization: Bearer <key>`. Stored keys are returned once and only an Argon2id verifier is retained.

## Bootstrap sequence

1. Use `IAPSTACK_BOOTSTRAP_ADMIN_KEY` to create a stored admin key with `POST /v1/admin/api-keys` and `{"role":"admin"}`.
2. Create the project, Huawei application, entitlement, product, provider-product mapping, and customer using the idempotent `/v1/admin/projects/...` PUT endpoints.
3. Store a `huawei_server_api` credential and HTTPS webhook configuration.
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

## Administrative read model

The initial dashboard uses two bounded, read-only administrator contracts:

- `GET /v1/admin/projects` lists up to 200 projects with application, customer, and product counts.
- `GET /v1/admin/projects/{project_id}/overview` returns up to 200 applications, products, and customers plus the 50 most recent normalized purchase observations and webhook delivery outcomes.

The overview also includes aggregate pending, completed, and failed counts for the
inbox, reconciliation, and outbox queues. These responses deliberately omit API key
verifiers, credential payloads, ciphertext, fingerprints, provider references,
purchase evidence, and webhook bodies. Unknown project scopes return the same
`not_found` envelope used by other scoped resources.

## Application operations

- `POST /v1/applications/{application_id}/purchases:verify`
- `POST /v1/applications/{application_id}/purchases:restore`
- `GET /v1/applications/{application_id}/customers/{external_customer_id}/entitlements`

A purchase item contains an external customer ID, claimed products, optional customer bindings, and a Huawei evidence object. The evidence media contract is `application/vnd.iapstack.huawei-purchase+json`:

```json
{
  "purchase_data": "<exact InAppPurchaseData JSON string>",
  "signature": "<base64 RSA-SHA256 signature>",
  "product_kind": "subscription"
}
```

IAPStack verifies the device signature, queries Huawei for current authoritative state, verifies the server response signature, checks application/product/token consistency, and commits evidence, projection, and outbox changes atomically.

For v0.1 customer binding, the Huawei `developerPayload` must equal the authenticated request's `external_customer_id`. The API always supplies this as an expected signed binding; a valid purchase cannot be reassigned to a different application customer.

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
keys.

## Huawei sandbox release gate

Before a v0.1 release, run fixture and sandbox scenarios for lifetime purchase, initial subscription, renewal, cancellation-at-period-end, expiration, grace period, refund/revocation, duplicate purchase submission, duplicate notification, and a missed notification recovered by scheduled reconciliation. Record request IDs and verify that duplicates do not increase entitlement versions or create new logical webhook events.

The automated production-shaped fixture subset runs with `./deploy/e2e/run.sh`. It covers signed lifetime verification, duplicate purchase and notification submission, worker restart recovery, authoritative refund projection, and HMAC webhook delivery. Provider-managed subscription lifecycle scenarios still require the Huawei sandbox before a release is promoted from candidate to stable.
