# IAPStack HTTP API v1

The canonical machine-readable contract is [`contracts/openapi/v1.yaml`](../contracts/openapi/v1.yaml).
CI validates the document, checks it against every registered Go route, validates representative
server responses, and verifies that the Flutter SDK, Go host SDK, and embedded dashboard only depend
on declared operations and fields. Public v1 changes must update the contract and affected compatibility tests
in the same pull request.

All request and response bodies are JSON. Error responses use:

```json
{"error":{"code":"invalid_request","message":"request values are invalid","request_id":"..."}}
```

Administration and trusted host-backend calls use `Authorization: Bearer <key>`.
Stored keys are returned once and only an Argon2id verifier is retained. Mobile
data-plane calls use a short-lived customer session instead of the durable
application key.

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
5. Keep that application key in the trusted host backend. After authenticating a
   user, mint a 15-minute customer session with
   `POST /v1/applications/{application_id}/customer-sessions` and
   `{"external_customer_id":"..."}`. Return only the resulting token to the mobile client.

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
  "private_key": "-----BEGIN PRIVATE KEY-----...",
  "rtdn": {
    "subscription": "projects/example-project/subscriptions/iapstack-google-play",
    "push_service_account_email": "iapstack-push@example-project.iam.gserviceaccount.com",
    "audience": "https://iapstack.example/v1/providers/google-play/projects/project-1/applications/application-1/notifications"
  }
}
```

Grant the service account access to the application in Play Console and set the
application's `provider_application_id` to its Android package name. IAPStack creates
a five-minute RS256 service-account assertion, exchanges it at Google's fixed OAuth
endpoint for the `androidpublisher` scope, and never accepts credential-controlled
OAuth or Publisher API URLs. The `rtdn` object is optional for direct verification
but required before the application's Google Play notification endpoint can accept
Pub/Sub pushes.

## Administrative read model

The dashboard exchanges an explicit administrator bearer through
`POST /v1/admin/dashboard-session` for a protected 12-hour browser session. The
encrypted session value is returned only as a `Secure`, `HttpOnly`,
`SameSite=Strict` cookie scoped to `/v1/admin`; it is never exposed to dashboard
JavaScript. `DELETE /v1/admin/dashboard-session` clears the cookie. Existing API
clients may continue using administrator bearer authentication directly. Dashboard
cookie mutations additionally require the same-origin browser marker and `Origin`.

The initial dashboard uses two bounded, read-only administrator contracts:

- `GET /v1/admin/projects` lists up to 200 projects with application, customer, and product counts.
- `GET /v1/admin/projects/{project_id}/overview` returns up to 200 applications, products, and customers plus the 50 most recent normalized purchase observations and webhook delivery outcomes. It also returns a fixed 30-day UTC verification-activity series, active-entitlement count, and an explicit revenue-availability status.

The overview also includes aggregate pending, completed, and failed counts for the
inbox, reconciliation, and outbox queues. These responses deliberately omit API key
verifiers, credential payloads, ciphertext, fingerprints, provider references,
purchase evidence, and webhook bodies. Unknown project scopes return the same
`not_found` envelope used by other scoped resources.

Verification activity is operational data, not a financial ledger. Sandbox-only
projects report zero real revenue. Projects with production applications report
`store_reports_required` until authoritative Apple, Google Play, or Huawei financial
reports are ingested; the API never estimates revenue from client product prices or
purchase counts.

Application summaries expose only `credential_revision` and `webhook_revision` for
optimistic dashboard updates. A zero revision means the corresponding configuration
is absent. Revision values are not secrets; endpoint URLs, signing secrets, provider
credential fields, and protected values are never included in the read model.

## Application operations

- `POST /v1/applications/{application_id}/customer-sessions`
- `POST /v1/applications/{application_id}/purchases:verify`
- `POST /v1/applications/{application_id}/purchases:restore`
- `GET /v1/applications/{application_id}/customers/{external_customer_id}/entitlements`

Only the trusted host backend calls the first endpoint, using its durable application
bearer after authenticating the user. The remaining endpoints require the returned
customer session and reject requests whose application or external customer ID does
not exactly match the authenticated session.

Restore processes items sequentially under a 25-second server budget. If that budget
expires, the API returns retryable `503 request_timeout`; already completed items remain
durable, and resubmitting the same batch is safe because each verification is idempotent.

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
[Get Transaction Info](https://developer.apple.com/documentation/appstoreserverapi/get-transaction-info)
for non-consumables. Subscription verification calls
[Get All Subscription Statuses](https://developer.apple.com/documentation/appstoreserverapi/get-all-subscription-statuses)
and independently verifies the latest transaction and renewal JWS values before persistence. The API client JWT
uses ES256, the required App Store Connect claims, and a five-minute lifetime.

The initial Apple server slice supports auto-renewable subscription and non-consumable
transaction verification, restore submissions, renewal enabled/disabled status,
billing retry, grace period, expiry, refund/revocation, ownership, and idempotent
projections. Subscription verification and each daily reconciliation use the current
App Store status rather than the original notification or client lifecycle claim.

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
one non-consumable line item per purchase token. A completed purchase whose
`acknowledgementState` is pending produces a bounded provider action. IAPStack calls
the matching Android Publisher product or subscription acknowledgement endpoint only
after the evidence, observation, entitlement projection, and outbox event commit.
Pending purchases and already acknowledged purchases never create this action. A
retryable acknowledgement failure is returned after the durable result so the API
submission, RTDN job, or scheduled reconciliation can safely retry and re-query the
current acknowledgement state. Multi-line subscription add-ons, consumable fulfillment,
and the real Google Play internal-testing lifecycle gate remain release work.

## Notifications and reconciliation

Huawei notification ingestion is
`POST /v1/providers/huawei/projects/{project_id}/applications/{application_id}/notifications`.
Configure this complete HTTPS URL as the Huawei IAP V2 callback; no custom header or
IAPStack bearer is required. The endpoint accepts Huawei's native `ORDER` wrapper or
`SUBSCRIPTION` wrapper. Subscription status strings are verified with the
application IAP public key using the declared `SHA256withRSA` or
`SHA256withRSA/PSS` algorithm before protected inbox persistence.

An order wrapper is treated only as an untrusted change signal because Huawei does
not attach an equivalent signature to that wrapper. Before inserting the inbox row or
returning `200`, the handler resolves its protected purchase-token fingerprint against
a previously verified purchase and requires the same application, product, and product
kind. The lookup and inbox insertion share one transaction. Unknown tokens and scope
mismatches are rejected without durable work; only an unavailable persistence lookup is
retryable. The worker still queries Huawei's server API before changing access, so
callback fields never grant access directly.

App Store Server Notifications V2 ingestion is
`POST /v1/providers/apple/projects/{project_id}/applications/{application_id}/notifications`.
Configure this complete HTTPS URL in App Store Connect. The request body contains only
Apple's `signedPayload` compact JWS; it does not use an IAPStack bearer. Before durable
inbox insertion, IAPStack verifies the outer ES256 signature and certificate chain,
version, notification UUID, bundle ID, App Apple ID in production, and environment.
When transaction or renewal JWS values are present, each signature and application,
product, lineage, and `appAccountToken` binding is checked independently.

The worker never grants access from `notificationType`, `subtype`, or the notification
status field. It submits the signed transaction to the normal verification service,
which queries the current App Store subscription status, atomically updates projections
and outbox events, and schedules the next daily refresh. Signed audit-only notifications,
including App Store test deliveries without transaction data, complete without an
entitlement change. Exact duplicate deliveries normalize to the same protected inbox
identity and downstream projection/outbox writes remain idempotent.

Google Play RTDN ingestion is
`POST /v1/providers/google-play/projects/{project_id}/applications/{application_id}/notifications`.
Both scope identifiers are embedded in the push URL because Pub/Sub does not attach
application-defined request headers. The request body is the wrapped Pub/Sub push
envelope: `message.data` contains the base64-encoded Google Play
`DeveloperNotification`, while `message.messageId` and `subscription` retain delivery
identity and scope. `Authorization` is the Google-signed Pub/Sub OIDC bearer, not an
IAPStack application key.

Before returning a successful Pub/Sub push response, IAPStack verifies the Google token signature, expiry and
configured audience using Google's supported ID-token verifier, then requires the
configured push service-account email, exact Pub/Sub subscription, Android package,
RTDN version, and one mutually exclusive notification variant. Subscription,
one-time-product, and voided-purchase signals are normalized into the protected inbox.
The worker fingerprints the purchase token in its original protected query scope,
resolves the previously verified customer, product kind, product ID, and opaque
customer binding, and calls Android Publisher for current state instead of trusting
the RTDN lifecycle type. The normalized native RTDN is retained as protected
`provider_notification_reconciliation` evidence; it is not rewritten as a client
purchase submission. Unknown purchase tokens
remain retryable so a client verification racing the notification can arrive first.
When that authoritative state requires purchase acknowledgement, the worker commits
the normalized state first and then calls Google using the same protected purchase
token. HTTP `409`, `429`, and `5xx` acknowledgement failures remain retryable.

Google Play Console test notifications and pending-refund-review notifications are
retained as authenticated audit records and complete without an entitlement change.
The pending chargeback review workflow itself is outside the current slice. Duplicate
Pub/Sub deliveries normalize to the same protected inbox identity and downstream
projection/outbox writes remain idempotent.

## Webhook verification

Outbound requests include `IAPStack-Event-ID`, `IAPStack-Timestamp`, and `IAPStack-Signature`. Verify `v2=<hex>` as HMAC-SHA-256 over the unambiguous newline-delimited input:

```text
v2
<event_id>
<unix_timestamp>
<exact_raw_body>
```

The event ID is authenticated. Reject `v1` signatures, timestamps outside the application's replay window, and any delivery whose `IAPStack-Event-ID` does not match the MAC. Deduplicate by that authenticated event ID.

## Go host SDK

The trusted-host Go package is in `sdk/go`. It uses the durable application bearer
to mint customer sessions, looks up entitlements with a minted customer session
because the public GET contract does not accept the application bearer, and
verifies `v2` webhook signatures with a replay window and event-ID dedupe. It
does not import `internal/` or talk to PostgreSQL. Never log or persist the
application bearer, and never ship it in a mobile binary.

## Flutter SDK

The provider-neutral Dart/Flutter package is in `sdk/flutter/iapstack`. It
implements customer-scoped session authentication, bounded response reads,
abortable per-attempt timeouts, full-jitter transient retries, request ID propagation, v1 models, and stable
error envelopes. It does not persist or log the customer bearer; the durable
application bearer remains in the trusted host backend.

Huawei applications add `sdk/flutter/iapstack_huawei`. The companion uses the
official `huawei_iap` plugin, retains the exact signed `InAppPurchaseData` JSON
string, checks regional IAP readiness, loads configured localized AppGallery products,
binds purchases through `developerPayload`, walks continuation tokens,
deduplicates repeated provider entries, and sends restore requests in batches
of at most 100. Consumables remain outside v0.1.

The runnable Android harness under `sdk/flutter/iapstack_huawei/example` takes
all IAPStack values through `--dart-define`; AppGallery Connect configuration
and signing files are deliberately gitignored. Follow its README for device
setup and never commit `agconnect-services.json`, keystores, or customer
sessions. It requires environment readiness, sandbox activation, and a purchasable
queried product before enabling purchase, and displays the request ID used to correlate secret-free release
evidence with server logs.

Google Play applications add `sdk/flutter/iapstack_google_play`. The companion
uses Flutter's official `in_app_purchase` implementation, checks queried product
kinds against an explicit local catalog, passes the opaque external customer ID
as `obfuscatedAccountId`, submits purchase-token evidence, queries owned purchases,
deduplicates restores, and sends batches of at most 100. It deliberately does not
call client-side `completePurchase`: IAPStack owns Android Publisher acknowledgement
after the authoritative transaction commits.

Each queried offer retains exact price micros and complete subscription base-plan,
offer-tag, and pricing-phase metadata. The coordinator rejects empty, incomplete,
duplicate, unconfigured, or out-of-scope query results and opens checkout only for a
purchasable offer retained from the successful query.

The Android harness under `sdk/flutter/iapstack_google_play/example` accepts only
customer-scoped test configuration through `--dart-define`, subscribes to the
purchase stream during startup, never renders or persists the bearer or purchase
token, and shows request IDs for redacted correlation. Install a signed build from
the Play Console internal-testing track for real Billing flows; a sideloaded debug
APK is only a local UI and connectivity smoke test.

Apple applications add `sdk/flutter/iapstack_apple`. The companion requires a
canonical lowercase UUID customer ID, passes it to StoreKit 2 as `appAccountToken`,
and sends only the compact transaction JWS from `serverVerificationData` to IAPStack.
It listens for unfinished transactions from startup and calls `completePurchase` only
after authoritative server verification succeeds. Restore synchronizes App Store
ownership, reads StoreKit 2 transaction history, deduplicates transaction IDs, and
sends batches of at most 100. Signed evidence and customer sessions stay in memory
and are redacted from string representations.

The iOS harness under `sdk/flutter/iapstack_apple/example` contains a shared Xcode
scheme with a StoreKit Configuration file for local product, listener, cancellation,
restore, and unfinished-transaction testing. Use App Store sandbox products and
matching server credentials for full certificate-chain and backend lifecycle tests.

## Three-provider release gate

Before a v0.1 release candidate or stable release, run real-provider scenarios for
Apple App Store, Google Play, and Huawei AppGallery. Each gate covers non-consumable
purchase and restore, provider-specific subscription states, refund/revocation,
negative application/product/customer bindings, duplicate notification, and a missed
notification recovered by authoritative reconciliation. Record only request IDs and
verify that duplicates do not increase entitlement versions or logical webhook events.

The automated production-shaped fixture subset runs with `./deploy/e2e/run.sh`. It
covers signed Huawei lifetime verification, duplicate purchase and notification
submission, worker restart recovery, authoritative refund projection, and HMAC webhook
delivery. Provider-specific unit and integration suites exercise Apple and Google
server behavior, but all three real-store lifecycle gates remain mandatory.

The complete procedure and provider-specific runbooks start at
[`docs/releases/v0.1.0-rc.1.md`](releases/v0.1.0-rc.1.md). Release evidence uses the
closed, secret-free aggregate JSON contract shown in
[`v0.1.0-rc.1-release-evidence.example.json`](releases/v0.1.0-rc.1-release-evidence.example.json)
and must pass `go run ./cmd/iapstack-release <release-evidence.json>`.
