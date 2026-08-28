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

## Deploy

Compare account requirements, free-tier limitations, and complete-stack costs in the
[deployment target matrix](deploy/README.md) before creating provider resources.

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)

The Render Blueprint provisions the API, worker, private PostgreSQL database, generated
protection secrets, HTTPS ingress, and database migration command. It intentionally
uses paid minimum-size resources because a complete IAPStack runtime needs a persistent
background worker. Review the [Render deployment guide](deploy/render/README.md), the
provider's live price estimate, and the early-development warning before approval.

The Heroku manifest provides the same API, worker, managed PostgreSQL, release-phase
migration, and HTTPS shape on two always-on Basic dynos. Heroku has no permanently
free runtime; read the [Heroku deployment guide](deploy/heroku/README.md) and confirm
the live total before creating the app.

DigitalOcean App Platform is available through the complete [App Spec deployment
guide](deploy/digitalocean/README.md). Its official Deploy Button cannot express the
required worker and migration job, so the full topology uses one `doctl apps create`
command instead.

Fly.io process groups are configured in [`fly.toml`](fly.toml); the [Fly.io deployment
guide](deploy/fly/README.md) covers app creation, private Managed Postgres attachment,
secrets, migrations, and cleanup. Fly.io has only a short free trial and its managed
database makes it a comparatively expensive sandbox choice.

Koyeb's [bootstrap deployment](deploy/koyeb/README.md) creates its managed database,
secret-backed configuration, HTTPS API, and private worker through the official CLI.
Its free web and database allowances can support only a short supervised check; a
persistent worker is paid, and the complete always-on topology requires paid resources.

Coolify can deploy the complete stack from the repository-owned [Docker Compose
definition](deploy/coolify/README.md), including generated secrets, PostgreSQL,
migrations, HTTPS API routing, and a private worker. Self-hosted Coolify is free, but
you provide an always-on Linux server; Coolify Cloud and the deployment server are
separately billed.

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
- [HTTP API v1 and provider notification contracts](docs/api-v1.md)
- [Machine-readable OpenAPI v1 contract](contracts/openapi/v1.yaml)
- [Operations and dashboard guide](docs/operations.md)
- [v0.1 threat model and security release checklist](docs/security.md)
- [v0.1.0-rc.1 three-provider release gate](docs/releases/v0.1.0-rc.1.md)
- [Provider-neutral Flutter SDK](sdk/flutter/iapstack/README.md)
- [Huawei Flutter SDK and sandbox example](sdk/flutter/iapstack_huawei/README.md)
- [Google Play Flutter SDK and internal-testing example](sdk/flutter/iapstack_google_play/README.md)
- [Apple StoreKit 2 Flutter SDK and iOS testing example](sdk/flutter/iapstack_apple/README.md)

The server has production-shaped Huawei, Apple App Store, and Google Play server
slices. Apple includes StoreKit 2 signed-transaction verification,
Notifications V2 ingestion, authoritative renewal-status reconciliation, and a Flutter
companion with an iOS StoreKit testing harness. Google Play includes authenticated RTDN
ingestion, authoritative worker reconciliation, post-commit purchase acknowledgement,
and a Flutter Billing companion with an Android internal-testing harness. Apple,
Google Play, and Huawei must all pass their real-provider lifecycle gates before any
v0.1 release candidate or stable release is published. Google Play consumable
fulfillment remains outside v0.1.

## Development

IAPStack currently requires Go 1.25 or newer. Run the API locally with:

```sh
go run ./cmd/iapstack api
```

