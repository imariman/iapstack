# Paywall, hosted operation, and Kubernetes

- Status: Design plan only. Unnumbered post-v0.1 work.
- Date: 2026-10-26

This document is three later plans. They are not one release, they do not share a milestone, and none of them is a prerequisite for the `v0.1.0-rc.1` three-provider release gate in [`docs/v0.1-scope.md`](../v0.1-scope.md) and [`docs/releases/v0.1.0-rc.1.md`](../releases/v0.1.0-rc.1.md).

Self-hosting is the product. [`README.md`](../../README.md) states that the operator owns the infrastructure, credentials, purchase data, and access decisions. A plan that turns the current server into a multi-tenant RevenueCat clone is rejected.

Verified facts are marked as such. Choices that the code does not already settle are marked as open questions or as assumptions.

## Recommendation

**Remote paywall stays in this repository, later, as a presentation document.** Entitlement authority already exists: verification and projection. An offering only tells a client which configured store product IDs to show. [`docs/adr/0004-api-first-monorepo.md`](../adr/0004-api-first-monorepo.md) keeps public contracts, SDKs, and deployment assets together until a contract is stable. A separate paywall product would invent a second source of catalog truth. A visual editor, an experiment platform, and price management do not belong here. The dashboard remains an operations console, as specified in [`design-system/iapstack/MASTER.md`](../../design-system/iapstack/MASTER.md) and [`design-system/iapstack/pages/dashboard.md`](../../design-system/iapstack/pages/dashboard.md).

**Hosted SaaS does not get built inside the current server.** It is a different product. The project and application columns are an authorization scope for one operator. They are not a boundary between customers of a vendor. The seam, if a hosted product is ever operated, is the existing HTTP API in front of a separate control plane. The self-hosted process must not grow signup, operator billing, residency, or a vendor staff role.

**Kubernetes packaging stays in this repository, later, as plain manifests for the container that already ships.** [`docs/adr/0001-go-modular-monolith.md`](../adr/0001-go-modular-monolith.md) already allows the API and the worker to run as separate processes against one PostgreSQL database. There is no `deploy/k8s` directory, no `Chart.yaml`, and no Kubernetes manifest in the repository today. [`deploy/README.md`](../../deploy/README.md) already refuses a deceptively small enterprise module for AWS, Google Cloud, or Azure. A Helm chart is optional packaging of the same objects, not a second architecture and not the first artifact. A Kubernetes operator, active-active deployment, and regional failover stay out, matching the v0.1 non-goals.

## 1. Remote paywall

### Decision

Build this later in this repository. Do not build it for `v0.1.0-rc.1`.

The paywall is presentation. It does not grant, revoke, or explain access. Access continues to come from the verification transaction and the entitlement projection described in [`docs/v0.1-scope.md`](../v0.1-scope.md).

### Verified facts

- Remote paywall authoring, pricing experiments, and A/B testing are explicit v0.1 non-goals.
- The catalog schema in `internal/persistence/postgres/migrations/001_catalog.sql` stores projects, applications, customers, products, entitlement keys, product-to-entitlement links, and store product IDs. It does not store display copy, offering groups, or a price list.
- Provider-signed price metadata exists on purchase observations (`internal/core/purchase.go`). That is evidence from a store transaction, not a price the server is allowed to invent or change.
- The v1 contract in `contracts/openapi/v1.yaml` exposes catalog writes only under `/v1/admin/...`. There is no public offering route. Customer sessions are minted at `POST /v1/applications/{application_id}/customer-sessions`. Entitlement reads stay on the existing customer route.
- The provider-neutral Flutter client in `sdk/flutter/iapstack` submits purchases, restores, and reads entitlements. Store companions load localized product details and prices from the store. The Google Play companion in `sdk/flutter/iapstack_google_play` is one example.
- [`README.md`](../../README.md) forbids shipping the durable application bearer in a mobile binary. Mobile clients use short-lived customer sessions. Those sessions expire after 15 minutes (`internal/auth/customer_session.go`).
- The dashboard is an embedded same-origin operations UI (`dashboard/handler.go`). It calls the public admin API and does not read PostgreSQL.

### Design

Add one versioned JSON document per application. The document names offerings and the store product IDs inside them. It may carry stable display keys for the client to resolve. It does not carry prices, currencies, credentials, customer identifiers, or entitlement state.

Proposed document shape, to be added under `contracts/` only when this work starts:

