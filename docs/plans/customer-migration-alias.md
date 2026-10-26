# Customer migration and alias consolidation

- Status: Proposed
- Date: 2026-10-26
- Release: Unnumbered post-v0.1 plan

This plan is not part of the `v0.1.0-rc.1` three-provider release gate. Do not cherry-pick it onto that candidate. The candidate stays on PostgreSQL schema version 9 (`internal/persistence/postgres/migrate.go`, `LatestVersion`). Shipping the migration below bumps that constant and will refuse to boot a version-9 binary.

`docs/v0.1-scope.md` lists automatic customer alias merging, beyond one stable external customer ID, as an explicit non-goal. This plan is the later, explicit replacement. It does not make that non-goal a prerequisite for the first release.

## Identity boundary

The entitlement owner is one canonical customer inside one project. It is not one customer per application.

Verified from the schema and domain types:

- `customers` in `internal/persistence/postgres/migrations/001_catalog.sql` is keyed by `id` and scoped by `project_id`. `customers_external_id_unique` is `UNIQUE (project_id, external_id)`. There is no `application_id` on the row.
- `core.Customer` in `internal/core/catalog.go` has `ID`, `ProjectID`, and `ExternalID` only.
- `customer_entitlements` in `003_entitlement_projections.sql` has primary key `(customer_id, entitlement_id)`. Application is not part of that key. The source observation carries `application_id`.
- `GET /v1/applications/{application_id}/customers/{external_customer_id}/entitlements` authenticates an application-bound session, then loads every current projection for that project customer. `customerEntitlements` in `internal/transport/httpapi/api.go` does not filter `CustomerEntitlements` by application. Sibling applications in the same project already share one customer when they share one external id.

A merge or alias that crosses `project_id` is not representable. Foreign keys on evidence, observations, projections, and reconciliation jobs all include `project_id`. Missing ids in the path project return the existing `not_found` envelope. They do not reveal a row that exists in another project.

## What is true today

One project has one `customers` row per external id. `CustomerByExternalID` in `internal/persistence/postgres/catalog.go` is a direct lookup on `(project_id, external_id)`. It does not follow any alias. `007_query_indexes.sql` drops `customers_project_idx`; the unique constraint remains the lookup index.

`PutCustomer` in `internal/persistence/postgres/control.go` inserts on `(project_id, external_id)` and, when that external id already exists, returns the stored row even if the requested `id` differs. It does not return `ErrConflict` for that case. A conflicting primary key (`id` already used for a different external id) does conflict, because `ON CONFLICT` names only `customers_external_id_unique`. This plan does not change that replay for real `customers` rows. Alias creation must not copy the silent retarget. A mismatched canonical customer is `409`.

`PUT /v1/admin/projects/{project_id}/customers/{customer_id}` is the only customer write. It requires an admin bearer or a dashboard session (`putAdminCustomer` in `contracts/openapi/v1.yaml`). The body is `CustomerRequest`: `external_id` only. There is no alias, resolve, or merge route.

Customer sessions do not create customers. `createCustomerSession` mints a 15-minute bearer for any well-formed `external_customer_id` and does not read `customers`. Entitlement lookup then returns `404` when the external id is absent. The session stores the external id (`internal/auth/customer_session.go`). The entitlement response `customer_id` is the internal `customers.id`.

Purchase binding:

1. The trusted host mints a session for one external id.
2. Verify and restore reject a body `external_customer_id` that is not exactly the session value (`401`, same unauthorized envelope).
3. `jobs.VerificationInputs` in `internal/jobs/contracts.go` builds one authoritative `customer_binding` whose value is that external id. Apple's kind is `app_account_token`, Google Play's is `obfuscated_external_account_id`, Huawei's is `developer_payload`.
4. `verification.Service.loadScope` loads the application and `CustomerByExternalID` before the provider call. `persist` locks that customer with `LockCustomer` (`UPDATE customers SET updated_at = updated_at`), re-reads the external id, and rejects the write with `ErrConflict` if the customer row changed.
5. Evidence and observations are stored under that internal `customer_id`.

