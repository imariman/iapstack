# IAPStack v0.1 Threat Model and Security Release Checklist

This document defines the security boundary for the self-hosted Huawei lifetime and
subscription vertical slice plus the initial Apple and Google Play server slices. It
applies to the `api`, `worker`, and `migrate` modes, PostgreSQL 17, the embedded
operations dashboard, the Flutter SDKs, provider integrations, and outbound
application webhooks.

## Security objectives

IAPStack must:

- grant or remove access only from authenticated, authoritative provider state;
- isolate every project, application, customer, credential, purchase, and webhook;
- avoid storing plaintext provider credentials, purchase evidence, notification
  payloads, or webhook signing secrets in PostgreSQL;
- remain idempotent under duplicate requests, notifications, reconciliation, worker
  restarts, and webhook retries;
- expose only bounded, redacted errors, logs, metrics, and administration responses;
- fail closed when authentication, signature verification, protection, persistence, or
  provider consistency checks fail.

Availability against a volumetric denial-of-service attack, a malicious host
administrator, a compromised process with access to all runtime secrets, and a
compromised Huawei signing key are outside the application boundary. Operators must
provide host hardening, TLS termination, ingress limits, network policy, monitoring,
secret management, and backup security.

## Assets and trust boundaries

The highest-value assets are protection root keys, bearer API keys, Huawei client
secrets and public-key configuration, signed purchase evidence, external customer
identifiers, entitlement projections, webhook signing secrets, PostgreSQL contents,
and encrypted backups.

```text
Flutter application
    | customer session + signed provider purchase over TLS
    v
TLS ingress -> API/dashboard -> PostgreSQL
                 |                 ^
                 | protected jobs  | atomic state and audit data
                 v                 |
               Worker ------------+
                 |                      |
                 | OAuth/order/subscription queries
                 v                      v
            Huawei APIs          Application webhook
```

Each arrow is a trust boundary. TLS is expected between external parties and the
deployment ingress. Provider and webhook clients require HTTPS. The default Compose
network is a single-host development baseline; multi-host production deployments must
also protect database traffic.

## Attacker capabilities

The model assumes an attacker may send arbitrary HTTP methods, headers, JSON, signed
evidence copied from another user or application, duplicate notifications, stale
webhook events, malicious webhook URLs, slow requests, oversized responses, invalid
provider responses, and concurrent requests. An attacker may know project and
application identifiers and may obtain one customer session. That session must not
authorize another application, customer, or any administrator route. Durable
application bearers remain restricted to trusted host backends that mint sessions
after authenticating their users.

The model also assumes a database-only attacker does not possess the protection roots,
and a network attacker does not defeat correctly configured TLS. If those assumptions
fail, incident response and secret rotation are required.

## Threat register