```json
{
  "schema_version": 1,
  "application_id": "application-id",
  "revision": "revision-token",
  "offerings": [
    {
      "id": "default",
      "packages": [
        {
          "id": "monthly",
          "store_product_id": "premium_monthly",
          "display_key": "premium_monthly"
        }
      ]
    }
  ]
}
```

`store_product_id` must already exist on `store_products` for that application. The offering loader rejects unknown IDs. It does not create catalog rows. Verification, the projector, the inbox, and the outbox do not read the document.

**Client contract.** A later Flutter client fetches `GET /v1/applications/{application_id}/offering`. The response is cacheable: `ETag` is the revision, and `Cache-Control` is public with a bounded max-age. The client persists the last successful body. When the server is unreachable, or the route is absent, the client uses that cache, then a document bundled with the app, then its existing hardcoded product IDs. Failure to fetch an offering must not change a purchase submission or an entitlement read. Native SDKs can consume the same document after the contract exists. Their current presence in `sdk/` does not pull this work into the v0.1 gate.

The GET is unauthenticated. Application identifiers are not secrets; [`docs/security.md`](../security.md) T02 already treats identifier disclosure as expected. The durable application bearer stays on the trusted host. The body is identical for every caller. Query parameters must not select a customer.

**Authoring.** Start with a file, one JSON document per application, loaded by the API from an explicit configuration path. Missing configuration means the route returns a stable not-found response and clients use their fallback. Do not add a visual editor. Do not add offering screens to the dashboard.

An admin write of the same JSON is the second step, and only after the document shape has been used from a file. That write is an admin API, not a dashboard feature. If it needs persistence, use a new application-scoped table that no verification transaction touches. Do not store the document on `products`, `customer_entitlements`, or the outbox.

**Customer-specific eligibility.** Do not build it in the first slice. If a later slice needs it, add a separate route that accepts the existing customer session and returns only offering or package IDs. That response is not cacheable as a public document, contains no credentials, and is not an entitlement grant. The client still asks the existing entitlement route what the customer can access, and still asks the store what the product costs.

**Bounds.** Reject an authoring document larger than 64 KiB. That is a new limit for this document, deliberately under the existing 1 MiB HTTP body limit in `internal/platform/config/config.go`. The public response must not echo request headers or customer state.

### Non-goals

- An A/B or feature-flag platform.
- Targeting, segmentation, or a model that chooses offers.
- Changing store prices, base plans, or introductory offers. The store remains the price authority. The signed observation price remains evidence, not a catalog.
- Consumable grants or any other write into the entitlement projection.
- A paywall editor in `dashboard/`.
- Blocking purchase or restore on offering availability.

### Open questions

- Localized sentences stay in the client. The document carries only `display_key`. Moving translated copy into the document would still be presentation data, not billing data.
- The public max-age of 300 seconds is a starting bound, not a measured cache policy.
- Do not add an offering-fetch metric in the first slice. Do not add customer, provider, or product identifiers as metric labels. [`docs/security.md`](../security.md) T09 already forbids that class of label.

## 2. Hosted SaaS

### Decision

Do not build hosted operation inside the current server. A vendor-operated service would be a separate product that speaks the existing API. This repository keeps one self-hosted threat model.

### Verified facts

The current row scope is real and intentional. Catalog and purchase tables carry `project_id`, and application-owned tables also carry `application_id`, with foreign keys back to `applications (id, project_id)`. Repositories filter on those columns. [`docs/security.md`](../security.md) T02 requires principals to carry project and application scope.

That scope is not tenant isolation. The following are verified:

- No migration enables row-level security or creates a database role per project. Runtime connections use one `IAPSTACK_DATABASE_URL`.
- `api_keys` allows `role = 'admin'` only when `project_id` and `application_id` are null (`internal/persistence/postgres/migrations/006_operations.sql`). Administrator sessions are installation-wide. `internal/auth/admin_session.go` binds them to a fixed protection scope, not to a tenant.
- Encryption roots are process environment variables. [`docs/adr/0006-scoped-authenticated-data-protection.md`](../adr/0006-scoped-authenticated-data-protection.md) puts project, application, and purpose into authenticated data, so ciphertext cannot be moved across applications. The same roots still open every project in the process.
- `applications_store_scope_unique` is `(provider, environment, provider_application_id)` with no project column. Two projects in one database cannot register the same store application. A unique violation also discloses that the store application is already present.
- Primary keys for projects, applications, customers, products, and entitlements are global text identifiers. `customers.external_id` is unique per project, which is the right rule for one operator and still shares one identifier space at the primary key.
- [`docs/security.md`](../security.md) places a malicious host administrator outside the application boundary. The operator is expected to provide host hardening, secret management, and backup security. A vendor who holds the host, the roots, and the backups is inside the customer's trust decision. The document does not describe a staff-access or residency boundary.

