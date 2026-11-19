<p align="center">
  <img src="assets/iapstack-gopher.png" alt="IAPStack Gopher mascot" width="150">
</p>

<h1 align="center">IAPStack</h1>

<p align="center">
  <strong>Self-hosted in-app purchase verification and entitlements.</strong>
  <br>
  One backend-owned model for Apple App Store, Google Play, and Huawei AppGallery.
</p>

<p align="center">
  <a href="https://imariman.github.io/iapstack/">Website</a>
  ·
  <a href="#quick-start">Quick start</a>
  ·
  <a href="#deploy">Deploy</a>
  ·
  <a href="#documentation">Documentation</a>
  ·
  <a href="contracts/openapi/v1.yaml">OpenAPI</a>
</p>

<p align="center">
  <a href="https://github.com/imariman/iapstack/actions/workflows/ci.yml"><img src="https://github.com/imariman/iapstack/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
  <a href="https://github.com/imariman/iapstack/actions/workflows/codeql.yml"><img src="https://github.com/imariman/iapstack/actions/workflows/codeql.yml/badge.svg" alt="CodeQL status"></a>
  <a href="https://github.com/imariman/iapstack/releases"><img src="https://img.shields.io/github/v/release/imariman/iapstack?include_prereleases&amp;sort=semver&amp;color=orange" alt="Latest prerelease"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="Apache 2.0 License"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/imariman/iapstack" alt="Go version from go.mod"></a>
</p>

<p align="center">
  <sub>
    <strong>English</strong>
    · <a href="README.de.md">Deutsch</a>
    · <a href="README.es.md">Español</a>
    · <a href="README.fr.md">Français</a>
    · <a href="README.it.md">Italiano</a>
    · <a href="README.pt-BR.md">Português (Brasil)</a>
    · <a href="README.tr.md">Türkçe</a>
    · <a href="README.ru.md">Русский</a>
    · <a href="README.ja.md">日本語</a>
    · <a href="README.ko.md">한국어</a>
    · <a href="README.zh-CN.md">简体中文</a>
  </sub>
</p>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/dashboard-dark.png">
    <img src="assets/dashboard-light.png" alt="IAPStack operations dashboard: purchase route health, verification activity with a hovered day, recognized store revenue, and sandbox test value for a demo project" width="100%">
  </picture>
</p>

IAPStack is a backend and control panel that verifies in-app purchases on the server
and turns store transactions into durable entitlements. Your apps ask one question,
*what can this customer access?*, and the answer is the same whichever store the
purchase came from. Credentials, purchase data, and access decisions stay on
infrastructure you run.

| Verify once, everywhere | Entitlements, not receipts | Yours to run |
| --- | --- | --- |
| Server-side verification for three stores behind one store-neutral model. Store APIs stay isolated in adapters. | Apps read access state instead of interpreting raw receipts and store-specific statuses. | A single Go binary and PostgreSQL. Runs in Docker on any cloud or on a VM you already have. |

> [!IMPORTANT]
> IAPStack is prerelease software and not yet supported for production. The
> `v0.1.0-rc1` and `v0.1.0-rc2` releases are legacy previews; the first supported
> candidate ships once the real-store lifecycle gates pass for all three providers.

## Features

- **Verification** of subscriptions and non-consumable lifetime purchases from Apple
  App Store, Google Play, and Huawei AppGallery
- **Lifecycle tracking** through durable store notification ingestion and scheduled
  reconciliation
- **Signed outbound webhooks** so your backend learns about every entitlement change
- **Operations dashboard** for applications, customers, queues, and webhook
  deliveries, in light and dark themes
- **Versioned HTTP API** with a machine-readable [OpenAPI contract](contracts/openapi/v1.yaml)
- **Client SDKs** for Flutter, iOS, Android, and React Native, plus Go and TypeScript
  SDKs for your own backend
- **Flexible topology**: one compact process, or separate API and worker services

