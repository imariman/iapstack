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
admin key is an installation credential and must contain at least 32 bytes: use it to
create stored admin/application keys through `POST /v1/admin/api-keys`, then remove it
from the runtime environment and recreate the API container. The default Compose worker
and migration services never receive this secret. Store every durable bearer outside
the database because IAPStack returns it only once.

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
- Configure webhook receivers to verify `IAPStack-Signature` over
  `<unix_timestamp>.<raw_body>`, reject stale timestamps, and deduplicate the stable
  `IAPStack-Event-ID` before applying an event.

Use `GET /v1/admin/api-keys` to inspect secret-free lifecycle metadata and
`DELETE /v1/admin/api-keys/{key_id}` to revoke a stored key. Revocation is idempotent,
authentication reads `revoked_at` on every request, and no process restart is required.
The API rejects revocation of the key authenticating the current request and preserves
the final active administrator. Rotate by creating the replacement, storing it, signing
in with it, and then revoking the old key.

If every administrator bearer is unavailable during an incident, temporarily restore
the bootstrap administrator through the runtime secret configuration and use the API.
The following restricted database operation is break-glass only. It uses the same
lifecycle lock and final-administrator predicate as the repository, but bypasses the
API's current-session safeguard. Confirm the exact public key ID and an active
replacement before running it:

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

## Operations dashboard

Open `http://127.0.0.1:8080/dashboard/` after the API becomes ready. Connect with a
bootstrap or stored administrator bearer. The dashboard stores the bearer only in
the current browser tab's `sessionStorage`; closing the tab clears it, and signing
out clears it immediately. Do not use the dashboard from an untrusted browser or
expose the API over plaintext outside local development.

The project overview shows application credential/webhook coverage, catalog mappings,
customer access counts, recent normalized purchase observations, queue outcomes, and
webhook delivery metadata. It never renders protected provider payloads or secrets.
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
transaction: a transient Google failure leaves the entitlement durable and causes the
API request or worker job to retry. Google `409` concurrent updates, `429` rate limits,
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

Never remove an encryption key until all rows written with that key ID have been re-encrypted or expired.

## Queue incident response

- `iapstack_queue_depth` shows River's available, scheduled, retryable, running, completed, cancelled, and discarded states.
- `iapstack_queue_oldest_runnable_age_seconds` reports how long each queue's oldest runnable job has waited.
- `iapstack_queue_job_duration_seconds` reports bounded worker attempt count and duration sums by queue and outcome.
- River owns job claims, stale-job recovery, exponential retry scheduling, and terminal job cleanup.
- Retryable failures become discarded after `IAPSTACK_WORKER_MAX_ATTEMPTS`; permanent failures are cancelled immediately.
- Inbox, outbox, and reconciliation tables retain protected payloads and stable terminal audit outcomes while River job arguments contain identifiers only.
- `IAPSTACK_QUEUE_RETENTION` applies to both River metadata and durable queue audit rows. Pending or otherwise runnable records are never removed by retention cleanup.
- Durable records contain only safe error codes; correlate application logs with `message_id` and HTTP logs with `X-Request-ID`.

## Compose release gate

Run the same clean-volume release gate required by GitHub CI:

```sh
./deploy/e2e/run.sh
```

The harness creates an isolated Compose project and temporary TLS certificate, then removes its containers and volume on exit. It exercises the production `migrate`, `api`, and `worker` modes with PostgreSQL 17 and a separate TLS fixture service.

The gate verifies:

1. Migration ordering and API/worker readiness from an empty volume.
2. Stored admin and application key creation plus full Huawei application configuration.
3. Authoritative signed lifetime verification and idempotent duplicate submission.
4. HMAC-SHA-256 webhook headers and one logical delivery per entitlement version.
5. Duplicate signed Huawei notification persistence while the worker is stopped.
6. River recovery after worker restart, authoritative refund projection, and duplicate notification suppression.
7. API and worker Prometheus metric families.

Ports default to `18080` for API, `18081` for worker metrics, `18082` for fixture controls, and `15432` for PostgreSQL. Override `IAPSTACK_HTTP_PORT`, `IAPSTACK_E2E_WORKER_PORT`, `IAPSTACK_E2E_FIXTURE_PORT`, or `IAPSTACK_POSTGRES_PORT` when those ports are occupied.

On failure the harness prints Compose status and logs before cleanup. The fixture uses generated test-only RSA and TLS keys; it never contacts Huawei or an application-owned endpoint.

## Release publication

Release candidates and stable releases are created only through
`.github/workflows/release.yml`. Run the workflow manually from `main` after the
version-specific evidence pull request has merged and main CI has succeeded. Supply a
numbered candidate such as `v0.1.0-rc.1` or a stable version such as `v0.1.0`; do not
create the Git tag first.

Before any registry or release write, the workflow requires:

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