[`docs/adr/0007-application-scoped-provider-credentials.md`](../adr/0007-application-scoped-provider-credentials.md) is the right credential model for several applications owned by one operator. It is not a substitute for separate trust domains.

### What a hosted product would have to add

These are properties of a different system. They are listed so they are not quietly folded into `internal/`:

- Isolation beyond the current project and application model: separate data stores, separate protection roots, and separate administrator bootstraps.
- Billing of the operators themselves: accounts, plans, invoices, and taxes for the team running IAPStack. That is not store-purchase evidence and must not join the entitlement schema.
- A public signup path that can create a deployment without receiving a shell on the database host.
- Regional data residency, including where purchase evidence and backups live, and how a tenant moves.
- A defined incident path for vendor staff who can see customer purchase data, including who may decrypt, how that access is audited, and how it ends.
- A SOC-style boundary between vendor personnel, tenant administrators, and application customers. The current admin role is on the wrong side of that line because it is installation-wide.

### Database isolation

Current `(project_id, application_id)` row scope is enough for one self-hosted operator who already holds the roots, the database role, and the admin key. It is not enough for a hosted beta that stores another company's purchases.

A shared-database beta would share the encryption roots, the installation administrator, the database role, backups, logs, and the River queues. There is no row-level security backstop if a query omits a predicate. Patching that with a `tenant_id` column, policies, and a vendor superadmin would rewrite the isolation model: global primary keys, the admin-key constraint, process-wide roots, and the global store-application unique constraint would all move.

The minimum bar before any such beta is a separate database per tenant, with its own protection roots and its own bootstrap administrator. Even that arrangement is a control plane outside this server. The binary and the schema stay as they are for a single operator.

### Seam

The public contract remains `contracts/openapi/v1.yaml` and the webhook schemas. A future control plane may provision a normal IAPStack deployment, or a dedicated database plus the same binary, and route that tenant to it. Provisioning, signup, metering, invoicing, and support access live outside this process.

The self-hosted API does not gain tenant, plan, invoice, or region concepts. Application code keeps resolving a project and an application. It does not resolve a billing account.

### What must never happen

Hosted mode must not weaken the self-hosted threat model in [`docs/security.md`](../security.md). In particular it must not:

- change the default of `IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS` or `IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS`;
- reuse one protection root or one fingerprint root across tenants;
- add a vendor staff credential to the self-hosted `admin` role;
- teach entitlement tables about operator billing;
- treat a shared database as equivalent to the current project scope in the security document.

Self-hosted deployments continue to follow [`docs/security.md`](../security.md) unchanged. A hosted product, if it exists, writes its own threat model elsewhere.

### Open questions

- Whether anyone should sell a hosted service is a product decision. The technical seam above does not depend on that answer.
- Residency regions and staff-access procedure are control-plane questions. They are not reasons to add columns to the entitlement projection.

## 3. Kubernetes

### Decision

Add plain manifests later under `deploy/k8s` for one single-replica installation. Do not add a controller, a custom resource, active-active replication, or multi-region failover. A thin Helm chart may render those same objects later. It is not required, and it must not be an operator.

This packaging does not block `v0.1.0-rc.1`. Compose, Fly, and the other existing targets remain the deployment path until these manifests exist and are exercised.

### Verified facts

The shippable unit is the image built from `Dockerfile`. The entrypoint is `/usr/local/bin/iapstack`, running as UID/GID 65532. `cmd/iapstack` delegates to `internal/app`, which accepts exactly four modes:

| Mode | Process |
| --- | --- |
| `migrate` | Applies the embedded schema migrations, then River migrations (`postgres.Migrate` calls `MigrateRiver`). It does not serve traffic. |
| `api` | HTTP API, embedded dashboard, probes, and metrics on `IAPSTACK_HTTP_ADDRESS` (default `:8080`). |
| `worker` | River worker plus probes and metrics on `IAPSTACK_WORKER_HTTP_ADDRESS` (default `:8081`). |
| `server` | Compact mode. API and worker in one process. `IAPSTACK_AUTO_MIGRATE` is accepted only in this mode. |

`deploy/compose.yaml` runs `migrate`, `api`, and `worker` as separate services. It sets a read-only root filesystem and a `/tmp` tmpfs. The API port defaults to loopback. The worker port is not published on the host. PostgreSQL is published on loopback and uses `sslmode=disable` inside that single Compose network. Webhook and Huawei private-network overrides default to false. The metrics bearer is required.