`internal/worker/runner.go` treats `persistence.ErrConflict` as permanent. `runner_test.go` locks that in. A merge implementation that makes an in-flight verification return `ErrConflict` will fail the inbox or reconciliation job for good. The race rule below exists to avoid that.

Entitlement projection is `DefaultProjector` in `internal/verification/projector.go`. For one customer it keeps the stronger independent application-plus-product source. It will pick one `allowed` row when two sources disagree. That policy is not a merge policy. Merge must refuse the conflicting case before the projector runs.

Outbox events for a changed projection use `aggregate_type = customer`, `aggregate_id = customers.id`, and payload `customer_id` set to that same internal id (`newEntitlementEvent` in `internal/verification/service.go`, schema version 1). The payload has no external id. `inbox_messages` and `outbox_events` have no `customer_id` column (`004_inbox_outbox.sql`). Apple's normalized inbox envelope can still carry `external_customer_id` inside `payload_ciphertext` (`internal/stores/apple/notification.go`). That ciphertext stays as written.

`reconciliation_jobs.customer_id` references `customers` and is part of `UNIQUE (application_id, customer_id, payload_fingerprint)` (`006_operations.sql`). Notification workers do not look customers up by store account token. Huawei and Google resolve a protected purchase-token fingerprint through `PurchaseContextByReference`, then build the expected binding from the observation owner's `customers.external_id` (`internal/processing/handlers.go`). Apple notifications carry the account token inside the signed payload and are verified by the normal binding check.

Transactions run at serializable isolation (`internal/persistence/postgres/store.go`).

## Store account tokens

These are `provider_references` rows with `role = customer_binding`. They are not a second customer primary key. `provider_references` has no `customer_id`. Uniqueness is `(application_id, role, kind, value_fingerprint)`. The value is encrypted. The customer is reached only through `observation_references` and `purchase_observations.customer_id`.

| Store | What the code compares | Kind stored | Not a customer key |
| --- | --- | --- | --- |
| Apple | `appAccountToken`, lowercased in `internal/stores/apple/jws.go`, must be a 36-character UUID (`validAppAccountToken`), and must equal the expected binding (`internal/stores/apple/adapter.go`) | `app_account_token` | Original transaction id, transaction id |
| Google Play | `obfuscatedExternalAccountId` on product purchases and on subscription `externalAccountIdentifiers`. Empty is rejected before a reference is stored (`internal/stores/googleplay/adapter.go`) | `obfuscated_external_account_id` | `obfuscatedExternalProfileId` is decoded and not passed to `googleReferences` |
| Huawei | `developerPayload` must equal the request external id (`docs/api-v1.md`). Empty payload is omitted and then fails the expected-binding check | `developer_payload` | Purchase token and order id are query and transaction references |

Huawei `accountFlag` is not decoded. `purchaseData` in `internal/stores/huawei/adapter.go` has `developerPayload` and no account-flag field. No file under `internal/stores/huawei` mentions one. This plan does not add one, and it does not treat a raw flag inside signed purchase JSON as an external customer id. This plan did not re-read Huawei's current `InAppPurchaseData` reference, so it does not assert what that field means in the store API.

An alias does not rewrite a store token and does not make verification skip the equality check. A purchase whose Apple, Google, or Huawei binding is the old external id still verifies only when the session and the request present that same external id. A new external id can be an alias for entitlement lookup, and a new purchase can be stored on the canonical customer only when the store binding equals the external id on that request. Restores of an old store token under the new external id keep failing the binding check. That is the current security check, and it stays.

Merge and alias requests do not accept raw store tokens, purchase tokens, `appAccountToken`, `obfuscatedExternalAccountId`, `developerPayload`, or `provider_references` fingerprints. There is no resolve-by-token operation.

## Decision

Keep every historical evidence row on the customer id it was written with. Teach identity resolution to find the canonical customer. Recompute only the canonical customer's current projection, citing the original observation through a new source column. Do not `UPDATE` `customer_id` on evidence, observations, inbox rows, outbox payloads, or existing source projections.

Two mechanisms:

