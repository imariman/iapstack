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

- `iapstack_queue_depth` shows pending, processing, delivered/processed, and failed records.
- Stale processing locks are returned to pending automatically after twice the configured job timeout.
- River owns job claims, stale-job recovery, exponential retry scheduling, and terminal job cleanup.
- Retryable failures become discarded after `IAPSTACK_WORKER_MAX_ATTEMPTS`; permanent failures are cancelled immediately.
- Inbox, outbox, and reconciliation tables retain protected payloads and stable terminal audit outcomes while River job arguments contain identifiers only.
- Durable records contain only safe error codes; correlate application logs with `message_id` and HTTP logs with `X-Request-ID`.
