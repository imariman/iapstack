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

- `GET /v1/admin/api-keys` returns at most 200 secret-free lifecycle records per
  page, newest first. When older keys exist the response includes `next_page_token`;
  pass that value as `page_token` to list the next older page. The last page omits
  `next_page_token`. Revoked records remain visible for audit context, and a stored
  key marks the record authenticating the request with `current: true`.
- `POST /v1/admin/api-keys` creates an administrator or application key and returns
  its bearer once. The bearer, salt, hash, and verifier are never returned by the list
  endpoint.
- `DELETE /v1/admin/api-keys/{key_id}` idempotently revokes a stored key. A stored
  administrator cannot revoke the key authenticating its current request. The final
  active administrator key cannot be revoked unless an administrator account exists,
  because that account can still sign in and create a replacement key.
- `GET /v1/admin/administrators` lists the administrator accounts that sign in to the
  dashboard with a username and password. It never returns a password or verifier, and
  marks the account behind the current dashboard session with `current: true`.
- `POST /v1/admin/administrators` creates an account from `username` and `password`.
  The username is trimmed and lowercased: 3-64 letters, digits, dots, underscores, at
  signs, and hyphens, starting with a letter or digit. The password needs at least 12
  characters, at most 256 bytes, and must differ from the username. A taken username
  answers `409`.
- `PUT /v1/admin/administrators/{administrator_id}/password` replaces an account's
  password. An administrator changing the password of the account they are signed in
  with must send `current_password`; a wrong one answers `403` with code
  `password_mismatch`. Any other administrator replaces it without the current
  password. The change ends every dashboard session the account issued earlier, and
  the caller's own session is replaced by a new cookie in the response.
- `DELETE /v1/admin/administrators/{administrator_id}` deletes another administrator's
  account and ends its sessions. Deleting the account you are signed in with answers
  `409`, and so does deleting the final account while no active administrator key
  exists.

Rotate a key by creating its replacement, storing the new bearer in the deployment
secret manager, connecting with the replacement, and only then revoking the old key.
Bootstrap authentication has no stored key identity, so it never appears as `current`.
It works only while no active stored administrator key and no administrator account
exists: creating the first of either retires the bootstrap bearer and every bootstrap
dashboard session immediately, without a restart. Clearing the bootstrap secret from
the runtime afterwards is optional.

## Bootstrap sequence