- **Alias.** An additional external id in the same project resolves to a canonical `customers.id`. The id is not already a `customers.external_id`.
- **Merge.** Two existing customer rows become one owner. The source row stays, because `purchase_evidence` is `ON DELETE RESTRICT`, `customer_entitlements` is `ON DELETE CASCADE`, and `reconciliation_jobs` is `ON DELETE CASCADE`. Deleting the source would either be refused or would destroy projections and jobs. Set `merged_into_customer_id` instead. The source `external_id` stays on that row so `customers_external_id_unique` and store-binding equality keep working.

No chains. A customer with `merged_into_customer_id` set is not a merge target. A customer that already has aliases, or that already has other rows merged into it, is not a merge source. Attach those identities to the final canonical customer first, in separate calls. Lookup follows at most one hop and fails closed if it finds a second.

Conflicting active entitlements are not combined. Active means `customer_entitlements.access_status = 'allowed'` for the same `entitlement_id` on both customers. That includes `grace_period` and `canceled_at_period_end`, because those reasons are still `allowed` in the check constraint. `denied` and `unresolved` do not conflict with `allowed` or with each other. Different entitlement ids do not conflict; the canonical owner exposes the union. There is no force flag.

`DefaultProjector` already prefers one allowed source inside a single customer. Do not use it as the conflict check.

## API

All bodies are JSON with `additionalProperties: false`. Identifiers use `core.ValidateExternalCustomerID` for external ids and the same rules as `validateIdentifier` in `internal/core/identifiers.go` for customer ids and idempotency keys: required, valid UTF-8, no leading or trailing whitespace, no control characters. The database checks on `customers.external_id` are weaker (non-empty and `btrim` only). Keep the Go checks at the HTTP boundary. Do not add a length limit the code does not have today.

v1 has no `Idempotency-Key` header. Do not add one here. Alias identity is the path. Merge identity is the body key plus the customer pair.

Error responses stay on `ErrorEnvelope` / `ApiError`: `code`, `message`, optional `request_id`. Do not add an entitlement-id field. Clients branch on `code`. SDK retry stays 429 and 5xx only, so these 409 responses are not retried by the existing clients.

Shared error cases:

| Situation | Status | Code |
| --- | --- | --- |
| Bad identifier, identical source and target, or a missing merge field | 400 | `invalid_request` |
| Missing bearer, customer session, application bearer on an admin route, admin bearer on an application route | 401 | `unauthorized` |
| Bootstrap admin (`Principal.KeyID` empty; `Authenticate` in `internal/auth/service.go` returns role admin and no key id), or dashboard cookie mutation that fails the existing origin check | 403 | `forbidden` or `csrf_rejected` |
| Project, application, customer, or external id not in the authenticated scope, including a row that exists only in another project | 404 | `not_found` |
| Alias or merge would retarget an id, source is already an owner, target is already merged, idempotency key reused with a different body | 409 | `conflict` |
| Both customers have `allowed` for the same entitlement id | 409 | `entitlement_conflict` |
| Existing size and media-type failures | 413, 415 | unchanged |
| Storage or authentication capacity | 503 | `storage_unavailable` or `authentication_unavailable` |

`entitlement_conflict` message: `the customers have conflicting active entitlements`. Do not put entitlement ids or external ids in the message.

### Create an alias

```http
PUT /v1/admin/projects/{project_id}/customers/{customer_id}/aliases/{external_id}
PUT /v1/applications/{application_id}/customers/{customer_id}/aliases/{external_id}
```

Body is `{}`. `{customer_id}` is the canonical internal id. `{external_id}` is the additional external id.

Admin route: stored admin bearer or dashboard session, same `requireAdmin` CSRF rule as other unsafe admin routes. Application route: application bearer only, and only when `principal.ApplicationID` matches the path. Load the application with `principal.ProjectID`. The customer must belong to that project. Ignore any project id that is not the path or the principal.

Results:

- New alias: `200` and the resolve body below with `match = alias`.
- Same path repeated: `200` and the same row. No second insert.
- `external_id` equals that canonical customer's `external_id`: `200`, `match = primary`, no alias row.
- `external_id` already resolves to that same canonical customer through a merged source row: `200` and the existing match. No alias row. Do not duplicate the external id into `customer_aliases`.
- `external_id` resolves to a different customer, or `{customer_id}` is itself merged into someone else: `409 conflict`.
- Application bearer cannot turn this into a merge by any extra field. The body has no fields.

### Resolve an external id

```http
GET /v1/admin/projects/{project_id}/customer-resolutions/{external_id}
GET /v1/applications/{application_id}/customer-resolutions/{external_id}
```

Authorization matches the alias routes. Customer sessions cannot call these. Probing other customers stays on the admin and the trusted host.

`200`:

```json
{
  "project_id": "project-1",
  "external_id": "auth-user-new",
  "canonical_customer_id": "customer-1",
  "match": "alias"
}
```

`match` is `primary`, `alias`, or `merged`. `merged` means the external id is still `customers.external_id` on a source row whose `merged_into_customer_id` is the canonical id. Unknown id: `404`.

### Merge two customers

```http
POST /v1/admin/projects/{project_id}/customer-merges
```

Admin only. Not the application bearer. Not a customer session.

```json
{
  "idempotency_key": "merge-auth-user-42",
  "canonical_customer_id": "customer-1",
  "source_customer_id": "customer-2"
}
```

`200` on first success and on every safe replay:

```json
{
  "id": "merge-…",
  "project_id": "project-1",
  "canonical_customer_id": "customer-1",
  "source_customer_id": "customer-2",
  "idempotency_key": "merge-auth-user-42",
  "created_at": "2026-10-26T00:00:00Z"
}
```

`id` is the merge audit id. The server generates it once (`merge-` plus random hex is enough if it passes identifier validation) and returns the stored value on replay. The client does not choose it.

Replay rules, all inside one serializable transaction:

- Same `idempotency_key`, same canonical id, same source id: return the stored audit row. Do not insert another row, do not run the projector again, do not write another outbox event.
- Same `idempotency_key`, different canonical or source: `409 conflict`.
- A new key whose pair is already merged to that same canonical customer: `200` and the original audit row. The pair is the state. A second audit row would lie.
- A new key that asks to merge an already merged source into a different canonical customer: `409 conflict`.
- Either id missing in the path project: `404`.
- Target `merged_into_customer_id` is not null, or source is already an alias owner or a merge owner: `409 conflict`.
- Dual `allowed` entitlement: `409 entitlement_conflict`, and no audit row.

Lock order is lexicographic `customers.id`, using the existing `LockCustomer` statement on both rows, then on every customer already merged into the target if the projection update needs them. Re-read after the locks. Dashboard session mutations use the existing CSRF check because the route goes through `requireAdmin`.

On success, if the canonical projection actually changes, write the existing `entitlement.changed` outbox event. `customer_id` and `aggregate_id` are the canonical id. `source_observation_id` is the original observation id. `application_id` is that observation's application id. Do not invent `customer.merged`. Do not edit an old outbox payload. If the union does not change the canonical snapshot, write the audit row and no outbox event.

### Reads and purchases after resolution

`CustomerByExternalID`, used by verification and by entitlement GET, resolves to the canonical customer:

- `customers.external_id` and `merged_into_customer_id` is null: that row.
- `customers.external_id` and `merged_into_customer_id` is set: the target row, one hop. If the target is itself merged, return a conflict-class error, not a guess.
- else `customer_aliases`: the canonical row, which must not itself be merged.

The returned `core.Customer.ID` is the canonical id. `ExternalID` on that struct stays the canonical row's `external_id`, matching the column, not the caller's alias. Callers that need the caller's external id already have it on the command or the session. `PutCustomer` must not call this resolving lookup. It keeps a direct read of `customers` so a replay of the original PUT still returns the source row and does not clear `merged_into_customer_id`. If the external id is only an alias, `PutCustomer` returns `ErrConflict` instead of inserting a second customer.

Entitlement GET keeps comparing the path external id to the session, then resolves, then returns the canonical internal id and the canonical owner's projection. A session may still be minted before the customer or the alias exists. Unknown external ids still `404` on GET.