The API listens on `:8080` by default and exposes `GET /healthz` and `GET /readyz`.
The embedded operations dashboard is available at `http://localhost:8080/dashboard/`
and uses the same authenticated `/v1/admin` API as external administration clients.
Its access-key workspace supports secret-free inventory, one-time administrator key
creation, and guarded revocation for routine credential rotation.
Configuration is supplied through environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `IAPSTACK_HTTP_ADDRESS` | `:$PORT` or `:8080` | API listen address in `host:port` form; takes precedence over a platform-provided `PORT` |
| `IAPSTACK_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown deadline |
| `IAPSTACK_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `IAPSTACK_DATABASE_URL` | none | PostgreSQL connection string required by database-backed modes |
| `IAPSTACK_BOOTSTRAP_ADMIN_KEY` | none | Installation-only administrator bearer; when set it must contain at least 32 bytes |
| `IAPSTACK_AUTH_MAX_CONCURRENT_DERIVATIONS` | `4` | Fail-fast concurrency bound for memory-hard API-key creation and verification; maximum `32` |
| `IAPSTACK_PROTECTION_ACTIVE_KEY_ID` | none | Encryption key ID used for new protected values |
| `IAPSTACK_PROTECTION_KEY` | none | Base64-encoded or 64-character hexadecimal 32-byte encryption root key for an initial single-key deployment |
| `IAPSTACK_PROTECTION_KEYS` | none | JSON object mapping key IDs to base64-encoded 32-byte encryption root keys |
| `IAPSTACK_PROTECTION_FINGERPRINT_KEY` | none | Base64-encoded or 64-character hexadecimal 32-byte stable fingerprint root key |
| `IAPSTACK_WORKER_ID` | automatic | Optional River client identity; leave empty unless the deployment guarantees uniqueness |
| `IAPSTACK_WORKER_HTTP_ADDRESS` | `:8081` | Worker health, readiness, and metrics listen address |
| `IAPSTACK_WORKER_CONCURRENCY` | `4` | Maximum concurrent River jobs per queue and worker process |
| `IAPSTACK_WORKER_MAX_ATTEMPTS` | `12` | Attempts before River discards a retryable job |
| `IAPSTACK_QUEUE_RETENTION` | `720h` | Retention period for terminal River jobs and durable queue audit records |
| `IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS` | `false` | Explicitly permit Huawei provider endpoints on private, loopback, link-local, CGNAT, or benchmark addresses |
| `IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS` | `false` | Explicitly permit webhook delivery to private, loopback, link-local, CGNAT, or benchmark addresses |

Modes that handle provider evidence initialize the protection keyring before serving
work and fail fast when any protection variable is missing or malformed. Generate every
root key with a cryptographically secure secret generator such as
`openssl rand -base64 32`. Configure exactly one of `IAPSTACK_PROTECTION_KEY` or
`IAPSTACK_PROTECTION_KEYS`. The single-key form makes initial deployment compatible
with managed-platform secret generators; switch to the JSON keyring before encryption
key rotation. Keep the fingerprint key stable across ordinary encryption key rotations;
changing it alters idempotency fingerprints and requires an explicit data migration.
To rotate encryption, add the new key to `IAPSTACK_PROTECTION_KEYS`, select it with
`IAPSTACK_PROTECTION_ACTIVE_KEY_ID`, and retain old keys until all values written with
them have been re-encrypted or expired.

Webhook delivery and configurable Huawei provider calls resolve and validate every
A/AAAA destination at connection time, do not follow redirects, and permit public
network addresses only by default. Set `IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS=true`
or `IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS=true` only when the corresponding endpoint
must run on a trusted internal network, and pair either opt-in with an egress firewall
or an infrastructure allowlist.

Start a local PostgreSQL instance and apply every pending migration with:

```sh
docker compose -f deploy/compose.yaml up -d postgres
IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable' \
  go run ./cmd/iapstack migrate
```

Compose publishes PostgreSQL and the API on `127.0.0.1` by default. Set
`IAPSTACK_POSTGRES_BIND_ADDRESS` or `IAPSTACK_HTTP_BIND_ADDRESS` only when an
explicit network boundary requires a different host address; keep production
exposure behind authenticated TLS ingress and network policy.

Run the local quality checks with:

```sh
gofmt -w .
go vet ./...
go test -race -count=1 ./...
```

Run the Flutter SDK checks with:

