# Customer support tools: search, timeline, manual grants

- Status: Implemented in the tree, pending review
- Date: 2026-11-18
- Release: Unnumbered post-v0.1 plan. It is not part of the `v0.1.0-rc.1` three-provider release gate. Do not cherry-pick it onto that candidate.
- Schema: migration `015_manual_grants.sql`. Migrations 014 and 016 belong to parallel work and must land in number order.

Support work was blind. The dashboard listed only the first 200 customers of a project, had no search, and had no view of one customer. An operator answering "why does this user not have premium?" had to read PostgreSQL. This plan closes gap rows E5 (customer search), E6 (customer timeline), and G1 (manual entitlement grant) from the competitor comparison. RevenueCat, Adapty, Qonversion, and Superwall all ship these three.

## Verified facts

Read from the tree before this change.

- `customers` (`001_catalog.sql`) is project-scoped. `external_id` is unique per project. `customer_aliases` and `customer_merges` (`010_customer_identity.sql`) carry `actor_key_id` and are the identity audit rows. `customers.merged_into_customer_id` is one hop.
- Provider identifiers live in `provider_references` (`002_purchase_evidence.sql`) as AES-GCM ciphertext plus an HMAC fingerprint. ADR-0006 scopes the fingerprint by project, application, and purpose. `verification.Service.prepare` uses purpose `provider_reference:<role>:<kind>`. Search therefore cannot compare plaintext. It must compute the fingerprint of the search text in every application scope.
- Adapters emit these searchable references: Apple `transaction/transaction_id` and `lineage/original_transaction_id`, Google Play and Huawei `transaction/order_id`. Purchase tokens (`query/purchase_token`) and customer bindings are bearer-like or personal and are not searched.
- `customer_entitlements` holds only the current projection and its `version`. There is no projection history table. Every change written by `verification.RefreshCustomerProjectionWithOptions` with webhooks on also writes an `entitlement.changed` outbox row whose payload carries entitlement, access, reason, version, and source. Outbox rows are the only stored projection history. Retention purges terminal ones (`PurgeTerminalQueueRecords`).
- The RevenueCat importer (`012_import_grants.sql`, `docs/plans/revenuecat-import.md`) already made the projection source-aware: `source_kind` is `store_observation` or `import`, exactly one source id is set, the projector ranks a store observation above an import, and import-backed webhooks use `schema_version` 2 with `source: import`. The plans README states the rule: historical access is an `import` projection, not a store verification.
- `requireDurableActor` (`internal/transport/httpapi/identity.go`) refuses the bootstrap administrator for audited identity writes, because it has no stored key id.
- `GET /entitlements` and the webhook payload evaluate `core.AccessAt` at read time. No worker writes a new version when an allowed projection passes its end.
- Tern refuses a gap in migration numbers ("Missing migration 14").

## Design

### Search

`GET /v1/admin/projects/{project_id}/customers?query=&limit=&page_token=`

The query is untrusted. The handler trims surrounding whitespace, refuses more than 512 bytes after trimming, refuses control characters and invalid UTF-8, and bounds `limit` to 1..100 (default 25). Every value reaches PostgreSQL only as a bound parameter. Prefix matching escapes `\`, `%`, and `_` before `LIKE ... ESCAPE '\'`, so `user_` matches only the literal prefix.

One query unions the match kinds and keeps the best rank per customer:

| Rank | `match` | Rule |
| --- | --- | --- |
| 1 | `external_id` | equals `customers.external_id` |
| 2 | `customer_id` | equals `customers.id` |
| 3 | `alias` | equals `customer_aliases.external_id`; returns the canonical owner |
| 4 | `transaction_id` | fingerprint equals a stored `transaction` reference |
| 5 | `original_transaction_id` | fingerprint equals a stored `lineage` reference |
| 6 | `external_id_prefix` | external id starts with the query |
| 7 | `customer_id_prefix` | internal id starts with the query |

An empty query lists customers newest first and sets no `match`. Results order by rank, then `created_at` descending, then id. `next_page_token` is an opaque keyset cursor (rank, created_at, id) bound to a short hash of the query. A token replayed with different text is refused. Access counts are computed only for the returned page.

Fingerprints: the store loads the project's application ids (bounded to 200) and asks a fingerprinter for the text in each `(application, role, kind)` scope above. The HTTP layer implements it with `protection.Protect`, discards the ciphertext, and passes only fingerprints. A search never decrypts a stored reference.