Verification of a new external id that is an alias stores new evidence on the canonical id, with the store binding equal to the alias. Verification of an external id that belongs to a merged source still presents that source external id to the store. The canonical id is the projection owner. The evidence-row owner is chosen by the closure rule in the next section, not by rewriting the old row.

`PurchaseContextByReference` continues to return the `customers` row joined from the observation. It must not skip `merged_into_customer_id` by rewriting that query into a canonical-only join. The worker keeps building the expected binding from the external id stored on the observation's customer, which is the value the store signed. `loadScope` then resolves that external id to the canonical customer before the new write.

In-flight verification that already loaded the source id, then loses the race with a merge, must not return permanent `ErrConflict` merely because resolution now returns the canonical id. Inside `persist`, after the locks, if the external id still resolves and the previously loaded id is the canonical id or a source merged into it, continue and write under the closure rule. A changed application, a disappeared external id, or a customer that resolved to an unrelated id is still `ErrConflict`.

## PostgreSQL

Add `internal/persistence/postgres/migrations/010_customer_identity.sql` in the existing tern shape (`---- create above / drop below ----`). Bump `LatestVersion` to 10 in the same change. Runtime already refuses a mismatch (`ErrSchemaVersionMismatch`).

### `customers`

Existing columns used by this plan: `id`, `project_id`, `external_id`, `created_at`, `updated_at`. Existing constraints: primary key `id`, `customers_id_project_unique` on `(id, project_id)`, `customers_external_id_unique` on `(project_id, external_id)`, `customers_project_fk`, `customers_id_valid`, `customers_external_id_valid`.

Add:

- `merged_into_customer_id text NULL`
- foreign key `(merged_into_customer_id, project_id)` to `customers (id, project_id)` `ON DELETE RESTRICT`
- check `merged_into_customer_id IS NULL OR merged_into_customer_id <> id`
- partial index `(project_id, merged_into_customer_id)` where the column is not null

No status column. Null means canonical.

### `customer_aliases`

New table. One additional external id that is not a `customers.external_id`.

- `project_id text NOT NULL`
- `external_id text NOT NULL`
- `canonical_customer_id text NOT NULL`
- `actor_role text NOT NULL` check `IN ('admin', 'application')`
- `actor_key_id text NOT NULL` check non-empty and trimmed. This is `api_keys.id` of the stored admin or application key. No foreign key: revoking a key must not cascade into identity.
- `created_at timestamptz NOT NULL DEFAULT now()`
- primary key `(project_id, external_id)`
- foreign key `(canonical_customer_id, project_id)` to `customers (id, project_id)` `ON DELETE RESTRICT`
- `external_id` check copied from `customers_external_id_valid`

The write transaction, not a check constraint, refuses a canonical customer whose `merged_into_customer_id` is set, and refuses an `external_id` that already exists on `customers`. Serializable isolation plus locking the canonical customer with `LockCustomer` closes the race with `PutCustomer`.

### `customer_merges`

New append-only audit table. No update method and no delete method.

- `id text PRIMARY KEY`
- `project_id text NOT NULL`
- `canonical_customer_id text NOT NULL`
- `source_customer_id text NOT NULL`
- `idempotency_key text NOT NULL`
- `actor_key_id text NOT NULL`
- `created_at timestamptz NOT NULL DEFAULT now()`
- unique `(project_id, idempotency_key)`
- unique `(source_customer_id)` because `customers.id` is globally unique
- check `source_customer_id <> canonical_customer_id`
- the same non-empty trimmed checks used on other ids
- foreign keys from `(canonical_customer_id, project_id)` and `(source_customer_id, project_id)` to `customers (id, project_id)` `ON DELETE RESTRICT`

### `customer_entitlements`

Existing primary key `(customer_id, entitlement_id)` stays the live owner key. Existing `customer_entitlements_source_fk` references `(source_observation_id, customer_id, project_id, source_product_id)` on `purchase_observations (id, customer_id, project_id, product_id)`. That forces the projection owner and the observation owner to be the same row. A canonical projection cannot cite a source observation until that foreign key moves.