Consumable fulfillment, hosted operation, and stores beyond these three are outside
the v0.1 scope. Customer migration, RevenueCat imports, offerings, and Apple financial
reports already exist on `main` and are previewed in
[`v0.2.0-alpha.1`](https://github.com/imariman/iapstack/releases/tag/v0.2.0-alpha.1),
but they are not part of the v0.1 release gate. See the [v0.1 scope](docs/v0.1-scope.md)
for the full boundary.

## Quick start

You need Docker with Compose and OpenSSL. From a clone of this repository, generate
local secrets and start the stack:

```sh
export IAPSTACK_POSTGRES_PASSWORD="$(openssl rand -base64 32)"
export IAPSTACK_PROTECTION_ACTIVE_KEY_ID="local-1"
export IAPSTACK_PROTECTION_KEYS="{\"local-1\":\"$(openssl rand -base64 32)\"}"
export IAPSTACK_PROTECTION_FINGERPRINT_KEY="$(openssl rand -base64 32)"
export IAPSTACK_BOOTSTRAP_ADMIN_KEY="$(openssl rand -base64 48)"
export IAPSTACK_METRICS_BEARER_TOKEN="$(openssl rand -base64 48)"
docker compose -f deploy/compose.yaml up --build -d --wait
curl --fail http://127.0.0.1:8080/readyz
```

Keep these values: the Compose volume depends on them, and generating new ones does
not rotate the existing database password or protection keys.

Open `http://127.0.0.1:8080/dashboard/` and sign in with the bootstrap administrator
key. Create a durable administrator key, then remove `IAPSTACK_BOOTSTRAP_ADMIN_KEY`
and recreate the API container. The [operations guide](docs/operations.md) covers the
rest: key rotation, backups, upgrades, and the production security baseline.

## Deploy

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/imariman/iapstack)
[![Deploy to Heroku](https://www.herokucdn.com/deploy/button.svg)](https://www.heroku.com/deploy?template=https://github.com/imariman/iapstack)
[![Deploy to Oracle Cloud](https://oci-resourcemanager-plugin.plugins.oci.oraclecloud.com/latest/deploy-to-oracle-cloud.svg)](https://cloud.oracle.com/resourcemanager/stacks/create?zipUrl=https://imariman.github.io/iapstack/deploy/iapstack-oracle-stack.zip)
[![Open in Cloud Shell](https://www.gstatic.com/cloudssh/images/open-btn.svg)](https://shell.cloud.google.com/cloudshell/editor?cloudshell_git_repo=https://github.com/imariman/iapstack&cloudshell_git_branch=main&cloudshell_tutorial=deploy/gcp/tutorial.md&show=terminal)

Every target runs the same container image with PostgreSQL, a migration step, the API,
and a worker that must stay running. Compare account requirements, free-tier limits,
and full-stack costs in the [deployment target matrix](deploy/README.md) before
creating resources.

| Target | Shape | Guide |
| --- | --- | --- |
| **Single VM** (Oracle, Google Cloud, Hetzner, DigitalOcean, your own server) | Cheapest always-on option: the full stack on one Linux machine behind a Cloudflare Tunnel, with no open inbound ports. From USD 0 per month on Oracle Cloud Always Free. | [Single-VM guide](deploy/vps/README.md) |
| Render | Free compact service and disposable PostgreSQL for evaluation; paid compact or split Blueprints | [Render](deploy/render/README.md) |
| Railway | PostgreSQL and one API/worker service; usually within the USD 5 Hobby plan | [Railway](deploy/railway/README.md) |
| Heroku | API, worker, and managed PostgreSQL on two Basic dynos; no free tier | [Heroku](deploy/heroku/README.md) |
| DigitalOcean App Platform | API, worker, and migration job from an App Spec | [DigitalOcean](deploy/digitalocean/README.md) |
| Fly.io | Process groups with private Managed Postgres | [Fly.io](deploy/fly/README.md) |
| Koyeb | Managed database, HTTPS API, and private worker via the CLI | [Koyeb](deploy/koyeb/README.md) |
| Coolify | Full stack from the repository's Compose definition on your server | [Coolify](deploy/coolify/README.md) |
| Kubernetes | Plain manifests for the API, worker, and migration job; bring your own PostgreSQL | [Kubernetes](deploy/k8s/README.md) |

Scale-to-zero and serverless platforms are a poor fit: the worker ingests store
notifications, delivers webhooks, and runs reconciliation every minute.

## SDKs

Mobile apps talk to IAPStack with short-lived customer sessions. The durable
application key stays on your backend and must never ship in a mobile binary or
browser bundle.

| Platform | Package | Status |
| --- | --- | --- |
| Flutter | [Provider-neutral client](sdk/flutter/iapstack/README.md) with [Apple](sdk/flutter/iapstack_apple/README.md), [Google Play](sdk/flutter/iapstack_google_play/README.md), and [Huawei](sdk/flutter/iapstack_huawei/README.md) companions | v0.1 release scope |
| iOS (Swift) | [HTTP client and StoreKit 2 companion](sdk/ios/README.md) | Implemented, not yet published |
| Android (Kotlin) | [HTTP client with Play Billing and HMS adapters](sdk/android/README.md) | Implemented, not yet published |
| React Native | [HTTP client and native StoreKit/Play/Huawei bridge](sdk/react-native/README.md) | Implemented, not yet published |
| Go (backend) | [Customer sessions, entitlement lookup, webhook verification](sdk/go/README.md) | Implemented, not yet published |
| TypeScript (backend) | [Customer sessions, entitlement lookup, webhook verification](sdk/typescript/README.md) | Implemented, not yet published |

Registry publication is tracked in [#141](https://github.com/imariman/iapstack/issues/141).
The [SDK release guide](docs/sdk-releases.md) covers installation from source,
versioning, and release automation.

## Documentation

| Guide | What it covers |
| --- | --- |
| [Operations](docs/operations.md) | Secrets, configuration, dashboard, store notification setup, backups, upgrades, releases |
| [HTTP API v1](docs/api-v1.md) | Endpoints, provider credentials, notifications, webhook verification |
| [v0.1 scope and architecture](docs/v0.1-scope.md) | System context, core invariants, provider slices |
| [Security](docs/security.md) | Threat model and release security checklist |
| [Architecture decisions](docs/adr/README.md) | Why the main boundaries exist |
| [Deployment targets](deploy/README.md) | Provider comparison and per-provider guides |
| [Release runbook](docs/releases/v0.1.0-rc.1.md) | The three-provider real-store release gate |

## Development

Use the Go toolchain declared in `go.mod`. Start PostgreSQL, apply migrations, and run
the API and worker in one process:

```sh
docker compose -f deploy/compose.yaml up -d postgres
export IAPSTACK_DATABASE_URL='postgres://iapstack:iapstack@localhost:5432/iapstack?sslmode=disable'
go run ./cmd/iapstack migrate
go run ./cmd/iapstack server
```

The server also needs the protection keys from the [quick start](#quick-start); every
setting is listed in the [configuration reference](docs/operations.md#configuration-reference).
The API listens on `:8080`, serves `/healthz` and `/readyz`, and embeds the dashboard
at `/dashboard/`.

Run the server checks and the end-to-end release gate:

```sh
go vet ./... && go test -race -count=1 ./...
./deploy/e2e/run.sh
```

The [contributing guide](CONTRIBUTING.md) has the SDK checks, integration test setup,
an architecture overview, and code conventions.

### Repository layout

```text
.
├── cmd/iapstack/       # API, worker, and migration entrypoints
├── internal/
│   ├── core/           # Customers, transactions, products, and entitlements
│   ├── verification/   # Provider-neutral purchase verification
│   ├── stores/         # Apple, Google Play, and Huawei adapters
│   ├── credentials/    # Protected application credentials
│   ├── protection/     # Sensitive-data protection port
│   ├── persistence/    # PostgreSQL repositories
│   └── platform/       # Configuration, logging, and keyring implementations
├── dashboard/          # Embedded operations dashboard
├── contracts/          # OpenAPI and webhook contracts
├── sdk/                # Flutter, iOS, Android, React Native, Go, and TypeScript SDKs
├── deploy/             # Compose, cloud, VM, and Kubernetes definitions
└── docs/               # Guides, ADRs, and release runbooks
```

## Roadmap

- [x] Store-neutral domain, PostgreSQL persistence, and versioned OpenAPI contract
- [x] Apple App Store, Google Play, and Huawei AppGallery adapters
- [x] Lifecycle reconciliation and signed outbound webhooks
- [x] Operations dashboard and deployment definitions for cloud, VM, and Kubernetes
- [x] Flutter, Swift, Kotlin, React Native, Go, and TypeScript SDKs
- [x] Customer migration, RevenueCat imports, offerings, and Apple financial reports (`v0.2.0-alpha.1`)
- [ ] Pass the three-provider real-store gates and publish `v0.1.0-rc.1`
- [ ] Publish stable `v0.1.0`
- [ ] Publish versioned SDK packages ([#141](https://github.com/imariman/iapstack/issues/141))
- [ ] Google Play and Huawei financial report recognition
- [ ] Amazon Appstore and additional store adapters

Checked items are implemented in the repository; they do not imply production
readiness.

## Contributing

Bug reports, fixes, and store expertise are welcome. Read the
[contributing guide](CONTRIBUTING.md) before opening a pull request, the
[support guide](SUPPORT.md) for questions, and the [security policy](SECURITY.md) to
report a vulnerability. Participation is governed by the
[Code of Conduct](CODE_OF_CONDUCT.md).

## License

[Apache License 2.0](LICENSE)