Migration 015 adds `text_pattern_ops` indexes on `(project_id, external_id)` and `(project_id, id)` for the prefix legs and `(project_id, created_at DESC, id)` for listing.

### Customer detail

`GET /v1/admin/projects/{project_id}/customers/{customer_id}`

The response describes the canonical closure. When the path names a merged customer, `customer` is that row with `merged_into_customer_id`, and access and timeline belong to `canonical_customer_id`.

- Identity: the requested row, closure members, alias audit rows, merge audit rows with the source external id.
- `entitlements`: every current projection with `source` (`store_observation`, `import`, or `manual`), `source_id`, source application and product, period, version, and `updated_at`. Access and reason are evaluated at the response time with `core.AccessAt`, like the public read.
- `manual_grants`: newest 100 with derived `status` (`active`, `expired`, `revoked`).
- `grant_options`: non-consumable product and entitlement pairs a grant may cite, with the applications that map each product.
- `timeline`: newest first, built only from stored rows. Each source is read with `LIMIT 100`. The merged list is capped at 200 and `timeline_truncated` says when older rows exist.

| `kind` | Stored record |
| --- | --- |
| `customer_created` | `customers.created_at` of each closure member |
| `alias_added` | `customer_aliases` with actor role and key |
| `customer_merged` | `customer_merges` with actor key |
| `purchase_observed` | `purchase_observations`, with `previous_lifecycle_state` from the previous snapshot of the same lineage |
| `import_grant_recorded` | `import_grants` |
| `manual_grant_created`, `manual_grant_revoked` | `manual_grants` with actor key and reason |
| `entitlement_changed` | outbox `entitlement.changed` payload fields, with delivery `status`; plus the current projection when no outbox row records its version |
| `webhook_delivered`, `webhook_failed` | outbox `delivered_at`, `failed_at`, `last_error_code` |
| `reconciliation` | `reconciliation_jobs` outcome |

The detail never returns ciphertext, fingerprints, purchase evidence, provider references, or webhook bodies. Outbox payloads are read field by field in SQL. Migration 015 indexes outbox rows by customer aggregate and alias and merge rows by canonical customer.

### Manual grants

`POST /v1/admin/projects/{project_id}/customers/{customer_id}/manual-grants`

```json
{
  "idempotency_key": "dashboard-6f1c...",
  "entitlement_id": "entitlement-premium",
  "product_id": "premium-yearly",
  "application_id": "ios-production",
  "reason": "Support ticket 4821: renewal failed during store outage",
  "duration_days": 30
}
```

Exactly one of `duration_days` (1..3660) or `ends_at` (future, at most about ten years ahead). The grant starts when it is stored.

`POST .../manual-grants/{grant_id}:revoke` with `{"reason": "..."}` ends it early.

Both need an administrator with a stored key. The bootstrap key and dashboard sessions created from it get `403 forbidden`, the same rule as alias and merge. The reason is required, single line, at most 500 characters.

Model. A manual grant is a third projection source, modelled like an import:

- `manual_grants` stores the canonical customer, application, product, entitlement, reason, actor key, idempotency key, period, and the all-or-nothing revocation fields (`revoked_at`, `revoked_by_key_id`, `revocation_reason`). The row is the audit record. It is never written as `purchase_evidence`, `verified_artifacts`, `purchase_observations`, or `provider_references`.
- `customer_entitlements.source_kind` gains `manual` and a `source_manual_grant_id` foreign key. Exactly one source id is set, and it matches the kind.
- `CustomerEntitlementSources` emits every grant as its own source with lineage `manual:<id>`: allowed until revoked, then denied with reason `revoked` and the period ending at the revocation.
- The projector never supersedes a manual grant with store evidence and never lets it supersede store evidence. Store still outranks an import before end times are compared, exactly as before. Otherwise the allowed source whose access lasts longest is projected, and equal ends prefer store evidence, then a manual grant, then an import. A 30-day goodwill grant on top of a subscription that renews in five days is therefore projected until the store access lasts longer. A grant behind lifetime store access changes nothing, and the response says so (`projections_changed: 0`).
- The handler runs the normal `RefreshCustomerProjectionWithOptions`, so a changed projection writes the normal signed `entitlement.changed` outbox event to the grant's application. The payload is `schema_version` 3 with `source: manual` and `source_manual_grant_id`. It has no `source_observation_id`, no `source_import_id`, and no reason. The Go and TypeScript host SDK verifiers accept version 3 only in that shape.
- Replay: the same `idempotency_key` with the same customer, entitlement, product, application, and reason returns the stored grant with `replay: true` and refreshes nothing. A different body under the same key is `409`. A second revoke returns the stored revocation.
- Expiry is the read path, like store and import projections. No worker and no webhook fire at `effective_ends_at`.