Add `source_customer_id text`. Backfill it to `customer_id`. Set it `NOT NULL`. Drop `customer_entitlements_source_fk` and add it again on `(source_observation_id, source_customer_id, project_id, source_product_id)`, still targeting `purchase_observations_source_unique`. Do not drop or rewrite existing projection rows. For every row this migration backfills, `source_customer_id = customer_id`.

`EntitlementProjection` in `internal/persistence/contracts.go` has no source-customer field today. Add one on that struct. It is internal. Do not add it to the public `Entitlement` schema in `contracts/openapi/v1.yaml`.

`PutEntitlement`, `loadCustomerEntitlement`, and the observation join in `CustomerEntitlements` (`internal/persistence/postgres/entitlement.go`) currently join `purchase_observations.customer_id` to `customer_entitlements.customer_id`. Point those joins at `source_customer_id`. While the values are equal, behavior is unchanged.

Down migration of version 10 runs only when `customer_aliases` and `customer_merges` are empty and every `source_customer_id` equals `customer_id`. Otherwise it raises and stops. The empty up/down test in `migrate_test.go` still passes. A database that has performed a merge does not go back to schema 9.

### Evidence idempotency inside a closure

Observation ids are hashes of store and lifecycle fields, not of `customer_id` (`observationSnapshotIdentity` in the Apple and Huawei adapters, `subscriptionObservationIdentity` in the Google adapter). `purchase_observations.id` is the primary key. `SaveObservation` treats a repeat as idempotent only when `customer_id` matches (`observationMatches`). `SaveEvidence` does the same on `(application_id, payload_fingerprint)` (`internal/persistence/postgres/purchase.go`). `purchase_observations_evidence_fk` requires the observation and its evidence to share `customer_id`.

After a merge, a naive write of the same snapshot under the canonical id is a permanent `ErrConflict`, and a new observation cannot reuse evidence owned by the source customer. Required repository behavior, only for a verified closure:

- Closure `S` is the canonical id plus every `customers.id` in that project whose `merged_into_customer_id` is the canonical id.
- New evidence fingerprint: insert evidence and observation under the canonical id.
- Existing evidence fingerprint whose stored `customer_id` is in `S`, with the same project, kind, and content type: reuse that evidence id. Do not update its `customer_id`. The observation written against that evidence uses the stored evidence `customer_id`, so the foreign key holds.
- Existing observation id whose stored `customer_id` is in `S` and whose snapshot matches: idempotent success. Do not update `customer_id`. Existing price enrichment of a null price may still run; that is current behavior, not a merge rewrite.
- Existing evidence or observation owned by a customer outside `S`: `ErrConflict`, as today.
- Projection owner is still the canonical id. `source_customer_id` is the observation's stored `customer_id`.

`CustomerEntitlementSources` for the canonical id must include observations whose `customer_id` is in `S`. `DISTINCT ON (entitlement, application, product)` already keeps the latest `observed_at`. The projector then sees a renewal written on the canonical id and the older snapshot left on the source id as one application-product source.

`reconciliation_jobs.customer_id` is not rewritten. New schedules use the canonical id returned after resolution. A job row that still names the source id resolves through `merged_into_customer_id` at processing time.

Rows this plan does not update: `purchase_evidence.customer_id`, `purchase_observations.customer_id`, `purchase_observations` snapshot columns, `provider_references`, `observation_references`, `inbox_messages` including ciphertext and fingerprint, `outbox_events.payload` and `aggregate_id` of events already stored, and `customer_entitlements` rows owned by the source customer. The source projection remains the historical current row for that customer id. Later verifies advance the canonical row only.

## Security

