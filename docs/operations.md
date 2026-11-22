# IAPStack Operations Guide

## Required secrets

Generate independent values before starting the stack. Do not commit the resulting `.env` file.

```sh
export IAPSTACK_POSTGRES_PASSWORD="$(openssl rand -base64 32)"
export IAPSTACK_PROTECTION_ACTIVE_KEY_ID="key-2026-01"
export IAPSTACK_PROTECTION_KEYS="{\"key-2026-01\":\"$(openssl rand -base64 32)\"}"
export IAPSTACK_PROTECTION_FINGERPRINT_KEY="$(openssl rand -base64 32)"
export IAPSTACK_BOOTSTRAP_ADMIN_KEY="$(openssl rand -base64 48)"
export IAPSTACK_METRICS_BEARER_TOKEN="$(openssl rand -base64 48)"
```

The Compose stack passes the generated PostgreSQL password through a libpq-style
connection string so Base64 URL-reserved characters remain literal password data.
If you assemble `IAPSTACK_DATABASE_URL` yourself, percent-encode credentials used in
a URL-form connection string or use the libpq key/value form.

The fingerprint key must remain stable across encryption-key rotation. The bootstrap
admin key is an installation credential and must contain at least 32 bytes. Use it to
create the first administrator account through `/dashboard/`, or the first stored
administrator key through `POST /v1/admin/api-keys`. From then on the bootstrap key and
every bootstrap dashboard session are rejected, with no restart. A bootstrap key left in
the environment is a dormant credential: it only works again when no administrator
account and no active stored administrator key remains. Clearing
`IAPSTACK_BOOTSTRAP_ADMIN_KEY` afterwards is optional; clear it if you do not want to
keep that dormant credential. The default Compose worker and migration services never
receive this secret. Store every durable bearer outside the database because IAPStack
returns it only once.

## Configuration reference