| ID | Threat | Application controls | Required operational control or residual risk |
| --- | --- | --- | --- |
| T01 | Stolen or guessed administrator/application bearer or customer session | Generated keys contain 256-bit random secrets, only Argon2id verifiers are stored, bootstrap comparison is constant-time, bootstrap keys shorter than 32 bytes are rejected, and every route enforces role and application scope. Customer sessions are encrypted, customer-bound, expire after 15 minutes, and require a still-active issuer key. Stored-key listing is secret-free; revocation is idempotent and atomically preserves the final active administrator. | TLS and ingress rate limits are mandatory. Keep durable application bearers in trusted host backends, remove the bootstrap key after provisioning, and rotate through a replacement bearer before revoking the old key. |
| T02 | Cross-project or cross-application access | Principals carry durable project/application scope; repositories and protection authenticated data include both identifiers; scope failures return the same unauthorized envelope. | Treat identifier disclosure as expected and rely on authorization, not identifier secrecy. Keep scope regression tests in the release gate. |
| T03 | Webhook SSRF, DNS rebinding, or redirect escape | Only absolute HTTPS URLs without user information or fragments are accepted. Every A/AAAA result is validated at connection time, the connection is pinned to a validated IP, mixed public/private answers are rejected, proxies and redirects are disabled, and response size/time are bounded. | Private-network delivery is disabled by default. When explicitly enabled, infrastructure egress allowlisting becomes mandatory because application-level address blocking is intentionally bypassed. |
| T04 | Forged, cross-customer, or replayed provider notification | Huawei signed purchase bytes and customer scope are verified before acknowledgement. Google Play pushes require a Google-signed OIDC token with exact audience and verified service-account email, exact Pub/Sub subscription and package scope, and one strict RTDN variant. Strict normalized envelopes share one protected inbox identity. Workers query authoritative provider state; Google purchase tokens must resolve to a previously verified protected reference before customer access can change. | Apply ingress limits to bound repeated valid evidence. Compromised Huawei signing material or a compromised configured Google push principal is outside this boundary. |
| T05 | Forged or replayed purchase verification | Huawei and Apple signed evidence is checked locally; all providers are queried for authoritative server state. Application, product, purchase token or transaction identity, and customer bindings must match before the atomic persistence transaction. Duplicate evidence produces one logical projection. | Mobile clients are untrusted. Never grant access from Flutter SDK or store state without the server entitlement response. |
| T06 | Webhook payload tampering or receiver replay | Each immutable outbox body is signed with HMAC-SHA-256 over `<timestamp>.<raw_body>` and carries a stable event ID. Redirects, response size, attempts, and retryable statuses are bounded. | Receivers must verify the raw body, enforce a timestamp tolerance, and atomically deduplicate event IDs. Delivery is at-least-once, not exactly-once. |
| T07 | Queue race, crash, or duplicate side effects | River owns transactional claims, retries, stale-job recovery, unique scheduling, bounded attempts, and shutdown. Projection and outbox insertion share the verification transaction. | Monitor available, retryable, running, cancelled, and discarded queue states. Retain terminal metadata for the configured audit period. |
| T08 | Database disclosure or ciphertext substitution | AES-256-GCM values use random nonces, versioned key IDs, HKDF-derived subkeys, and project/application/purpose authenticated data. Stable fingerprints use a separate HMAC root. API keys use one-way memory-hard verifiers. | A database plus runtime-root compromise exposes protected data. Separate database and secret-manager access, encrypt backups, and retain old encryption keys until rotation completes. |
| T09 | Secret or purchase leakage through logs, metrics, errors, or dashboard | Access logs use route patterns and bounded request IDs; provider and worker failures expose stable codes; metrics use bounded labels; admin reads omit ciphertext, fingerprints, secrets, tokens, and payloads; JSON responses are `no-store`. | Restrict log, metric, and dashboard access. Do not add user/provider values as metric labels or error text. |
| T10 | Slow request, oversized input/output, authentication amplification, or worker exhaustion | HTTP header/body/read/write/idle limits, provider and webhook timeouts, bounded provider/webhook responses, fail-fast Argon2id concurrency, worker concurrency, per-job deadlines, and maximum attempts constrain resource use. | A distributed rate limiter and volumetric protection remain ingress responsibilities in v0.1. |
| T11 | Dashboard token theft or browser injection | The dashboard uses same-origin admin APIs, `sessionStorage`, `no-store`, a restrictive CSP, frame denial, MIME sniffing denial, and no third-party scripts. | Use a trusted, patched browser over TLS. Any same-origin XSS or hostile browser extension can steal the active bearer; use a dedicated administration origin where possible. |
| T12 | Dependency, image, or migration compromise | Go dependencies are pinned, CI runs formatting, vet, race, integration, migration lifecycle, contract, and container smoke checks; the runtime image is multi-stage, non-root, and read-only compatible. | Review dependency and base-image updates, pin deployed image digests, scan release images, protect CI credentials, and back up before migrations. |

## Security invariants

The following conditions are release-blocking:

1. No entitlement write occurs after signature, scope, catalog, customer-binding,
   protection, provider, or transaction failure.
2. Duplicate verification, restore, notification, reconciliation, or worker recovery
   does not create an additional logical entitlement change or outbox event.