- Project scope comes from the admin path or from the application principal. Never from an unbound body field.
- No cross-project merge and no cross-project alias.
- No merge or alias by raw store token.
- Customer sessions receive `401` on every new route.
- Application bearers may add an external id that does not already identify a customer, and may resolve ids inside their own project. They may not merge, and they may not retarget an external id that already resolves elsewhere.
- Bootstrap admin cannot write aliases or merges. There is no durable `actor_key_id`.
- Audit and alias rows are insert-only. Replay returns the stored row.
- Locks use the existing customer write barrier, in id order, at the existing serializable isolation.
- Do not log `provider_references` values. External ids are already stored in plaintext and returned by the admin customer API; do not add a new log field for them. Log project id, canonical id, source id, and merge audit id.
- A merged source stays addressable by its original external id, so a stolen old id still verifies only with a matching store binding. Combining two payers who both have `allowed` on the same entitlement is the case this plan rejects.

## Dashboard

The dashboard may keep using the admin read model only. It must not query PostgreSQL, and this plan does not add a merge or alias form.

`AdminCustomer` in `contracts/openapi/v1.yaml` gains two optional fields:

- `merged_into_customer_id`, nullable, omitted or null when the row is canonical
- `alias_count`, integer, count of `customer_aliases` rows for that customer id

`adminCustomers` in `internal/persistence/postgres/admin.go` keeps listing up to 200 `customers` rows and keeps `entitlement_count`, `allowed_count`, and `last_observed_at` as direct aggregates of that row. Do not redefine `customer_count` on the project summary. Merged sources remain rows.

`renderCustomers` in `dashboard/app.js` already prints `external_id` and internal `id`. When `merged_into_customer_id` is set, show that id in the existing cell. When `alias_count` is non-zero, show the count in that same cell. Leave the add-customer dialog as one internal id and one external id (`dashboard/index.html`).

Update `dashboard/contract_test.go` and the OpenAPI contract test only for the new fields and, once the routes exist, for route registration. The embedded dashboard does not call the new write routes.

Metric meaning of `customer_count` is unchanged. A later overview that counts canonical people instead of customer rows is a separate metric change. This plan keeps the row count.

## Delivery slices

Each slice is one engineer's change, with tests, and is safe to land only after the slices it names. None of them is a `v0.1.0-rc.1` task. Slice 1 is the first schema bump; do not mix it into the release-candidate branch.

1. **Schema, behavior-preserving.** Migration `010`, `LatestVersion = 10`, backfill `source_customer_id = customer_id`, switch the entitlement joins to that column, and write it from `PutEntitlement` as equal to `customer_id`. Tests: `migrate_test.go` up/down on an empty database, down refusal when a fixture has an alias or a differing `source_customer_id`, and one repository assertion that new projections store equal ids. Existing purchase and entitlement tests stay green.

2. **Resolution reads.** `CustomerByExternalID` follows one hop. Direct lookup remains for `PutCustomer`. Alias external ids conflict on `PutCustomer`. Tests in `repository_test.go` with SQL fixtures for alias, merged source, a chain (error), and a cross-project id (`not_found`). No HTTP yet, so production data does not change.

3. **Alias and resolve HTTP.** Both admin and application routes, idempotent replay, the error table above, bootstrap rejection, customer-session rejection, and cross-project `404`. Update `contracts/openapi/v1.yaml` and `internal/transport/httpapi/contract_test.go`. Tests in `api_test.go` and `api_postgres_integration_test.go`.

4. **Closure writes and projection reads.** Evidence and observation idempotency from the previous section, `CustomerEntitlementSources` over the closure, and `source_customer_id` allowed to differ from the projection owner. Tests create `merged_into_customer_id` in SQL. Cover: unchanged snapshot replay does not move `customer_id` and does not return `ErrConflict`; a new fingerprint is stored on the canonical id; an observation outside the closure still conflicts; entitlement GET for the canonical id includes the source observation's allowed access; the source projection row is unchanged. Include a verification race where the customer resolves to canonical between the provider result and `persist`, and assert the job error is not a permanent conflict.

5. **Merge HTTP.** Audit row, lock order, conflict check before `DefaultProjector`, idempotency rules, outbox only when the canonical snapshot changes, no writes to the evidence tables listed above. Tests for dual `allowed` (`entitlement_conflict`, zero audit rows), allowed plus denied (one audit row, canonical projection cites the source observation, source projection untouched), idempotent replay, reused key with a different body, source that already has an alias (`409`), and application bearer (`401`). Integration test that a later Huawei or Google token lookup still builds the binding from the source `external_id` and completes.

