# ADR-0003: Use PostgreSQL as the source of truth

- Status: Accepted
- Date: 2026-08-23

## Context

Purchase submission, store notifications, reconciliation, and webhook delivery are retryable operations. External stores provide at-least-once behavior in important paths, and network failures can make the outcome of a request temporarily uncertain.

IAPStack must never grant twice, lose a recorded lifecycle event, or publish a state change that was not committed.

## Decision

PostgreSQL will be the authoritative store for all v0.1 state. Purchase evidence is append-oriented; the current entitlement is a reproducible projection of verified evidence.

IAPStack will use:

- Unique idempotency identities scoped by store and application
- A durable inbox for notifications and retryable commands
- A transactional outbox for application webhooks
- Row-level locking or compare-and-set versioning when entitlement projections change
- Database transactions that commit evidence, projections, and outbox records together

Workers may process an inbox or outbox item more than once. Handlers must therefore be deterministic and idempotent. Delivery is at least once; business effects are effectively once.

No external message broker is required for v0.1. PostgreSQL queue tables are sufficient until measured throughput or isolation requirements justify another component.

## Consequences

- Backup, restore, and migration quality directly affect purchase correctness.
- Operators need only one stateful dependency for the first release.
- Queue polling must use bounded batches and safe concurrent claiming.
- Historical evidence enables audit and projection repair.
- Very high event throughput may eventually require partitioning or a dedicated broker, but that migration can preserve the inbox/outbox contracts.

## Alternatives considered

### Update only a mutable entitlement row

Rejected because it loses the evidence needed to explain, repair, or audit access decisions.

### Require Kafka or another broker

Rejected for v0.1 because it materially increases self-hosting cost without a demonstrated throughput requirement.

### Trust exactly-once delivery from stores or webhooks

Rejected because distributed network calls cannot provide that guarantee end to end.
