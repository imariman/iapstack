<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStack Gopher mascot" width="360">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  Open-source, self-hosted in-app purchase verification and entitlement infrastructure.
  <br>
  Built in Go. Store-agnostic. Deployable anywhere.
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="Apache 2.0 License"></a>
  <img src="https://img.shields.io/badge/status-early%20development-orange.svg" alt="Early development">
  <img src="https://img.shields.io/badge/built%20with-Go-00ADD8.svg" alt="Built with Go">
</p>

> [!IMPORTANT]
> IAPStack is in early development and is not ready for production use yet.

IAPStack is a self-hosted control plane for validating in-app purchases and turning store transactions into durable application entitlements. It is designed for teams that want one backend-owned model across mobile stores without handing their purchase data or access rules to a hosted subscription platform.

## What IAPStack aims to provide

- Server-side verification for subscriptions, consumables, non-consumables, and lifetime purchases
- A store-neutral transaction and entitlement model
- Customer identity, aliases, restore flows, and migration support
- Store notification ingestion and lifecycle reconciliation
- Signed outbound webhooks for application backends
- An operational dashboard for customers, products, transactions, and entitlements
- First-party SDKs, beginning with Flutter
- Docker-first deployment backed by PostgreSQL

## Design principles

**Self-hosted by default.** You own the infrastructure, credentials, purchase data, and access decisions.

**Stores are adapters.** Store-specific APIs remain isolated behind shared contracts; the core domain does not depend on a single marketplace.

**Entitlements are the product boundary.** Applications ask what a customer can access instead of interpreting raw receipts or store states.

**Deploy anywhere.** The target is a straightforward container deployment rather than dependence on a particular cloud platform.

**Independent implementation.** Official store documentation defines behavior and security requirements. Other open-source projects may be studied for interoperability and edge cases, while IAPStack's code is implemented independently.

## Documentation

- [v0.1 scope and architecture](docs/v0.1-scope.md)
- [Architecture Decision Records](docs/adr/README.md)

## Development

IAPStack currently requires Go 1.24 or newer. Run the API locally with:

```sh
go run ./cmd/iapstack api
```

The API listens on `:8080` by default and exposes `GET /healthz` and `GET /readyz`.
Configuration is supplied through environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `IAPSTACK_HTTP_ADDRESS` | `:8080` | API listen address in `host:port` form |
| `IAPSTACK_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown deadline |
| `IAPSTACK_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `IAPSTACK_DATABASE_URL` | none | PostgreSQL connection string required by database-backed modes |
| `IAPSTACK_BOOTSTRAP_ADMIN_KEY` | none | Installation-only administrator bearer used to create stored API keys |
| `IAPSTACK_PROTECTION_ACTIVE_KEY_ID` | none | Encryption key ID used for new protected values |
| `IAPSTACK_PROTECTION_KEYS` | none | JSON object mapping key IDs to base64-encoded 32-byte encryption root keys |
| `IAPSTACK_PROTECTION_FINGERPRINT_KEY` | none | Base64-encoded 32-byte stable fingerprint root key |
| `IAPSTACK_WORKER_ID` | `worker-1` | Unique durable queue worker identity |
| `IAPSTACK_WORKER_HTTP_ADDRESS` | `:8081` | Worker health, readiness, and metrics listen address |
| `IAPSTACK_WORKER_CONCURRENCY` | `4` | Maximum concurrent job attempts |
| `IAPSTACK_WORKER_BATCH_SIZE` | `16` | Maximum records claimed per queue poll |
| `IAPSTACK_WORKER_MAX_ATTEMPTS` | `12` | Attempts before a record enters terminal failed state |
| `IAPSTACK_WORKER_MAINTENANCE_INTERVAL` | `1m` | Queue depth sampling and retention cleanup interval |
| `IAPSTACK_QUEUE_RETENTION` | `720h` | Retention period for delivered, processed, and failed queue records |
| `IAPSTACK_QUEUE_PRUNE_BATCH_SIZE` | `1000` | Maximum terminal records deleted per queue and maintenance cycle |