6. **Dashboard read model.** The two `AdminCustomer` fields and the cell text. No new controls. Contract test for the overview document.

Do not start slice 5 before slice 4. Slice 3 can follow slice 2 directly. Slice 6 can follow slice 3 and does not need merge to render null `merged_into_customer_id` and `alias_count = 0`.

## Acceptance criteria

- An external id that is not a `customers.external_id` can be attached to one canonical customer in the same project, and repeating the request does not create a second row.
- Resolve reports `primary`, `alias`, or `merged`, and the canonical id, or `404` when the id is unknown in that project.
- Entitlement GET and new purchases for an alias use the canonical `customers.id` as `customer_id` in the API response.
- Merge of two customers with `allowed` on the same entitlement id writes nothing and returns `409 entitlement_conflict`.
- Merge of a non-conflicting pair writes one `customer_merges` row, sets `merged_into_customer_id`, leaves source and canonical `external_id` values in place, and does not change `customer_id` or ciphertext on `purchase_evidence`, `purchase_observations`, `inbox_messages`, or existing `outbox_events`.
- After that merge, entitlement GET for either external id returns the canonical id and one projection. The cited observation id is one that already existed or a newer observation appended under the closure rule.
- A repeated store snapshot after the merge is idempotent and does not fail the worker with permanent `ErrConflict`.
- Store binding checks still require an exact `app_account_token`, `obfuscated_external_account_id`, or `developer_payload` match with the request external id.
- A customer session, a bootstrap admin, and an application bearer cannot merge. An application bearer cannot retarget an existing external id.
- Two projects cannot be combined. Schema 9 remains the `v0.1.0-rc.1` gate.

## Non-goals

- RevenueCat customer, receipt, or entitlement import.
- Consumable delivery or consumption.
- Amazon, Samsung, or any store beyond Apple, Google Play, and Huawei.
- A new public webhook type. Importers use the merge response.
- Unmerge, chain collapse, and an audited override of `entitlement_conflict`.
- Rewriting historical evidence or backfilling store account tokens.
- Mobile SDK methods for alias or merge. Request and response shapes for verify, restore, and entitlement GET stay as they are, except that `customer_id` may be the canonical internal id the server already returns today.
- Changing `DefaultProjector` ranking inside a single customer.
- Making this work a blocker for the three-provider release candidate.

## Contract for later importers

Importers depend on this identity contract and on nothing else in the customer tables:

- Canonical customer id: `customers.id` of the row whose `merged_into_customer_id` is null.
- Alias external ids: `customer_aliases.external_id` for that canonical id, plus `customers.external_id` of every row whose `merged_into_customer_id` is that canonical id. The canonical row's own `external_id` is the primary external id, not an alias row.
- Merge audit id: `customer_merges.id` for the source customer.
- Import calls the alias and merge HTTP APIs in this plan. It does not `INSERT` into `customers`, `customer_aliases`, `customer_merges`, or `customer_entitlements` on its own. A brand-new canonical customer may still be created with the existing `PutCustomer` path. Extra external ids go through the alias route.
- Real store evidence still enters through verification, so evidence, projection, and outbox stay one transaction.

`docs/plans/revenuecat-import.md` is the first importer that depends on this contract. It never calls merge. Until the alias route exists, it refuses alias ids instead of inserting a second customer. Historical RevenueCat access is not store evidence. That plan writes an `import` projection source and does not call `Verify`. The verification rule above applies to Apple, Google, and Huawei observations, not to an export that those stores did not sign.

## Open questions

- Whether `unresolved` against `allowed` should also refuse a merge. This plan treats only dual `allowed` as active conflict, because that is the access flag the schema calls allowed. If unresolved access should also block a merge, decide that before slice 5. The check is one predicate.
- Whether project `customer_count` should ignore merged sources. Left as a row count on purpose.
- Huawei `accountFlag` is unread by this repository. If a current store document shows it is customer identity rather than an account-site flag, that is an adapter change with its own binding kind, not an alias shortcut.
