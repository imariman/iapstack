# IAPStack Operations Guide

## Required secrets

Generate independent values before starting the stack. Do not commit the resulting `.env` file.

```sh
export IAPSTACK_POSTGRES_PASSWORD="$(openssl rand -base64 32)"
export IAPSTACK_PROTECTION_ACTIVE_KEY_ID="key-2026-01"
export IAPSTACK_PROTECTION_KEYS="{\"key-2026-01\":\"$(openssl rand -base64 32)\"}"
export IAPSTACK_PROTECTION_FINGERPRINT_KEY="$(openssl rand -base64 32)"
export IAPSTACK_BOOTSTRAP_ADMIN_KEY="$(openssl rand -base64 48)"
```

The fingerprint key must remain stable across encryption-key rotation. The bootstrap admin key is an installation credential: use it to create stored admin/application keys through `POST /v1/admin/api-keys`, then rotate it in the deployment secret store.

## Start and inspect

```sh
docker compose -f deploy/compose.yaml up --build -d
docker compose -f deploy/compose.yaml ps
curl --fail http://127.0.0.1:8080/readyz
curl --fail http://127.0.0.1:8080/metrics
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
The quick-start dialog creates a Huawei project, application, entitlement, product,
and provider-product mapping. Configure the provider credential, webhook endpoint,
stored API keys, and initial customers through the documented admin API before using
the application in production.

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
- River owns job claims, stale-job recovery, exponential retry scheduling, and terminal job cleanup.
- Retryable failures become discarded after `IAPSTACK_WORKER_MAX_ATTEMPTS`; permanent failures are cancelled immediately.
- Inbox, outbox, and reconciliation tables retain protected payloads and stable terminal audit outcomes while River job arguments contain identifiers only.
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