Modes that handle provider evidence initialize the protection keyring before serving
work and fail fast when any protection variable is missing or malformed. Generate every
root key with a cryptographically secure secret generator such as
`openssl rand -base64 32`. Keep the fingerprint key stable across ordinary encryption
key rotations; changing it alters idempotency fingerprints and requires an explicit
data migration. To rotate encryption, add the new key to `IAPSTACK_PROTECTION_KEYS`,
select it with `IAPSTACK_PROTECTION_ACTIVE_KEY_ID`, and retain old keys until all values
written with them have been re-encrypted or expired.

Start a local PostgreSQL instance and apply every pending migration with:

```sh
docker compose -f deploy/compose.yaml up -d postgres
IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable' \
  go run ./cmd/iapstack migrate
```

Run the local quality checks with:

```sh
gofmt -w .
go vet ./...
go test -race -count=1 ./...
```

PostgreSQL integration tests activate when `IAPSTACK_TEST_DATABASE_URL` is set. CI
runs them against a clean PostgreSQL service and verifies every migration can be
applied, rolled back, and reapplied.

Purchase verification is coordinated by the provider-neutral `internal/verification`
use case. Provider network calls and protection of sensitive evidence happen before
the durable transaction. Catalog validation, evidence and observation persistence,
entitlement projection, and outbox insertion then commit or roll back together. The
use case depends only on adapter, protector, clock, and persistence ports; concrete
providers and PostgreSQL remain replaceable implementations.

Sensitive data crosses the private-plaintext `internal/protection` port. The concrete
platform keyring uses versioned AES-256-GCM envelopes with secure random nonces,
scope-bound authenticated data, HKDF-derived encryption subkeys, and a separate
project/application/purpose-scoped HMAC-SHA-256 fingerprint. Persistence receives only
ciphertext, fingerprint, and `key_id`; it never receives plaintext.

Provider adapters obtain application credentials through the opaque, versioned
`stores.CredentialSource` port. `internal/credentials` validates the authoritative
application scope and opens protected PostgreSQL values only for the requesting
provider configuration. Shared configuration never acquires Huawei-, Apple-, or
Google-shaped credential fields. Credential payload rotation uses optimistic durable
revisions; encryption-key rotation remains independent and preserves fingerprints.

Go source files place constants first, type and struct declarations second, and
executable code last. Related constants stay together; unrelated constant groups are
separated by a blank line. Every constant, function, and method has an English
explanatory comment.

The `worker` process handles the protected Huawei notification inbox, scheduled
reconciliation, and signed application webhook outbox with bounded concurrency,
stale-lock recovery, and exponential full-jitter retries.

Operational deployment, backup, queue recovery, and upgrade procedures are documented
in [the operations guide](docs/operations.md). The authenticated v1 endpoints, Huawei
credential/evidence contracts, and webhook verification rules are documented in
[the HTTP API guide](docs/api-v1.md).

## Planned architecture

```text
.
├── cmd/iapstack/          # API and worker entrypoints
├── internal/
│   ├── core/              # Customers, transactions, products, and entitlements
│   ├── credentials/       # Protected application credential orchestration
│   ├── stores/            # Apple, Google, Huawei, Amazon, and future adapters
│   ├── persistence/       # Durable ports and PostgreSQL repositories
│   ├── protection/        # Provider-neutral sensitive-data protection port
│   ├── platform/          # Concrete configuration, logging, and protection implementations
│   └── verification/      # Provider-neutral purchase verification orchestration
├── dashboard/             # Self-hosted web dashboard
├── sdks/                  # Flutter and future client SDKs
├── contracts/             # Public API and webhook contracts
└── deploy/                # Docker and deployment templates
```

## Roadmap

- [x] Define the core domain, public API contracts, and persistence model
- [x] Implement the Huawei AppGallery adapter from official specifications
- [x] Add PostgreSQL migrations and a production-ready Docker setup
- [ ] Build the initial dashboard and Flutter SDK
- [ ] Add Apple App Store and Google Play adapters
- [ ] Add Amazon Appstore and additional store adapters
- [ ] Support customer migration, reconciliation, and signed outbound webhooks
- [ ] Publish the first stable release and production hardening guide

The order above describes the initial implementation sequence, not a limitation of the architecture. IAPStack is intended to treat every store as a first-class adapter.

## License

Licensed under the [Apache License 2.0](LICENSE).