```sh
(cd sdk/flutter/iapstack && dart pub get && dart analyze && dart test)
(cd sdk/flutter/iapstack_huawei && flutter pub get && flutter analyze && flutter test)
(cd sdk/flutter/iapstack_huawei/example && flutter pub get && flutter analyze && flutter test)
(cd sdk/flutter/iapstack_google_play && flutter pub get && flutter analyze && flutter test)
(cd sdk/flutter/iapstack_google_play/example && flutter pub get && flutter analyze && flutter test)
```

The provider-neutral package owns `/v1` transport, timeout/retry behavior,
models, and stable error mapping. The Huawei companion uses the official
`huawei_iap` plugin to preserve signed purchase data, paginate restore results,
and batch submissions. The Google Play companion uses Flutter's official
`in_app_purchase` implementation, binds checkout through an opaque account ID,
and leaves post-commit acknowledgement to IAPStack. Each companion includes a
manual Android provider-test harness.

PostgreSQL integration tests activate when `IAPSTACK_TEST_DATABASE_URL` is set. CI
runs them against a clean PostgreSQL service and verifies every migration can be
applied, rolled back, and reapplied.

Run the clean Compose release gate locally with:

```sh
./deploy/e2e/run.sh
```

It exercises PostgreSQL migration, API bootstrap, signed Huawei verification, River worker restart recovery, duplicate notification handling, entitlement projection, and signed webhook delivery. The provider-specific unit and integration suites cover the Apple and Google server paths; real-store gates remain mandatory for all three providers.

The automated fixture does not replace provider-managed lifecycle testing. Before a
`v0.1.0-rc.1` tag, complete the real-device Apple App Store, Google Play, and Huawei
gates in the [three-provider release runbook](docs/releases/v0.1.0-rc.1.md) and
validate their aggregate secret-free evidence with:

```sh
go run ./cmd/iapstack-release docs/releases/v0.1.0-rc.1-release-evidence.json
```

After an evidence-only pull request is merged and its main CI succeeds, dispatch the
`Release` workflow from `main`. It revalidates the evidence, candidate ancestry,
candidate and release-commit CI runs, and evidence-only diff before creating the Git
tag and GitHub release. The workflow publishes signed-provenance, SBOM-bearing
`linux/amd64` and `linux/arm64` images to `ghcr.io/imariman/iapstack`; release
candidates are GitHub prereleases and never update stable container aliases. Tags must
not be created manually.

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

The `worker` process handles the protected Huawei and Google Play notification inbox,
scheduled reconciliation, and signed application webhook outbox with bounded concurrency,
River-managed stale-job recovery, and exponential retry scheduling.

Operational deployment, backup, queue recovery, and upgrade procedures are documented
in [the operations guide](docs/operations.md). The authenticated v1 endpoints, provider
credential/evidence and notification contracts, and webhook verification rules are documented in
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
├── sdk/flutter/           # Provider-neutral, Huawei, and Google Play Flutter SDKs
├── contracts/             # Public API and webhook contracts
└── deploy/                # Docker and deployment templates
```

## Roadmap

- [x] Define the core domain, public API contracts, and persistence model
- [x] Implement the Huawei AppGallery adapter from official specifications
- [x] Add PostgreSQL migrations and a production-ready Docker setup
- [x] Build the initial provider-neutral, Huawei, and Google Play Flutter SDKs
- [x] Build the initial dashboard
- [x] Support lifecycle reconciliation and signed outbound webhooks
- [x] Publish a machine-readable v1 API contract with client compatibility checks
- [ ] Complete Apple App Store, Google Play, and Huawei lifecycle release gates and publish `v0.1.0-rc.1`
- [ ] Resolve candidate feedback and publish stable `v0.1.0`
- [ ] Add Amazon Appstore and additional store adapters
- [ ] Support customer migration and alias consolidation

The order above describes the initial implementation sequence, not a limitation of the architecture. IAPStack is intended to treat every store as a first-class adapter.

## License

Licensed under the [Apache License 2.0](LICENSE).