Why this respects ADR-0005 and "verification before access": an observation is still only verified store evidence, and nothing on the store path changed. A manual grant is an explicit, audited operator decision that is labelled `manual` in the projection, the admin API, the dashboard, and the webhook. It never appears in recent verifications, the 30-day verified series, or sandbox value.

### Dashboard

- The Customers panel replaces the fixed "First 200" label with a search field (debounced, plus an explicit Search button), a live count, a newest-first listing with Load more, and separate empty ("No customers yet.") and no-match states. Each row opens the detail. A match label says why a row matched.
- The detail is a `setup-dialog` like the other dialogs: identity vitals, aliases and merges, an Access table whose Source column names "Store verification", "Import", or "Manual grant" (non-store sources also get a dashed badge edge), manual grants with Revoke, and the timeline. Every state has a text label.
- Grant and revoke are small dialogs over the detail, like the access key revocation. Grant sends a fresh idempotency key per opening, so a double submit cannot create two grants. The 403 for bootstrap sessions explains that a stored admin key is needed.
- All untrusted values are rendered as text nodes.

## Tests

- Projector: longer manual outlasts shorter store access; store wins on equal or longer access; a store denial for the same product does not clear a manual grant; revocation falls back to store access or projects the revoked grant; manual beats import on equal ends; version 3 payload shape.
- PostgreSQL: grants follow the merge closure, replay and conflict, catalog refusals (consumable, product not granting, unknown application, product, or customer), no evidence or observation rows, revocation period and version, cross-closure revoke refused; search ranks, literal `LIKE` escaping, fingerprint matches, keyset paging; merged-customer detail; migration constraints and refusal to roll back with grants or manual projections.
- HTTP: input bounds and cursor binding before the store runs, fingerprints equal the verification scopes, bootstrap and application principals refused, period and reason validation, `:revoke` routing, access evaluated at read time; an integration journey over real PostgreSQL covering search by transaction id, grant, replay, public entitlement, outbox payload, revoke, timeline, and that the overview still counts one verification.
- Dashboard: markup, script, and style contract tests, and OpenAPI coverage of every field the dialog reads.

## Open questions

- Public `reason` of an allowed manual grant. The v1 `Entitlement.reason` enum has no operator value. The projection uses `purchase_valid`, as imports do, and the source kind carries the manual fact. A dedicated public reason such as `manual_grant` would be an additive v1 contract change and needs a maintainer decision.
- SDK release. Hosts on Go or TypeScript SDK releases before this change reject version 3 events and the outbox will retry and then fail them. The SDK changelog has an Unreleased entry; the maintainer decides when to cut it. Until then operators can see the failure in the timeline and the delivery table.
- Application choice. A grant names the application whose webhook endpoint receives the change. The dashboard lists every application and puts the ones that map the product first. Should the API require a store-product mapping for that application?
- Migration order. On this branch alone tern refuses to load migrations because 014 is missing, and `TestMigrationLifecycle` lists 14 relations for version 15. Merge after the PR that owns 014 and add its relation before `manual_grants`.
- Expiry notification. A grant that ends naturally fails closed on read but emits no webhook, like store and import projections. A later worker could emit one change at the end instant for every source kind.
- Timeline depth. Outbox and reconciliation rows are purged by retention, so older projection history disappears. A projection history table would keep it, at the cost of another write per change.
- Search scope. Exact and prefix only, case-sensitive. Substring or case-insensitive search would need trigram indexes.
- Fingerprint cost. The protection port only offers `Protect`, so each non-empty search encrypts the text once per application and reference scope (three scopes) and discards the ciphertext. Each call uses one GCM nonce under the active key. That is far below the ADR-0006 rotation bound at dashboard rates, but a fingerprint-only method on the port would avoid it.