1. Use `IAPSTACK_BOOTSTRAP_ADMIN_KEY` to create a stored admin key with `POST /v1/admin/api-keys` and `{"role":"admin"}`. The bootstrap key stops working at that moment, so use the returned stored admin key for every later step. A person setting up the dashboard creates an administrator account instead (the setup banner does this); scripts keep using stored admin keys.
2. Create the project, provider application, entitlement, product, and provider-product mapping using the idempotent `/v1/admin/projects/...` PUT endpoints. Customers need no step here: the first stored purchase creates one (see [Application operations](#application-operations)). Create a customer with `PUT /v1/admin/projects/{project_id}/customers/{customer_id}` only to choose its internal id or to attach aliases before its first purchase. That PUT returns `409` when the external id already belongs to a customer with a different internal id.
3. Store the provider's versioned credential package and an HTTPS webhook configuration.
4. Create an application key with `{"role":"application","project_id":"...","application_id":"..."}`.
5. Keep that application key in the trusted host backend. After authenticating a
   user, mint a 15-minute customer session with
   `POST /v1/applications/{application_id}/customer-sessions` and
   `{"external_customer_id":"..."}`. Return only the resulting token to the mobile client.

The Huawei credential uses schema version 1 and media type `application/vnd.iapstack.huawei-credentials+json`:

```json
{
  "client_id": "YOUR_APPGALLERY_CLIENT_ID",
  "client_secret": "YOUR_APPGALLERY_CLIENT_SECRET",
  "public_key": "-----BEGIN PUBLIC KEY-----\nYOUR_IAP_PUBLIC_KEY\n-----END PUBLIC KEY-----",
  "order_url": "https://orders-dre.iap.cloud.huawei.eu/applications/purchases/tokens/verify",
  "subscription_url": "https://subscr-dre.iap.cloud.huawei.eu/sub/applications/v2/purchases/get"
}
```

Huawei service roots vary by account site. The URLs above are the Germany site. Confirm the site against the current Huawei console. `token_url` is optional and defaults to `https://oauth-login.cloud.huawei.com/oauth2/v3/token`. IAPStack exchanges the client id and secret for an app-level access token and sends `Authorization: Basic` with Base64(`APPAT:` + token). Order verification posts `purchaseToken` and `productId`. Subscription verification posts `purchaseToken` and `subscriptionId`. `subscriptionId` is the subscription instance id inside `InAppPurchaseData`, distinct from the catalog `productId`. The returned `purchaseTokenData` or `inappPurchaseData` is checked with the IAP public key. The response `signatureAlgorithm` selects `SHA256WithRSA` or `SHA256WithRSA/PSS`; when that field is absent, verification uses `SHA256WithRSA`. `InAppPurchaseData.applicationId` is often a JSON number. Set `provider_application_id` to that decimal text.

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

The dashboard exchanges an administrator account's username and password, or an
explicit administrator bearer, through `POST /v1/admin/dashboard-session` for a
protected 12-hour browser session. A request with an `Authorization` header signs in
with that bearer. A request without one signs in with the JSON body
`{"username": "...", "password": "..."}`; it must come from the dashboard's own origin
and send `X-IAPStack-Dashboard: 1`, or it gets `403` with code `csrf_rejected`. A wrong
username or password answers `401` with code `invalid_credentials`. Five consecutive
wrong passwords lock the account for five minutes, and until a sign-in succeeds every
further wrong password locks it again. A locked account answers `429` with code
`too_many_attempts` and a `Retry-After` header, even for the right password. The
encrypted session value is returned only as a `Secure`, `HttpOnly`,
`SameSite=Strict` cookie scoped to `/v1/admin`; it is never exposed to dashboard
JavaScript. `GET /v1/admin/dashboard-session` reads only that cookie (a request that
also sends `Authorization` gets `401`) and reports its `expires_at` plus whether it was
issued by the bootstrap credential (`bootstrap`, `has_durable_admin`). A session
created with a username and password also reports that `username`.
`DELETE /v1/admin/dashboard-session` clears the cookie. Existing API
clients may continue using administrator bearer authentication directly. Dashboard
cookie mutations additionally require the same-origin browser marker and `Origin`.

The initial dashboard uses two bounded, read-only administrator contracts:

- `GET /v1/admin/projects` lists up to 200 projects with application, customer, and product counts.
- `GET /v1/admin/projects/{project_id}/overview` returns up to 200 applications, products, and customers plus the 50 most recent normalized purchase observations and webhook delivery outcomes. An application with no webhook configured still records each `entitlement.changed` event; it is closed as `skipped` (error code `webhook_not_configured`), counted under the outbox queue's `skipped`, never `failed`, and is not retried. Configuring a webhook later does not replay skipped events: the receiver gets only changes recorded after the webhook exists, so read current entitlements to backfill. The admin overview's `queues` gained `skipped`, and webhook event `status` can be `skipped`; admin API clients should accept both. It also returns a fixed 30-day UTC verification-activity series, active-entitlement count, and an explicit revenue-availability status.

The overview also includes aggregate pending, completed, and failed counts for the
inbox, reconciliation, and outbox queues. These responses deliberately omit API key
verifiers, credential payloads, ciphertext, fingerprints, provider references,
purchase evidence, and webhook bodies. Unknown project scopes return the same
`not_found` envelope used by other scoped resources.

Verification activity is operational data, not a financial ledger. Sandbox-only
projects report zero real revenue. Projects with production applications report
`store_reports_required` until every production application appears in its provider's
accepted official Apple, Google Play, or Huawei financial report. When that coverage
is complete the status is `reports_imported`.

Imports do not record a store account or vendor. A report that matches none of a
provider's production applications does not cover that provider. A report that matches
some of them keeps those applications' currency totals unchanged and lists the absent
application ids in `zero_filled_application_ids`. Those applications are counted as
zero and do not add a currency row. Because the file cannot show whether they had no
sales or belong to another account, status stays `store_reports_required` while that
list is non-empty.

`amounts` lists store-reported proceeds by provider, period, and currency, with no
currency conversion. For `reports_imported` every production application appeared in
its provider's report. For `store_reports_required`, `amounts` includes only matched
applications and is not a complete project total. `uncovered_providers` names
providers with no covering report. `recognized_minor_units` and `currency` stay null
in both of those statuses so a single total is not implied. The API never sums
`purchase_observations.price_milliunits` or client prices into recognized proceeds.

`GET /v1/admin/projects/{project_id}/overview` accepts optional
`financial_period_start` and `financial_period_end`. Both must be omitted or both
supplied. They choose which accepted imports may cover the project and do not change
the 30-day verification series. A missing requested period stays
`store_reports_required`; it is not treated as zero.

`POST /v1/admin/projects/{project_id}/financial-reports` uploads one file
(`provider`, `report_kind`, and `file`). Apple period and currency come from the
consolidated financial report. Google Play earnings and Huawei settlement uploads are
stored as `unrecognized` with reason `fixture_required` until a real report fixture
is pinned; those uploads may include the statement period. The raw file is not
stored. `POST .../financial-reports/{import_id}:supersede` makes a newer accepted
file the one that counts for that period and refuses a replacement that is not
accepted. `POST .../financial-reports:pull` validates one provider period and
refuses the pull until that provider's official parameter contract is pinned. It
does not guess provider query parameters. `GET .../financial-reports` lists
metadata only: no report body and no order id.

`GET /v1/admin/projects/{project_id}/subscription-metrics` reports subscription
health for one application environment (`environment`, default `production`) over
whole UTC days ending today (`window_days`, 1 to 90, default 30). A subscription is
one purchase lineage of a subscription product. The response gives active
subscriptions at the window start and at `measured_at`, new subscriptions, renewals,
plan changes and resubscriptions (kept out of renewals), churn split into voluntary,
billing, reversed, and unclassified, a churn rate of
`churned / active_during_window`, a daily active series, and an application and
product breakdown. Every figure is evaluated at or before `measured_at`; nothing is
projected. Family-shared access and imported RevenueCat grants are not counted.

Observations also store Apple `transactionReason` and `originalPurchaseDate` and
Google Play `canceledStateContext`. With them, an Apple subscriber first seen at a
renewal is not new, an Apple upgrade or downgrade is a plan change rather than a
renewal, and a Google Play token canceled by `replacementCancellation` is replaced
rather than churned even when the new token starts after a gap. Rows without these
fields fall back to simpler rules: earliest stored period for new, every later transaction a renewal, and replaced only when the
periods meet.

Trials come only from a provider-signed offer phase. Apple and Google Play record
one; Huawei does not, so its `trials` row is `not_available` with null counts.
An observation whose provider evidence had no phase carries none, and
`with_offer_phase` shows that coverage. The response has no money figure. The definitions are in
[docs/plans/subscription-metrics.md](plans/subscription-metrics.md).

### Customer support

- `GET /v1/admin/projects/{project_id}/customers?query=&limit=&page_token=` searches one
  project. The query matches exactly an external customer id, an internal customer id, an
  alias, or a stored provider transaction or original transaction id, and as a
  case-sensitive prefix of the external or internal id. Each result carries `match`. Store
  references are compared by keyed fingerprint; purchase tokens and customer bindings are
  not searchable. An empty query lists customers newest first. Pages hold at most 100
  results and `next_page_token` is valid only with the same query.
- `GET /v1/admin/projects/{project_id}/customers/{customer_id}` returns identity, aliases
  and merges, every current entitlement with its `source` (`store_observation`, `import`,
  or `manual`), manual grants, and a bounded newest-first timeline built from stored
  observations, import grants, manual grants, entitlement changes, webhook outcomes,
  reconciliation jobs, and identity audit rows.
- `POST /v1/admin/projects/{project_id}/customers/{customer_id}/manual-grants` grants an
  entitlement through one product and application for `duration_days` or until `ends_at`,
  with a required `reason` and `idempotency_key`. The application must map the product to
  a store product; otherwise the grant is refused with `422 product_not_mapped`.
  `POST .../manual-grants/{grant_id}:revoke` ends it early with a required reason. Both
  require a stored administrator key or a dashboard session opened with an administrator
  account. Before the first of either exists, the bootstrap credential gets
  `403 forbidden`; after that it gets `401 unauthorized`.

Audit fields named `actor_key_id`, `revoked_by_key_id`, and `updated_by_key_id` hold the
public id of the stored key that made the change. When the change came from a dashboard
session opened with a username and password, they hold `account:<username>` instead.

A manual grant is an audited access source of kind `manual`. While it allows access, the
public entitlement and the webhook report `reason` `manual_grant`; after revocation they report
`revoked`. Imports keep their own reasons. It is never stored as purchase
evidence or a store observation and never appears in recent verifications or the
verification series. It feeds the normal projection and a changed projection enqueues the
normal webhook.

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

Minting a session does not create a customer, so a host can mint one before it attaches
an alias. The entitlement GET returns `404 not_found` until the external id is a customer
or an alias. When `purchases:verify` or a `purchases:restore` item stores a purchase for
an external id that is neither, IAPStack creates the customer, with a generated `cust-`
internal id, in the same transaction as that purchase. A request that fails before the
purchase is stored, for example on validation, catalog resolution, or provider
verification, creates no customer. After the first stored purchase, attaching the external
id as another customer's alias returns `409`; merge the customers instead. For the same
reason a RevenueCat import that lists such an external id as an alias refuses that
subscriber with `alias_has_customer`, so import before migrated users' purchases reach
IAPStack, or merge and import again.

Restore processes items sequentially under a 25-second server budget. If that budget
expires, the API returns retryable `503 request_timeout`; already completed items remain
durable, and resubmitting the same batch is safe because each verification is idempotent.

A purchase item contains an external customer ID, claimed products, optional customer bindings, and one provider evidence object. Huawei uses `application/vnd.iapstack.huawei-purchase+json`:

```json
{
  "purchase_data": "<exact InAppPurchaseData JSON string>",
  "signature": "<base64 detached signature>",
  "signature_algorithm": "SHA256WithRSA",
  "product_kind": "subscription"
}
```

`signature_algorithm` is optional. Omitted evidence uses `SHA256WithRSA`. `SHA256WithRSA/PSS` is also accepted. Any other value is rejected with `422 invalid_purchase`. IAPStack verifies the device signature with that algorithm, queries Huawei for current authoritative state, verifies the server response signature, checks application, product, subscription id, and token consistency, and commits evidence, projection, and outbox changes atomically. The stored observation keeps the authoritative price and currency. Subscription `purchase_data` must include `subscriptionId`. `applicationId` inside that JSON may be a number.

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

When subscription status has moved to another product than the submitted transaction, the previous product
is recorded as expired at the new purchase. When the submitted transaction already is the new product (the
StoreKit upgrade purchase, or an `UPGRADE` notification), verification also calls
[Get Transaction History](https://developer.apple.com/documentation/appstoreserverapi/get-transaction-history)
for that customer to find the replaced product. It does so only for a transaction purchased in the last 48
hours that is not the first in its lineage and whose `transactionReason` is not `RENEWAL`, with
`productType=AUTO_RENEWABLE`, newest-modified first, at most three pages, and a five-second budget. Each
history entry is verified like submitted evidence and must share the lineage and `appAccountToken`. Every
consecutive predecessor Apple marks `isUpgraded` inside that window is recorded as expired. The lookup uses
the same In-App Purchase key, so plan its rate limit with the other App Store Server API calls. It is
enrichment only: a failure leaves the verification result unchanged and is counted in
`iapstack_provider_calls_total` with operation `transaction_history`. Daily reconciliation replays the
submitted transaction, and Apple reconciliation by transaction reference makes no history call. A replaced
product the catalog does not map grants nothing, so its expired record is skipped instead of failing the new
purchase.

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
one non-consumable line item per purchase token. A subscription changed with the
`DEFERRED` replacement mode has two line items on its new purchase token. Until the
change takes effect, IAPStack records the existing item, which keeps access until its
expiry. Its renewal follows the new item's plan, and the new product is stored as the
next renewal product when that plan renews automatically. After the change, the
replacement item is recorded together with the replaced item as expired, once the
replaced item's expiry has passed, and later notifications for that token expect the
new product. Both products of that deferred replacement must stay mapped in the catalog.
A one-item response whose `itemReplacement` names another product also records that
product as expired for that token, whichever product the request names. When the catalog
does not map the replaced product, that expired record is skipped and the new product is
still stored and acknowledged. A claimed product may name either item of a replacement. A completed
purchase whose `acknowledgementState` is pending produces a bounded provider action.
IAPStack calls the matching Android Publisher product or subscription acknowledgement
endpoint only after the evidence, observation, entitlement projection, and outbox
event commit.
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
callback fields never grant access directly. A subscription notification stored before
`subscription_id` was recorded uses `subscriptionId` from the stored client purchase
evidence for that purchase token. When that evidence is missing, the worker retries
instead of failing the notification permanently.

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

A `CONSUMPTION_REQUEST` also schedules a refund consumption answer. It is off by default.
An operator turns it on per App Store application with
`PUT /v1/admin/projects/{project_id}/applications/{application_id}/apple-consumption`
(`enabled`, `refund_preference`, `sample_content_provided`, `expected_revision`). `GET` on the same path returns those settings and the 20 newest
requests with their status: `pending`, `sent`, `skipped`, `expired`, or `failed`, plus a
reason code. Consent is opt-in. Only the host backend records it, with the durable
application bearer; a customer session token gets `401`:
`PUT /v1/applications/{application_id}/customers/{external_customer_id}/apple-consumption-consent`
with `{"consented": true}` or `false`. The worker sends Apple's version 2 Send
Consumption Information body only when the customer's latest decision is `true` and IAPStack holds an
allowed observation for that transaction. It never sends `consumptionPercentage`.
See `docs/plans/apple-consumption-response.md`.

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

A subscription notification for an unknown token can still be bound when the token
replaces a stored purchase. Google issues a new purchase token for an upgrade, a
downgrade, a `DEFERRED` replacement, or a resubscribe before expiry, and names the old
token in `linkedPurchaseToken`. The worker reads `purchases.subscriptionsv2` for the
unknown token before any customer is known and stores nothing from that read. It then
looks up the linked token in its protected query scope. The new purchase's
`obfuscatedExternalAccountId` must be the old purchase's `external_customer_id`, or an
external id of the same canonical customer after a merge. When both hold, the token is
reconciled like a stored one for that customer and the product that grants access now.
Its state, projection, and outbox event commit first, and then IAPStack acknowledges the
purchase, which Google requires within three days. The daily refresh is scheduled for the
new token. Later notifications and redeliveries find the stored token and do not repeat
the link lookup. A token that replaces nothing, whose linked token is not stored yet, or
that Android Publisher does not return yet stays retryable. A different or missing
account id fails the job without a retry; the client can still submit that token for
its own customer. Voided-purchase notifications for unknown tokens are not bound
this way.
When that authoritative state requires purchase acknowledgement, the worker commits
the normalized state first and then calls Google using the same protected purchase
token. HTTP `409`, `429`, and `5xx` acknowledgement failures remain retryable.

Google Play Console test notifications and pending-refund-review notifications are
retained as authenticated audit records and complete without an entitlement change.
The pending chargeback review workflow itself is outside the current slice. Duplicate
Pub/Sub deliveries normalize to the same protected inbox identity and downstream
projection/outbox writes remain idempotent.

Each purchase lineage is a separate entitlement source: the App Store original
transaction, the Google Play purchase token, or the Huawei purchase token. Observation
times are compared only inside one lineage. A customer who lets a subscription expire
and buys the same product again keeps access from the new lineage when the old one is
refreshed. A store observation supersedes every RevenueCat import of its own
application and product. An administrator's manual grant is its own source: store
evidence never supersedes it, and it never supersedes store evidence. When several of
the remaining sources allow access, the one whose access ends last is projected,
whether it is a store observation, a manual grant, or an import. Equal ends prefer
store evidence, then a manual grant, then an import. The daily reconciliation of a
lineage stops once the provider reports every line item expired with automatic renewal
off. A lineage the provider can still renew, such as one in billing retry, keeps its
daily refresh. Refunded and revoked lineages keep their daily refresh too. A later resubscription is scheduled again when the
client submits it, or when an App Store or Google Play notification reports it.

## Webhook verification

Outbound requests include `IAPStack-Event-ID`, `IAPStack-Timestamp`, and `IAPStack-Signature`. Verify `v2=<hex>` as HMAC-SHA-256 over the unambiguous newline-delimited input:

```text
v2
<event_id>
<unix_timestamp>
<exact_raw_body>
```

The event ID is authenticated. Reject `v1` signatures, timestamps outside the application's replay window, and any delivery whose `IAPStack-Event-ID` does not match the MAC. Deduplicate by that authenticated event ID.

The body is the `entitlement.changed` payload modeled by `EntitlementChange` in the Go and TypeScript SDKs. It has no `event_type` field, and no header names the event type. `entitlement.changed` is the only event IAPStack delivers. `schema_version` 1 cites `source_observation_id`, version 2 has `source` `import` and `source_import_id`, and version 3 has `source` `manual` and `source_manual_grant_id`. Only manual-grant events use version 3; store events stay on version 1 and import events on version 2, so older SDK verifiers keep accepting them. Versions 2 and 3 are not store verification and carry no observation id. An allowed manual grant reports `access_reason` `manual_grant`. `application_id` follows the shared identifier rules: it can contain inner spaces and non-ASCII text and has no length cap.

## Shared SDK behavior

Behavior that is meant to be identical across the Go, TypeScript, React Native,
Flutter, Kotlin and Swift SDKs. The SDK contract tests assert this section.

- **Retries.** `429` and every `5xx` response, timeouts and transport failures
  are retried with bounded full-jitter backoff (3 attempts, 250 ms base, 2 s
  cap by default). Every omitted retry field takes its default separately.
- **`Retry-After`.** The server sets `Retry-After` on a `503
  provider_unavailable` when the store provider asked for a cooldown, as
  delay-seconds rounded up. Every SDK honors the header on a retryable
  response with one grammar: delay-seconds of one to nine ASCII digits, or an
  IMF-fixdate HTTP-date such as `Sun, 06 Nov 1994 08:49:37 GMT` whose fields,
  including the weekday, round-trip through the Gregorian calendar (`Fri, 31
  Feb 2026 00:00:00 GMT` is malformed, not 3 March; RFC 850 and asctime forms
  are malformed). A malformed header is ignored and an elapsed HTTP-date counts
  as zero. The wait before the next attempt is
  `max(jitter, min(Retry-After, 30 s))`: the 30-second cap is a client budget
  that cuts a longer cooldown short, so the retry may then land inside it, and
  it never reduces the configured jitter. The parsed cooldown is exposed on the
  API error (`RetryAfter`, `retryAfterMs`, `retryAfter`); Go reports zero for
  absent, malformed and elapsed alike.
- **`X-Request-ID`.** A blank request ID is dropped before sending. An error's
  request ID comes from the envelope `request_id`, falling back to the
  `X-Request-ID` response header.
- **`external_customer_id`.** Blank values and values with surrounding
  whitespace are rejected before a request is sent.
- **Timestamps.** Only RFC 3339 date-times are accepted.
- **`grantsAccess`.** Go and Swift expose a method (`GrantsAccess()`,
  `grantsAccess()`); TypeScript, React Native, Flutter and Kotlin expose a
  property (`grantsAccess`). Every SDK also offers `grantsAccessAt(instant)`.
- **`effective_starts_at`.** `grantsAccessAt` checks only `access` and
  `effective_ends_at`. The server only emits an `allowed` projection once its
  period has started, so `effective_starts_at` is informational; checking it on
  a device whose clock runs behind the server would deny access right after a
  purchase. Provider clock skew is already included in `effective_ends_at`
  when the server still allows access. The App Store allowance is five minutes
  past a signed expiry that Apple still reports as active. Clients do not add
  a second window.
- **`MemoryEventStore`.** The Go and TypeScript in-memory webhook stores never
  evict and are for tests and local development only.

## Go host SDK

The trusted-host Go package is in `sdk/go`. It uses the durable application bearer
to mint customer sessions, looks up entitlements with a minted customer session
because the public GET contract does not accept the application bearer, and
verifies `v2` webhook signatures with a replay window and event-ID dedupe. The
webhook handler maps signature failures to 401, identity conflict to 409, and
store failures to 503 so IAPStack retries only when delivery may succeed later.
It does not import `internal/` or talk to PostgreSQL. Never log or persist the
application bearer, and never ship it in a mobile binary.

## TypeScript host SDK

The trusted-host TypeScript package is in `sdk/typescript`. It uses the durable
application bearer to mint customer sessions, looks up entitlements with a
minted customer session because the public GET contract does not accept the
application bearer, and verifies `v2` webhook signatures with a replay window
and event-ID dedupe. The webhook handler maps signature failures to 401,
identity conflict to 409, and store failures to 503 so IAPStack retries only
when delivery may succeed later. It is for Node backends only. Never log or
persist the application bearer, and never ship it in a mobile or browser bundle.

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