`fly.toml` uses the same split: process groups `api` and `worker`, `release_command = "migrate"`, and HTTP only for the API on port 8080. `min_machines_running` is 1. It is not a Kubernetes definition.

`internal/persistence/postgres/store.go` refuses to treat the database as ready unless `public.schema_version` matches this build and the bundled River migrations are fully applied. [`docs/operations.md`](../operations.md) already orders upgrades as: back up, run `migrate`, roll API processes, then roll workers.

There is no `deploy/k8s` tree and no Helm chart. A repository search finds no Kubernetes workload manifest.

River behavior, from `internal/worker` and `internal/jobs`, not from an invented election protocol:

- The worker starts one River client for `iapstack_inbox`, `iapstack_outbox`, and `iapstack_reconciliation`.
- Neither package implements leader election or a worker advisory lock.
- `internal/persistence/postgres/queue.go` locks the durable inbox, outbox, or reconciliation row with `FOR UPDATE`, inserts one River job when `river_job_id` is null, and stores that job ID. Insert options do not configure a separate uniqueness policy.
- [`docs/security.md`](../security.md) T07 assigns transactional claims, retries, stale-job recovery, unique scheduling, attempt limits, and shutdown to River.
- The production advisory lock in this repository is `pg_advisory_xact_lock` for API-key revocation (`internal/persistence/postgres/operations.go` and the break-glass statement in [`docs/operations.md`](../operations.md)). Integration tests also take `pg_advisory_lock` so they can share a database. Those locks are not a worker leader.

Default worker concurrency is 4 jobs per process (`internal/platform/config/config.go`). The pool created in `OpenStore` does not set `MaxConns`; pgx derives it unless the connection string sets it.

### Manifests to add

One namespace is an operator convenience. It is not a tenant boundary. The manifests assume the operator already has PostgreSQL 18 and a TLS ingress controller. They do not install PostgreSQL and they do not embed a database operator.

| Object | Role |
| --- | --- |
| `Job` `iapstack-migrate` | `command: ["migrate"]`. `completions: 1`, `parallelism: 1`, `backoffLimit` small, `restartPolicy: Never`. Runs to completion before the Deployments roll. Never a Deployment. |
| `Deployment` `iapstack-api` | `command: ["api"]`, `replicas: 1`, container port 8080. Readiness and liveness use `GET /readyz` and `GET /healthz` on that port. |
| `Deployment` `iapstack-worker` | `command: ["worker"]`, `replicas: 1`, container port 8081. No Ingress. |
| `Service` `iapstack-api` | ClusterIP port 8080. The only Ingress backend. |
| `Service` `iapstack-api-metrics` | ClusterIP to the same API pods, port 8080. Not an Ingress backend. |
| `Service` `iapstack-worker` | ClusterIP port 8081 for probes and `/metrics`. Not an Ingress backend. |
| `Ingress` `iapstack-api` | TLS, API host only. Paths for the public API and `/dashboard`. Include `/healthz` and `/readyz` only if the ingress controller needs them. Do not add `/metrics`. |

Two metrics Services are required because the API and the worker are different pods and already listen on different ports. One shared Service would invent a port that the process does not have.

Do not set `IAPSTACK_AUTO_MIGRATE`. The API and worker modes reject it, and a split deployment must not migrate from a replica. The Job uses the same image digest as the Deployments it gates. `Ping` failing a schema mismatch is the runtime backstop; it is not a reason to race the Job against new pods.

**Security context on every IAPStack pod**, including the Job:

- `runAsNonRoot: true`, `runAsUser: 65532`, `runAsGroup: 65532`
- `readOnlyRootFilesystem: true`
- an emptyDir mounted at `/tmp` only, matching the Compose tmpfs
- `allowPrivilegeEscalation: false`
- drop all capabilities
- `privileged: false`

**Secrets.** The orchestrator injects environment from a Kubernetes Secret or from an existing external secret store. Do not put secret values in a ConfigMap, a manifest, or the image. The API, worker, and migrate Job need the database URL, `IAPSTACK_PROTECTION_ACTIVE_KEY_ID`, `IAPSTACK_PROTECTION_KEYS`, `IAPSTACK_PROTECTION_FINGERPRINT_KEY`, and `IAPSTACK_METRICS_BEARER_TOKEN`. `IAPSTACK_BOOTSTRAP_ADMIN_KEY` is API-only, and only until a durable admin key exists, as Compose already does. The worker and the migrate Job do not receive it.