3. A bearer for one application cannot read or mutate another application, an
   application bearer cannot use administrator routes, and a customer session
   cannot read or mutate another customer.
4. Plaintext credentials, signing secrets, access tokens, purchase tokens, purchase
   payloads, ciphertext, and fingerprints do not appear in HTTP errors, dashboard
   responses, access logs, provider logs, or metric labels.
5. Outbound webhook delivery cannot reach a non-public address unless the operator has
   explicitly enabled the private-network override.
6. Notification acknowledgement occurs only after signature/scope validation and
   durable protected inbox insertion.

## Release security checklist

### Code and automated gates

- [ ] `gofmt -w .` produces no diff.
- [ ] `go vet ./...` succeeds.
- [ ] `go test -race -count=1 ./...` succeeds.
- [ ] PostgreSQL integration tests run against a clean PostgreSQL 17 database.
- [ ] Every migration applies, rolls back, and reapplies.
- [ ] OpenAPI contract validation and dashboard/Flutter checks succeed.
- [ ] The container smoke test confirms non-root and read-only filesystem operation.
- [ ] `./deploy/e2e/run.sh` passes lifetime, refund/revocation, duplicate notification,
  worker restart, entitlement, metrics, and signed webhook assertions.
- [ ] SSRF tests cover literal private ranges, IPv4-mapped IPv6, CGNAT, link-local,
  mixed DNS answers, redirect refusal, timeout, and bounded responses.
- [ ] Security regression tests cover signature tampering, application/product/customer
  mismatch, cross-scope auth, notification canonical replay, protected-data
  authentication, stable error codes, and log/dashboard redaction.

### Deployment review

- [ ] Public traffic terminates at trusted TLS ingress; HTTP redirects to HTTPS.
- [ ] API, dashboard, metrics, worker probes, and PostgreSQL have the intended network
  exposure and firewall rules.
- [ ] Ingress rate limits and maximum connection counts are configured.
- [ ] Protection roots, PostgreSQL credentials, Huawei client secrets, webhook secrets,
  and bearers come from a secret manager rather than committed environment files.
- [ ] The bootstrap admin key is at least 32 random bytes and is removed after durable
  administrator/application keys are stored.
- [ ] `IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS` is `false`, or an approved egress
  allowlist documents every permitted internal destination.
- [ ] PostgreSQL TLS verification is enabled when database traffic crosses hosts.
- [ ] Backup encryption, restore access, restoration rehearsal, image digest rollback,
  and encryption-key retention have been verified.
- [ ] Webhook receivers verify the HMAC over raw bytes, enforce timestamp tolerance,
  and deduplicate event IDs before applying state.
- [ ] Alerts exist for readiness failure, provider failures, queue backlog/discards,
  webhook terminal failures, elevated unauthorized responses, and unexpected restarts.

### Manual Huawei sandbox gate

- [ ] The test account and candidate APK both pass Huawei's device-side sandbox
  activation check before any purchase UI opens.
- [ ] Lifetime purchase and duplicate restore remain idempotent.
- [ ] Subscription renewal, cancellation, expiration, grace, refund, and revocation
  produce the expected entitlement state.
- [ ] Invalid signature and wrong application/product/customer bindings produce no
  entitlement or outbox write.
- [ ] Duplicate and missed-notification recovery converges through authoritative
  reconciliation.
- [ ] Application webhook verification succeeds using the exact production receiver
  implementation and rejects tampered, stale, and duplicate events.
- [ ] The secret-free evidence file passes `go run ./cmd/iapstack-release` and names
  the exact candidate commit and successful CI run.
- [ ] The evidence-only merge passes main CI and the protected `Stable release`
  workflow publishes the tag, image digest, SBOM, provenance, and evidence asset.

Any unchecked release-blocking invariant or code gate prevents a v0.1 stable release.
Operational checklist exceptions require a written deployment-specific risk acceptance.
Execute and record this section using the
[`v0.1.0 Huawei sandbox runbook`](releases/v0.1.0.md).