IAPStack is configured entirely through environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `IAPSTACK_HTTP_ADDRESS` | `:$PORT` or `:8080` | API listen address in `host:port` form; takes precedence over a platform-provided `PORT` |
| `IAPSTACK_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown deadline |
| `IAPSTACK_READINESS_TIMEOUT` | `2s` | Deadline for one dependency readiness probe |
| `IAPSTACK_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `IAPSTACK_DATABASE_URL` | none | PostgreSQL connection string required by database-backed modes |
| `IAPSTACK_AUTO_MIGRATE` | `false` | Apply migrations at compact `server` startup; use only for one-instance disposable environments that cannot run a pre-deploy command |
| `IAPSTACK_BOOTSTRAP_ADMIN_KEY` | none | Installation-only administrator bearer; when set it must contain at least 32 bytes, and it is rejected while any administrator account or active stored administrator key exists |
| `IAPSTACK_METRICS_BEARER_TOKEN` | none | Dedicated 32+ character bearer for `/metrics`; the endpoint is disabled when absent |
| `IAPSTACK_AUTH_MAX_CONCURRENT_DERIVATIONS` | `4` | Fail-fast concurrency bound for memory-hard API-key creation and verification; maximum `32` |
| `IAPSTACK_PROTECTION_ACTIVE_KEY_ID` | none | Encryption key ID used for new protected values |
| `IAPSTACK_PROTECTION_KEY` | none | Base64-encoded or 64-character hexadecimal 32-byte encryption root key for an initial single-key deployment |
| `IAPSTACK_PROTECTION_KEYS` | none | JSON object mapping key IDs to base64-encoded 32-byte encryption root keys |
| `IAPSTACK_PROTECTION_FINGERPRINT_KEY` | none | Base64-encoded or 64-character hexadecimal 32-byte stable fingerprint root key |
| `IAPSTACK_WORKER_ID` | automatic | Optional River client identity; leave empty unless the deployment guarantees uniqueness |
| `IAPSTACK_WORKER_HTTP_ADDRESS` | `:8081` | Worker health, readiness, and metrics listen address |
| `IAPSTACK_WORKER_POLL_INTERVAL` | `1s` | Interval between idle durable-queue polls |
| `IAPSTACK_WORKER_JOB_TIMEOUT` | `30s` | Deadline for one durable job attempt |
| `IAPSTACK_WORKER_CONCURRENCY` | `4` | Maximum concurrent River jobs per queue and worker process |
| `IAPSTACK_WORKER_MAX_ATTEMPTS` | `12` | Attempts before River discards a retryable job |
| `IAPSTACK_QUEUE_RETENTION` | `720h` | Retention period for terminal River jobs and durable queue audit records |
| `IAPSTACK_HTTP_BODY_LIMIT` | `1048576` | Maximum request body size in bytes for JSON and provider notifications |
| `IAPSTACK_OFFERING_DIRECTORY` | none | Directory of one `{application_id}.json` offering file per application; empty answers `GET /v1/applications/{application_id}/offering` with not-found |
| `IAPSTACK_PROVIDER_TIMEOUT` | `15s` | Deadline for one outbound store-provider request |
| `IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS` | `false` | Explicitly permit Huawei provider endpoints on private, loopback, link-local, CGNAT, or benchmark addresses |
| `IAPSTACK_WEBHOOK_TIMEOUT` | `10s` | Deadline for one outbound application webhook request |
| `IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS` | `false` | Explicitly permit webhook delivery to private, loopback, link-local, CGNAT, or benchmark addresses |
| `IAPSTACK_ENVIRONMENT` | `development` | Bounded deployment label exported with metrics |
| `IAPSTACK_GRAFANA_OTLP_ENDPOINT` | none | Grafana Cloud HTTPS OTLP base URL; all three Grafana settings are optional together |
| `IAPSTACK_GRAFANA_OTLP_USERNAME` | none | Grafana Cloud OTLP instance ID used for Basic authentication |
| `IAPSTACK_GRAFANA_OTLP_TOKEN` | none | 32+ character metrics-write-only Grafana Cloud access-policy token |
| `IAPSTACK_GRAFANA_EXPORT_INTERVAL` | `1m` | Direct OTLP metric export interval |
| `IAPSTACK_GRAFANA_EXPORT_TIMEOUT` | `10s` | Deadline for one direct OTLP export attempt |
| `IAPSTACK_MCP_ENABLED` | `false` | Serve the [MCP endpoint](#mcp-endpoint) at `/mcp` from `server` and `api` modes; `true` or `false` |
| `IAPSTACK_MCP_MAX_CONCURRENT_REQUESTS_PER_KEY` | `4` | In-flight `/mcp` requests allowed per API key; further requests get `429` with `Retry-After` |
| `IAPSTACK_PUBLIC_URL` | none | Absolute `http` or `https` base URL of the API, without credentials, query, or fragment. Only used to print full store notification URLs in the MCP `get_setup_status` tool; never derived from a request `Host` header |

The local Compose files publish PostgreSQL and the API on `127.0.0.1`. Set
`IAPSTACK_POSTGRES_BIND_ADDRESS` or `IAPSTACK_HTTP_BIND_ADDRESS` only when an explicit
network boundary requires a different host address; keep production exposure behind
authenticated TLS ingress and network policy.

### Protection keys

Modes that handle provider evidence initialize the protection keyring before serving
work and fail fast when any protection variable is missing or malformed. Generate every
root key with a cryptographically secure generator such as `openssl rand -base64 32`.

Configure exactly one of `IAPSTACK_PROTECTION_KEY` or `IAPSTACK_PROTECTION_KEYS`. The
single-key form works with managed-platform secret generators for an initial
deployment; switch to the JSON keyring before rotating encryption keys. To rotate, add
the new key to `IAPSTACK_PROTECTION_KEYS`, select it with
`IAPSTACK_PROTECTION_ACTIVE_KEY_ID`, and retain old keys until every value written with
them has been re-encrypted or expired.

Keep the fingerprint key stable across ordinary encryption-key rotations. Changing it
alters idempotency fingerprints and requires an explicit data migration.

### Outbound network policy

Webhook delivery and configurable Huawei provider calls resolve and validate every
A/AAAA destination at connection time, do not follow redirects, and permit only public
network addresses by default. The private-network opt-ins are described in the
[production security baseline](#production-security-baseline).

## Production security baseline

- Terminate TLS at a trusted reverse proxy or load balancer. Never expose PostgreSQL
  or worker probes directly to the public Internet. Protect a public `/metrics`
  endpoint with its dedicated bearer and do not reuse an administrator/application key.
- Apply per-source and per-bearer request limits at the ingress. IAPStack bounds request
  bodies, headers, connection duration, provider calls, webhook calls, and worker
  concurrency. Memory-hard API-key derivations also fail fast at
  `IAPSTACK_AUTH_MAX_CONCURRENT_DERIVATIONS`, but v0.1 does not include a distributed
  rate limiter.
- Restrict PostgreSQL and process egress with network policy. Use authenticated,
  certificate-verified PostgreSQL TLS whenever traffic leaves one trusted host.
- Keep protection roots, provider credentials, webhook signing secrets, administrator
  bearers, and application bearers in a dedicated secret manager. Encrypt backups and
  test restore access controls as well as data integrity.
- Keep `IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS=false` for Internet webhooks. If a
  trusted internal application endpoint requires it, set the value to `true` for API
  and worker together and enforce the exact destination with an egress firewall or
  service-mesh policy.
- Keep `IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS=false` for Huawei's public services. If
  an approved private provider fixture or gateway requires it, set the value to `true`
  for API and worker together and enforce the exact destination with an egress firewall
  or service-mesh policy.
- Configure webhook receivers to verify `v2` `IAPStack-Signature` over
  `v2\\n<event_id>\\n<unix_timestamp>\\n<raw_body>`, reject `v1` signatures and stale
  timestamps, and deduplicate the authenticated `IAPStack-Event-ID` before applying an
  event.

Use `GET /v1/admin/api-keys` to inspect secret-free lifecycle metadata. Each response
contains at most 200 keys, newest first. Follow `next_page_token` as `page_token`
until it is absent so an older key remains available to revoke. Use
`DELETE /v1/admin/api-keys/{key_id}` to revoke a stored key. Revocation is idempotent,
authentication reads `revoked_at` on every request, and no process restart is required.
The API rejects revocation of the key authenticating the current request and preserves
the final active administrator: the last administrator key can be revoked only while an
administrator account exists, and the last account can be deleted only while an active
administrator key exists. Rotate by creating the replacement, storing it, signing in
with it, and then revoking the old key.

### Recovering administrator access

A lost administrator key needs no recovery while an account can sign in: sign in with
the username and password, revoke the lost key under **Access**, and create a new one.

If a password is forgotten, the account is locked, or no administrator can sign in at
all, reset the password on the server. The command needs only database access, so
whoever can run it already controls the installation:

```sh
docker compose -f deploy/compose.yaml exec api /usr/local/bin/iapstack admin reset-password <username>
```

Outside Compose, run `iapstack admin reset-password <username>` with
`IAPSTACK_DATABASE_URL` set. It prints a generated temporary password once, clears the
account's sign-in lock, and ends the dashboard sessions the account issued. If no
account has that username, it creates one, which also covers installations that only
ever used keys. Sign in with the temporary password and change it under **Access**. The
password goes to the terminal, so do not run the command where output is logged.

Account sign-in is throttled per account: five consecutive wrong passwords lock the
account for five minutes, and until a sign-in succeeds every further wrong password
locks it again. Someone who knows a username can therefore keep that account locked;
keys and the reset command are unaffected. Keep the ingress rate limits from the list
above in front of `/v1/admin/dashboard-session`.

To cut off every stored administrator key at once, for example after a leak, use this
restricted, break-glass database operation. It revokes every active administrator key
(and therefore every dashboard session those keys issued) under the repository's
lifecycle lock. Administrator accounts keep working:

```sql
BEGIN;
SELECT pg_advisory_xact_lock(5278588522471178564);
UPDATE api_keys
SET revoked_at = now()
WHERE role = 'admin'
  AND revoked_at IS NULL
RETURNING id, revoked_at;
COMMIT;
```

Application keys are not affected. If no administrator account exists either, the
bootstrap key works again after this: make sure `IAPSTACK_BOOTSTRAP_ADMIN_KEY` is set
(restore it and recreate the API container if you cleared it), sign in at `/dashboard/`
with it, and create an administrator account. The bootstrap key retires again as soon
as that account exists.

The following restricted database operation revokes one specific key and is also
break-glass only. It uses the same lifecycle lock as the repository and never revokes
the last active administrator key, even when an administrator account exists, but it
bypasses the API's current-session safeguard. Confirm the exact
public key ID and an active replacement before running it:

```sql
BEGIN;
SELECT pg_advisory_xact_lock(5278588522471178564);
UPDATE api_keys
SET revoked_at = now()
WHERE id = '<public-key-id>'
  AND revoked_at IS NULL
  AND (
    role <> 'admin'
    OR 1 < (SELECT count(*) FROM api_keys WHERE role = 'admin' AND revoked_at IS NULL)
  )
RETURNING id, role, revoked_at;
COMMIT;
```

The complete security assumptions, residual risks, and release gate are documented in
[the v0.1 threat model](security.md).

## Start and inspect

```sh
docker compose -f deploy/compose.yaml up --build -d
docker compose -f deploy/compose.yaml ps
curl --fail http://127.0.0.1:8080/readyz
curl --fail -H "Authorization: Bearer $IAPSTACK_METRICS_BEARER_TOKEN" http://127.0.0.1:8080/metrics
```

The migration container must complete successfully before API and worker processes start. `/healthz` is process liveness; `/readyz` also probes PostgreSQL.

`/readyz` also reports the running build, so you can check which release an upgrade
deployed: `{"status":"ready","version":"v0.2.0","commit":"<full SHA>"}`. The body used to
be `{"status":"ready"}` alone; a monitor that matched it byte for byte should check the
status code or the `status` field instead.

The GHCR release images and the VPS installer stamp the version and commit. Every other
image built from the `Dockerfile` has no `.git` in its build context, so it reports
`"version":"dev"` and no commit unless the build passes them as arguments, for example
`docker build --build-arg IAPSTACK_COMMIT="$(git rev-parse HEAD)" .` (add
`--build-arg IAPSTACK_VERSION=<tag>` for a release tag). That includes local Compose
builds and, until their configurations pass the arguments, the Fly.io (`fly.toml`),
Railway, DigitalOcean App Platform (`.do/app.yaml`), Heroku (`heroku.yml`), Render
(`render.yaml`), Koyeb (`deploy/koyeb/deploy.sh`) and Coolify
(`deploy/coolify/compose.yaml`) deployments.

Version and commit are public on this unauthenticated probe. They identify the release,
not any secret, but they also tell anyone which release you run, so keep up with
security releases.

## Operations dashboard

Open `http://127.0.0.1:8080/dashboard/` after the API becomes ready. Sign in with an
administrator account's username and password. Before the first account exists, choose
"Use an admin or bootstrap key instead" and sign in with the bootstrap key (a stored
administrator bearer works there too). A bootstrap session shows a setup banner that
creates the first administrator account and switches the browser session to it. Manage
accounts and keys under **Access**: add or delete accounts, change your password, reset
another administrator's password, and create or revoke keys. Keys remain the credential
for scripts and backends; nobody needs one to sign in. The server exchanges the
credential once for an encrypted 12-hour dashboard session in a `Secure`, `HttpOnly`, `SameSite=Strict`
cookie scoped to `/v1/admin`; the page never stores the bearer in browser storage.
The session survives refreshes and browser restarts until expiry, while signing out
clears it immediately. Sessions issued by stored administrator keys also stop working
when their issuer key is revoked, and sessions issued by an account stop working when
that account is deleted or its password changes. Cookie-authenticated mutations require same-origin
browser proof. Do not use the dashboard from an untrusted browser or expose the API
over plaintext outside local development.

The project overview shows application credential/webhook coverage, catalog mappings,
customer access counts, recent normalized purchase observations, queue outcomes, and
webhook delivery metadata. Its commerce panel charts the last 30 UTC days of verified
access outcomes. That chart is daily verification activity, not a financial period.
Sandbox projects show zero real revenue and separately group provider-signed test
transaction prices by currency. The test value uses the latest non-reversed snapshot of
each purchased transaction, never converts currencies, and is not accounting revenue.
Apple transactions provide this signed price metadata today; older observations report
missing-price coverage until a fresh verification enriches them. Production projects
stay on Reports required until every production application appears in its provider's
accepted official store financial report. A file does not name its store account. If
it includes one production application and omits another of the same provider, the
card keeps the imported currency totals, lists the omitted application ids, and stays
on Reports required. Those omitted applications are counted as zero, which can mean
no sales or a separate account whose report was not imported. When some providers
have no report, the card lists the matched proceeds and names the providers that
still need a report. Those lines are not a complete project total. When every
production application is present in its provider's report, the card lists each
imported period and currency. The card does not convert currencies and does not
calculate tax or commission. Purchase prices are not revenue. The dashboard never
renders report files, order ids, protected provider payloads, or secrets.
The Subscriptions panel counts subscription lineages of one application environment
over 7, 30, or 90 UTC days: active now, new, renewals, and churn by reason, with a daily
active series and a product table. It reads only stored, provider-signed purchase
observations, so a renewal or expiry that IAPStack has not yet observed (by
notification or the daily reconciliation) is not counted yet. Trials and trial
conversion appear only where the adapter records a signed offer phase (Apple, Google
Play); Huawei shows "Not available". Apple transaction reasons and original purchase
dates separate plan changes and existing subscribers from renewals and new
subscriptions, and Google Play cancellation context marks replacements. Observations
whose provider evidence carried none of these fields use simpler rules until
reconciliation enriches them. The panel shows no money figure.
The access-key workspace lists active and revoked administrator/application key
metadata, identifies the current stored key, creates one-time administrator bearers,
and requires explicit confirmation before revocation.
The quick-start dialog creates an Apple App Store, Google Play, or Huawei AppGallery project,
application, entitlement, product, and provider-product mapping. Open an application
card to create or rotate its provider-specific protected server credential, configure
signed webhook delivery, and create a one-time application bearer for the Flutter SDK.
Apple setup accepts the App Store Connect issuer/key identity, matching bundle and App
Apple IDs, PKCS#8 In-App Purchase private key, and one or more PEM-encoded trusted Apple
roots. Google Play setup accepts an Android Publisher service-account JSON and optional
RTDN authentication scope. Credential files are bounded, parsed in browser memory, and
sent only through the protected credential request when the operator saves the form;
they are not retained as uploaded files. Firebase `google-services.json` and Huawei
`agconnect-services.json` remain mobile build inputs and must not be sent to IAPStack.
Customers can also be created from the project overview.
Credential and signing-secret fields are cleared after submission and never returned
by the admin read API. Store every newly displayed application bearer immediately in
your deployment secret manager; the dashboard cannot recover it after the dialog closes.

## MCP endpoint

The API process can serve a [Model Context Protocol](mcp.md) endpoint so AI agents
(Claude Code, Cursor, Codex, and other MCP clients) can answer support and setup
questions without hand-written `curl` calls, and, with a key that may write, manage
manual grants, customer aliases, and catalog definitions. It is off by default.

1. Set `IAPSTACK_MCP_ENABLED=true` on the `server` or `api` process and restart it.
   The worker never serves `/mcp`. Optionally set `IAPSTACK_PUBLIC_URL` to the
   external API base URL so `get_setup_status` prints complete store notification URLs.
2. Create an agent key for one project, from **Access keys** in the dashboard or with
   `POST /v1/admin/api-keys` and `{"role":"agent","project_id":"..."}`. The key is
   read-only. Add `"allow_writes":true` only for an agent that should create and revoke
   manual grants, attach aliases, or define entitlements, products, and store products
   in that project; see [Write tools](mcp.md#write-tools).
3. Configure the client with the URL `https://<api-host>/mcp` and the header
   `Authorization: Bearer <agent key>`. [MCP endpoint](mcp.md) has client examples.

When the endpoint is disabled, `/mcp` answers exactly like any other unknown path.

Reverse proxies and ingress controllers that forward an allow-list of paths must add
the exact path `/mcp` (POST only). The bundled Kubernetes Ingress forwards only `/v1` and
`/dashboard`; uncomment its `/mcp` entry after enabling the endpoint. Do not rewrite or
strip the `Authorization` header, and do not buffer responses longer than the 30-second
server write timeout. The endpoint answers each request with one JSON body and keeps no
long-lived streams or sessions, so it needs no sticky sessions.

Limits:

- Only stored `agent` and `admin` API keys authenticate. Application keys get `403`,
  the bootstrap administrator gets `403`, and dashboard session cookies are ignored, so a
  request without a bearer gets `401`. Prefer agent keys: an admin key reaches every
  project.
- Request bodies use `IAPSTACK_HTTP_BODY_LIMIT` (`413` above it). Each key may have
  `IAPSTACK_MCP_MAX_CONCURRENT_REQUESTS_PER_KEY` requests in flight (`429` with
  `Retry-After` above it). Authentication shares the bounded
  `IAPSTACK_AUTH_MAX_CONCURRENT_DERIVATIONS` capacity with `/v1` (`503` when exhausted).
- `GET` and `DELETE` get `405`. A browser request whose `Origin` differs from the
  request host gets `403`; no CORS headers are sent.
- One serialized tool result is capped at 256 KiB. Lists are paginated with `next_page_token`.
- Each tool call writes one structured log line (`mcp tool call`) with the tool name,
  key id, role, channel, whether the tool writes, outcome code, and duration; a write
  call also logs its `project_id`, and a successful definition write logs the
  `definition` id. Other arguments and results are never logged.
- Manual grants, revocations, and aliases written through MCP are stored with the agent
  key id and the channel `mcp`, and the customer detail shows it. Entitlement, product,
  and store product definitions have no stored audit row on any transport, so the log
  line is the only record of an agent's definition writes. Retain these log lines if
  you need that record.

## Huawei AppGallery notification setup

Configure Huawei IAP V2 with the application's complete callback URL:

```text
https://iapstack.example/v1/providers/huawei/projects/<project_id>/applications/<application_id>/notifications
```

The endpoint is public HTTPS and does not use an IAPStack bearer or custom header.
Choose either Huawei `SHA256withRSA` or `SHA256withRSA/PSS` subscription notification
signatures and keep the matching IAP public key in the protected application credential.
IAPStack verifies signed subscription status before inbox insertion. Huawei order
notifications are token-only signals: they can trigger processing only after their
protected token resolves to a purchase already verified for the same application and
product kind. That lookup and inbox insertion are atomic; unknown tokens or mismatched
products return `422` without durable work. Access changes only after a successful Huawei
server query.

A `400` response indicates an invalid V2 body, signature, algorithm, or provider scope;
`404` means the path scope is unknown; `422` means an unsigned order token is unknown or
does not match its product; `502` indicates invalid protected Huawei credentials; and
`503` means durable storage is temporarily unavailable. Put a bounded rate limit in the
public reverse proxy because order wrappers are not independently signed by Huawei, but
do not rewrite the request body.

## Apple App Store notification setup

Configure the App Store Server Notifications V2 production and sandbox URLs with the
application's exact IAPStack scope:

```text
https://iapstack.example/v1/providers/apple/projects/<project_id>/applications/<application_id>/notifications
```

The endpoint must be publicly reachable over HTTPS. It authenticates Apple's
`signedPayload` and does not accept an application bearer. The application's protected
Apple credential must include the matching bundle ID, production App Apple ID, App
Store Server API private key, and trusted Apple roots. Use App Store Connect's test
notification and confirm one completed `app_store_server_notification_v2` inbox row.

A `400` response indicates an invalid signature, certificate chain, V2 envelope, or
application scope. A `404` indicates that the URL scope does not resolve to an Apple
application. A `502` indicates invalid protected Apple credential configuration, and
`503` means durable storage is temporarily unavailable. Apple retries unsuccessful
deliveries; IAPStack acknowledges only after protected inbox persistence.

### Refund consumption answers

When a customer asks Apple for a refund, Apple sends `CONSUMPTION_REQUEST` and accepts
an answer for 12 hours. IAPStack records every such request. It answers only when the
operator enabled **Refund consumption answers** in the application's connections dialog,
the customer's consent is established, and IAPStack verified that transaction with
allowed access. Everything else is recorded as `skipped` with a reason, such as
`disabled`, `no_consent`, `consent_withdrawn`, `no_delivery_evidence`, or `family_shared`.

Consent is opt-in. Before you turn it on, make your host backend call
`PUT /v1/applications/{application_id}/customers/{external_customer_id}/apple-consumption-consent`
with the application key after the customer answers your consent prompt. Customer session
tokens cannot write consent. Customers without a recorded yes are skipped, and recording
`{"consented": false}` withdraws consent.

Apple's consent and privacy-label rules are on the Send Consumption Information page. You
are responsible for obtaining valid consent. Rate-limited and `5xx` answers are retried
with a doubling delay that starts at one minute, stops growing at one hour, and never
passes the deadline. A request still open at the deadline is `expired`. An HTTP `400`,
`401`, or `404` answer is `failed` at once; the dialog shows the reason and the last HTTP
status is in the API response.

## Google Play RTDN push setup

Configure one authenticated Pub/Sub push subscription per Google Play application.
The push endpoint embeds both IAPStack scope identifiers because Pub/Sub does not add
application-defined headers:

```text
https://iapstack.example/v1/providers/google-play/projects/<project_id>/applications/<application_id>/notifications
```

Grant `google-play-developer-notifications@system.gserviceaccount.com` the Pub/Sub
Publisher role on the topic. Create a dedicated push-auth service account, allow the
Pub/Sub service agent to mint its OIDC tokens, and configure the subscription with an
explicit audience. For example:

```sh
gcloud pubsub topics add-iam-policy-binding "${RTDN_TOPIC}" \
  --member='serviceAccount:google-play-developer-notifications@system.gserviceaccount.com' \
  --role='roles/pubsub.publisher'

gcloud iam service-accounts add-iam-policy-binding "${RTDN_PUSH_SERVICE_ACCOUNT}" \
  --member="serviceAccount:service-${GOOGLE_CLOUD_PROJECT_NUMBER}@gcp-sa-pubsub.iam.gserviceaccount.com" \
  --role='roles/iam.serviceAccountTokenCreator'

gcloud pubsub subscriptions create "${RTDN_SUBSCRIPTION}" \
  --topic="${RTDN_TOPIC}" \
  --push-endpoint="${IAPSTACK_GOOGLE_PLAY_NOTIFICATION_URL}" \
  --push-auth-service-account="${RTDN_PUSH_SERVICE_ACCOUNT}" \
  --push-auth-token-audience="${IAPSTACK_GOOGLE_PLAY_NOTIFICATION_AUDIENCE}"
```

Store the resulting full subscription resource name, push service-account email, and
audience in the protected Google Play credential `rtdn` object. The configured audience
must exactly match the token audience; it may equal the endpoint URL. IAPStack also
requires the OIDC token's verified email claim to equal the configured service account.
Use Play Console's test notification after deployment, confirm one completed inbox
record, then test a sandbox purchase and authoritative worker reconciliation. Repeated
`401` responses indicate OIDC email/audience mismatch; `400` indicates an invalid
Pub/Sub or DeveloperNotification envelope; `502` indicates incomplete provider
credential configuration.

For a completed non-consumable or new subscription purchase, confirm that the Android
Publisher query reports `ACKNOWLEDGEMENT_STATE_ACKNOWLEDGED` after IAPStack commits the
entitlement. IAPStack deliberately sends the acknowledgement after its database
transaction for both client submissions and RTDN reconciliation: a transient Google
failure leaves the entitlement durable and causes the API request or worker job to retry.
The RTDN itself is retained as encrypted reconciliation evidence and cannot grant access
without the protected purchase-token, product, kind, and customer-binding lookup.
A replacement token that no client submitted is bound from its RTDN only through the
stored token in its `linkedPurchaseToken` and a matching obfuscated account id (see
`docs/api-v1.md`). That adds one `subscriptionsv2.get` call per worker attempt for an
unknown subscription token. An unknown token stays retryable until
`IAPSTACK_WORKER_MAX_ATTEMPTS`, about 11 hours with the default 12 attempts, which is
inside Google's three-day acknowledgement window. If the attempts run out before the
token is bound, Google refunds and revokes the unacknowledged purchase.
Google `409` concurrent updates, `429` rate limits,
and `5xx` responses are retryable. A persistent `401` or `403` means the protected
service account credential or Play Console application permission must be corrected.
Consumable products are not accepted by this slice and require a future fulfillment
policy before `purchases.products.consume` can be enabled safely.

## Backup and restore rehearsal

Create a compressed logical backup:

```sh
docker compose -f deploy/compose.yaml exec -T postgres \
  pg_dump -U "${IAPSTACK_POSTGRES_USER:-iapstack}" \
  -d "${IAPSTACK_POSTGRES_DB:-iapstack}" --format=custom > iapstack.backup
```

Restore into a new empty database rather than overwriting the running source:

```sh
docker compose -f deploy/compose.yaml exec -T postgres \
  createdb -U "${IAPSTACK_POSTGRES_USER:-iapstack}" iapstack_restore
docker compose -f deploy/compose.yaml exec -T postgres \
  pg_restore -U "${IAPSTACK_POSTGRES_USER:-iapstack}" \
  -d iapstack_restore --clean --if-exists < iapstack.backup
```

Validate table counts, application configuration, entitlement snapshots, and queue state in the restored database. Delete the rehearsal database only after validation.

## Upgrade and rollback

1. Back up PostgreSQL and record the currently deployed image digest.
2. Run the new image in `migrate` mode. Migrations are transactional and tested up/down from an empty database.
3. Roll API processes, then workers. Confirm readiness and queue depth metrics.
4. For an application rollback, deploy the recorded image digest. For a schema rollback, stop API and workers first, run the migration tool to the explicitly tested prior version, then restore the prior image.

The schema starts from one baseline migration (schema version 1). A database created by
a development build from before that baseline has a higher recorded version and cannot
be migrated: the `migrate` step fails. Recreate that database (for Compose, remove its
volume) and run `migrate` again.

Application migrations do not upgrade the PostgreSQL server itself. PostgreSQL 18
stores its versioned cluster beneath `/var/lib/postgresql`, so the Compose definitions
mount the named volume at that parent directory. A PostgreSQL 17 volume cannot be
attached to the PostgreSQL 18 service and upgraded in place. Before changing an
existing installation, stop application writes and perform a tested logical
dump/restore or `pg_upgrade` procedure that retains the PostgreSQL 17 volume for
rollback. Fresh installations initialize the PostgreSQL 18 layout automatically.

Never remove an encryption key until all rows written with that key ID have been re-encrypted or expired.

## Reproject stored entitlements

`customer_entitlements` stores the projection the projector chose at the last refresh of each
customer. A refresh runs on verify, restore, store notifications, reconciliation, RevenueCat
import, customer merges, and manual grants. When a release changes how the projector ranks
sources, rows chosen by the old order stay in place until that customer's next refresh, and the
public read path keeps returning them.

The projector now compares when allowed access ends before it compares the source kind. Before
that change a store observation outranked an import before ends were compared, so a customer
whose store purchase of one product had lapsed could stay denied although a current import of
another product granted the same entitlement. `iapstack-reproject-entitlements` refreshes those
customers at once instead of waiting for their next refresh.

The command is a separate binary that is not in the container image. Build it from a checkout of
the release you deployed and run it against the same database, after the new API and worker
processes are live. Run the built binary rather than `go run`: `go run` reports exit status 1 for
any failure, so a script could not tell a usage error from a failed run.

```bash
go build -o iapstack-reproject-entitlements ./cmd/iapstack-reproject-entitlements
```

```bash
IAPSTACK_DATABASE_URL='postgres://...' ./iapstack-reproject-entitlements --all-projects --report reproject-dry-run.jsonl
```

```bash
IAPSTACK_DATABASE_URL='postgres://...' ./iapstack-reproject-entitlements --all-projects --apply --report reproject-apply.jsonl
```

- Select the scope with exactly one of `--project <id>` or `--all-projects`. A `--project` that
  does not exist is refused.
- The default is a dry run (`--dry-run`). `--apply` commits the refreshes. The two cannot be combined.
- Each keyset page examines `--batch-size` canonical customers in id order (default 100, at most
  1000). By default a page keeps only the examined customers whose closure holds an allowed import
  grant and an allowed store observation of another application product that grants the same
  entitlement. That is the only pair the new order ranks differently. The filter runs only on the
  examined customers, so one page stays cheap however few customers match. `--all-customers`
  refreshes every canonical customer instead, for a later projector change that affects other
  sources.
- Each customer is refreshed in its own serializable transaction. It locks the customer and every
  customer merged into it before reading, as verification, import, merges, and manual grants do,
  and then runs the same refresh with the default projector. A concurrent request for the same
  customer waits or retries, so the command can run while the server is live.
- A changed projection enqueues the same `entitlement.changed` outbox event as any other refresh.
  A customer whose rows already match their sources writes nothing and enqueues nothing, so the
  command is safe to rerun and to stop with Ctrl-C between customers.
- A customer that is merged into another after its page was read is skipped. The merge already
  refreshed its canonical customer.
- A customer whose refresh fails, for example because a long request holds its lock past the
  retries, is recorded in the summary and the run continues with the next customer.
- A dry run performs the same locks and writes and then rolls each customer transaction back. It
  reports exactly what an apply at the same moment would write, including the events, but nothing
  is committed. It still holds each customer's lock for the length of one refresh.

The report is JSON Lines on stdout, or in the `--report` file (mode 0600). The file is opened only
after the database is, so a run that cannot connect leaves an earlier report in place. Each
customer whose projections changed, or would change, is one `{"customer": ...}` line with `mode`,
`project_id`, `customer_id`, `evaluated_at`, `projections_changed`, `webhooks_enqueued`, and one
entry in `changes` per entitlement. Each entry has `entitlement_id`, `entitlement_key`, and the
`before` and `after` stored projection: `source_kind`, `source_id`, `source_application_id`,
`source_product_id`, `source_customer_id` (the merged customer that owns the source, or the
customer itself), `stored_access`, `access` and `access_reason` as the public read path returns
them at `evaluated_at`, `ends_at`, and `version`. `before` is `null` when the customer had no row
for that entitlement. The report holds no purchase evidence or secrets.

The last line is `{"summary": ...}` with `mode`, the `projects` walked, and these counts:
`customers_scanned` (every selected customer the run tried, including skipped and failed ones),
`customers_skipped`, `customers_failed`, `customers_changed`, `projections_changed`, and
`webhooks_enqueued`. `failed_customers` lists each failed customer with its `project_id`,
`customer_id`, and an `error` class such as `unavailable`, `conflict`, or `failed`. `stopped` is
set when the run ended early, with the `project_id`, the last `after_customer_id` it finished
there, and the `error` class.

A customer line is written right after its transaction commits. If the process is killed at the
moment of a commit, that customer's change and event can be committed without a report line, and
a rerun finds nothing left to change for it. Treat the report as the list of changes the command
saw, not as a complete audit; the outbox and each row's `version` are the record of what was
written.

The command exits 0 when every selected customer was refreshed, 2 for invalid usage or a missing
project, and 1 otherwise, with one line on stderr that names the cause without secrets:

- If some customers failed, the run still walked every other customer. Rerun the same command to
  retry them; customers already fixed write nothing.
- If the run stopped early (Ctrl-C, a page that could not be read, or a report that could not be
  written), stderr and `stopped` name the project and the last finished customer. Resume with
  `--project <project_id> --after-customer <after_customer_id>`, or rerun from the start.

Review the dry-run report before applying. Webhook receivers get one event per changed
entitlement, so a large apply can raise outbox depth for a while.

## Queue incident response

- `iapstack_queue_depth` shows River's available, scheduled, retryable, running, completed, cancelled, and discarded states.
- `iapstack_queue_oldest_runnable_age_seconds` reports how long each queue's oldest runnable job has waited.
- `iapstack_queue_job_duration_seconds` reports bounded worker attempt count and duration sums by queue and outcome.
- River owns job claims, stale-job recovery, exponential retry scheduling, and terminal job cleanup.
- Retryable failures become discarded after `IAPSTACK_WORKER_MAX_ATTEMPTS`; permanent failures are cancelled immediately.
- A failed terminal audit write on the final attempt is snoozed for 15 seconds and logged at warn with `queue_transition_failed`. River's `snoozes` metadata caps that loop at 20 (about five minutes). The next run does not call the handler again. It logs `terminal_audit_write_failed` at error and increments `iapstack_queue_attempts_total{outcome="terminal_audit_write_failed"}`, then River discards the job. The durable outbox or reconciliation row can remain pending. Correlate `message_id`. Each snooze runs the handler again, so webhook receivers must keep deduplicating `IAPStack-Event-ID`.
- Inbox, outbox, and reconciliation tables retain protected payloads and stable terminal audit outcomes while River job arguments contain identifiers only.
- `IAPSTACK_QUEUE_RETENTION` applies to both River metadata and durable queue audit rows. Pending or otherwise runnable records are never removed by retention cleanup.
- Durable records contain only safe error codes; correlate application logs with `message_id` and HTTP logs with `X-Request-ID`.

## Missed subscription reconciliation

`purchases:verify` and `purchases:restore` commit a subscription before they schedule its daily reconciliation job. Those writes use separate transactions, so a scheduling failure does not roll back the entitlement. The HTTP call still returns the committed snapshot. The API logs `reconciliation_schedule_failed` with `project_id`, `application_id`, and `customer_id`, and increments `iapstack_verifications_total{outcome="reconciliation_schedule_failed"}`. It does not log purchase evidence or the storage error text.

That failure also writes a protected row in `reconciliation_schedule_retries`. The worker reads due rows (`next_attempt_at` in the past, not scheduled, not dead) about once a minute and inserts the daily reconciliation job. The insert is idempotent for the same protected payload, so a later verify or a second worker pass does not create another job. A failed promotion increments `attempts` and waits with exponential backoff from one minute up to one hour, so a row that can never succeed leaves the due batch. After 12 attempts the row is marked dead and the worker logs `reconciliation_schedule_retry_dead` with `project_id`, `application_id`, `customer_id`, and `attempts`. It does not log purchase evidence or the storage error text. The worker logs `reconciliation_schedule_retry_failed` when a pass cannot record that backoff.

Completed and dead rows keep their protected payload until `IAPSTACK_QUEUE_RETENTION`, then the same cleanup that purges terminal inbox, outbox, and reconciliation rows deletes them. Pending rows are not purged. Migration 013 refuses to reverse while any unscheduled row remains, including a dead row.

Until that job exists, a store that does not send lifecycle notifications will not refresh expiry or refund for that subscription.

Resubmitting the same subscription through `purchases:verify` or `purchases:restore` still schedules idempotently for the same UTC day. Confirm a reconciliation row for that customer, then watch `iapstack_queue_depth` for the reconciliation queue. Alert on any increase of `iapstack_verifications_total{outcome="reconciliation_schedule_failed"}`, on `reconciliation_schedule_retry_failed`, and on `reconciliation_schedule_retry_dead`.

## Compose release gate

Run the same clean-volume release gate required by GitHub CI:

```sh
./deploy/e2e/run.sh
```

The harness creates an isolated Compose project and temporary TLS certificate, then removes its containers and volume on exit. It exercises the production `migrate`, `api`, and `worker` modes with PostgreSQL 18 and a separate TLS fixture service.

The gate verifies:

1. Migration ordering and API/worker readiness from an empty volume.
2. Stored admin and application key creation, bootstrap credential retirement once the first stored admin key exists, and full Huawei application configuration. The harness hands the stored admin key to later phases through a private temporary file.
3. Authoritative signed lifetime verification and idempotent duplicate submission.
4. HMAC-SHA-256 webhook headers and one logical delivery per entitlement version.
5. Duplicate signed Huawei notification persistence while the worker is stopped.
6. River recovery after worker restart, authoritative refund projection, and duplicate notification suppression.
7. API and worker Prometheus metric families.

Ports default to `18080` for API, `18081` for worker metrics, `18082` for fixture controls, and `15432` for PostgreSQL. Override `IAPSTACK_HTTP_PORT`, `IAPSTACK_E2E_WORKER_PORT`, `IAPSTACK_E2E_FIXTURE_PORT`, or `IAPSTACK_POSTGRES_PORT` when those ports are occupied.

On failure the harness prints Compose status and logs before cleanup. The fixture uses generated test-only RSA and TLS keys; it never contacts Huawei or an application-owned endpoint.

## Release publication

Alpha, release-candidate, and stable releases are created only through
`.github/workflows/release.yml`. Run the workflow manually from `main` after main CI
has succeeded. Supply a numbered alpha such as `v0.1.0-alpha.1`, a numbered candidate
such as `v0.1.0-rc.1`, or a stable version such as `v0.1.0`; do not create the Git tag
first.

An alpha release requires a public known-limitations note and uses the exact successful
`main` commit as its automated test candidate. It is marked as a prerelease, publishes
only its exact container tag, and does not attach or claim completed real-provider
evidence. Use alpha releases for integration evaluation while a provider gate is
externally blocked.

Release candidates and stable releases additionally require a version-specific
evidence pull request. Before any registry or release write, the workflow requires:

1. A valid schema-v2 `docs/releases/<version>-release-evidence.json` record containing
   the Apple App Store, Google Play, and Huawei AppGallery gates.
2. Evidence whose release matches the requested version.
3. A tested candidate commit that is an ancestor of the release commit.
4. Exactly one changed path between the candidate and release commit: the evidence
   file itself.
5. A successful candidate CI run at the exact URL recorded in evidence.
6. A successful push CI run for the evidence merge commit.
7. No existing Git tag or GitHub release for the requested version.

The protected `stable-release` GitHub environment should require an operator review
for every publication. After validation, the workflow publishes `linux/amd64` and
`linux/arm64` images to GitHub Container Registry. Release candidates receive only
their exact version tag and are GitHub prereleases; stable versions additionally
update the minor and `latest` tags. The image carries
BuildKit provenance and SBOM attestations, and the workflow adds a signed GitHub build
provenance attestation. It then creates the GitHub release and attaches the secret-free
aggregate evidence file.

Deploy by immutable digest from the release notes instead of a mutable tag. Verify the
published provenance before deployment:

```sh
gh attestation verify \
  oci://ghcr.io/imariman/iapstack@sha256:<release-digest> \
  -R imariman/iapstack
```

If image publication succeeds but GitHub release creation fails, do not change or
delete the image tags. Investigate the failed workflow and rerun the same version only
while the Git tag and GitHub release remain absent; the digest must remain identical.