**PostgreSQL TLS.** Copying Compose `sslmode=disable` into the cluster is wrong once database traffic leaves one trusted host. [`docs/operations.md`](../operations.md) and [`docs/security.md`](../security.md) require certificate-verified TLS in that case. The manifest's connection string uses `sslmode=verify-full` and a mounted CA bundle. The operator supplies the PostgreSQL endpoint. Backups and restore rehearsals stay the procedure in [`docs/operations.md`](../operations.md); these manifests do not automate them.

**Private-network defaults.** Set `IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS=false` and `IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS=false`. Do not turn either on so that a webhook can reach a ClusterIP. If an approved destination is inside the cluster, the operator sets the matching flag on both the API and the worker and allows only that destination with egress policy. That exception is already the documented residual risk, not a new Kubernetes default.

**Network exposure.** Publish the API Service through Ingress. Do not publish the worker Service or either metrics Service. Scrape `/metrics` from inside the cluster with the dedicated bearer. A public Ingress path that forwards every URL would also forward `/metrics`; the path list above is what keeps it private. The bearer remains mandatory even on the private Service. [`docs/security.md`](../security.md) T09 disables `/metrics` when the bearer is absent.

PostgreSQL has no Service in this set. Egress from the API and worker is limited to PostgreSQL, the store APIs, and the configured webhook hosts.

### Single replica, and what a later API HPA requires

Start at one API pod and one worker pod. That matches Fly's single-machine minimum and the current absence of a capacity measurement.

A later HorizontalPodAutoscaler may target only `iapstack-api`. It requires all of the following, and none of them are present just because the Deployment exists:

- CPU or memory requests on the API container, so the scaler has a signal. Prefer those requests over inventing a new application metric. If the scaler reads the private metrics endpoint, it still must not put customer or provider values into labels.
- An explicit `pool_max_conns` on `IAPSTACK_DATABASE_URL` before replicas increase. The server does not cap the pool itself, so each new pod multiplies connections.
- The same protection keys, fingerprint key, and metrics bearer on every API pod. Admin sessions already tolerate one minute of clock skew across API instances (`internal/auth/admin_session.go`). No pod-local session store is required.
- Readiness on `/readyz`, so a pod that cannot see the migrated schema does not receive traffic.
- `maxSurge: 1` when leaving a single replica, so the process can roll forward. The API holds no lease to split.
- The migrate Job remains `completions: 1` and is not a scale target. Schema rollback still follows [`docs/operations.md`](../operations.md): stop API and workers before migrating down.

The worker stays at one replica in this plan. A second worker is not forbidden by the code: River claims jobs, and this repository does not elect a leader. Scaling it is still out of scope here. Do not add leader election in order to run the single worker. Do not autoscale the worker as part of the API HPA.

### Rejected in this plan

- A Kubernetes operator or any custom resource that reconciles IAPStack.
- Active-active PostgreSQL, multi-master writes, or two primaries.
- Zero-downtime regional failover, global load balancing, and replicated queues across regions.
- Running `server` (compact mode) as the production Kubernetes shape. Compact mode remains valid for local and single-process installs. The manifests use the split modes Compose and Fly already use.
- Bundling a PostgreSQL operator or a production StatefulSet that pretends these manifests manage the database.
- Replacing the provider-specific modules that [`deploy/README.md`](../../deploy/README.md) already excludes. These manifests are the generic container shape only.

### Open questions

- Which ingress controller and which cert issuer the example Ingress should name. The objects above are controller-neutral; the example should pick one only when the manifests are written.
- Whether the first exercise is kind, k3s, or a managed cluster. That choice does not change the objects.
- A numeric connection-pool cap is an operator input. This plan does not invent one.

## What stays outside this repository

The following are not later patches to the entitlement server:

- A hosted control plane: public signup, provisioning, billing of operators, residency routing, vendor incident access, and the SOC boundary around customer purchase data.
- A paywall studio: visual editing, experiments, targeting, store price changes, and consumable grants.
- Any change that makes verification or the entitlement projection consult an offering document.
- A Kubernetes operator, active-active deployment, and regional failover.
- One-click AWS, Google Cloud, or Azure modules. [`deploy/README.md`](../../deploy/README.md) already requires those to be designed separately, with networking, managed database, identity, logging, backup, and budget policy.
- Importing another vendor's customers, receipts, or historical entitlements.

Work that does stay here, when it is scheduled, is limited to an offering document and its read contract, and to plain manifests for `migrate`, `api`, and `worker`. Neither one changes the v0.1 release gate.
