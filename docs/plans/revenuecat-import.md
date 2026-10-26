# RevenueCat one-way import

- Status: Unnumbered post-v0.1 plan
- Date: 2026-10-26
- Release gate: This plan does not block, and is not part of, the `v0.1.0-rc.1` three-provider release. Do not add it to the release record, the provider runbooks, or the `cmd/iapstack` server, api, worker, or migrate modes.

## Purpose

An operator who already holds their own RevenueCat export can load historical customers and access into one self-hosted IAPStack project. The import is one shot. It is not a sync, and it is not store verification.

`docs/v0.1-scope.md` lists importing RevenueCat customers, receipts, or historical entitlements as an explicit non-goal, and lists automatic alias merging beyond one stable external customer ID as another. Those boundaries stay. This work lands after that gate, as an additive migration and a separate command.

## Outcome

After a successful apply:

- Each selected RevenueCat `app_user_id` is one IAPStack customer, project-scoped, with that string as `external_id`.
- Each mapped, still-relevant grant is a current entitlement projection whose durable source kind is `import`.
- The public entitlement body stays the v1 object in `contracts/openapi/v1.yaml`: `key`, `access`, `reason`, optional effective interval, and `version`. It has no `verified_at` and it does not say the store was checked.
- A second run of the same export does not create another customer, another grant, another projection version, or another webhook.
- No RevenueCat secret is written to PostgreSQL, logs, or the report.

## Assumptions

- The operator already possesses the export. The command reads a local file. It does not search RevenueCat, and it does not accept another party's customer list.
- The string the app will later send as `external_customer_id` is the RevenueCat `app_user_id` chosen below. IAPStack does not rewrite it. Apple verification requires that string to be the StoreKit `appAccountToken` (`docs/api-v1.md`). Google Play requires the same string as `obfuscatedAccountId`. Huawei requires it as `developerPayload`. If those strings differ, a later real purchase attaches to a different customer. The importer reports that risk; it does not fix it.
- Catalog entitlements, products, and store-product mappings already exist. The importer does not create them.
- `docs/plans/customer-migration-alias.md` may not exist. Until the alias port below is implemented, alias identities are refused.

## Inputs

Two file shapes. Both are allowlisted. Unknown columns and unknown JSON fields are discarded and never logged. A cell named like a receipt, purchase token, attribute blob, or price is not persisted.

### `transactions-csv`

RevenueCat's Scheduled Data Exports transactions feed, plain CSV or gzip CSV. Column meanings below were read from [Scheduled Data Exports](https://www.revenuecat.com/docs/integrations/scheduled-data-exports). Dates in that feed are UTC.

| Column | Use |
| --- | --- |
| `rc_original_app_user_id` | Candidate canonical id, or an alias, per the mapping file |
| `rc_last_seen_app_user_id_alias` | The other id. Same rule |
| `product_identifier` | Store product id. Not an IAPStack product id |
| `entitlement_identifiers` | RevenueCat entitlement ids unlocked by the row |
| `start_time` | Effective period start |
| `end_time` | Store period end. Not the access boundary by itself |
| `grace_period_end_time` | Grace end, when set |
| `effective_end_time` | Access boundary. The feed defines this as the single expiration and revocation point, including refunds and grace |
| `store` | Origin label. Verified examples are `app_store`, `play_store`, `stripe`, and `promotional` |
| `is_auto_renewable` | Distinguishes an open-ended non-subscription from a subscription missing its end |
| `is_sandbox` | `true` is sandbox. `false` is production. There is no third value |
| `store_transaction_id` | Lineage material. Fingerprinted. Not stored raw |
| `original_store_transaction_id` | Stronger lineage material when present. Fingerprinted. Not stored raw |
| `refunded_at` | Forces denial when set |
| `unsubscribe_detected_at` | Cancellation with access until the effective end |
| `billing_issues_detected_at` | Billing-issue reason |
| `ownership_type` | `PURCHASED` or `FAMILY_SHARED`. Not a second customer |
| `auto_resume_time` | Play pause hint |
| `updated_at` | Recency when several rows share one lineage |
| `is_trial_period` | Kept only as a report count. Not a separate product kind |

There is no `app_user_id` column on this feed. The mapping file must set `canonical_id_column` to exactly one of `rc_original_app_user_id` or `rc_last_seen_app_user_id_alias`. That column is the IAPStack external id. The other column, when different and non-empty, is an alias identity.

### `subscriber-jsonl`

One JSON object per line:

```json
{"app_user_id":"user-1456","subscriber":{}}
```

`app_user_id` is required on the envelope. It becomes `external_id`. The public Customer Info schema does not put `app_user_id` inside `subscriber`; the fetch wrapper or the operator's export must supply the id that was queried. Do not substitute `original_app_user_id` when the envelope id is missing.

Subscriber fields read from [Customer Info Model](https://www.revenuecat.com/docs/api-v1/customer-info-model):

- `original_app_user_id`
- `entitlements.<id>.expires_date`, `grace_period_expires_date`, `product_identifier`, `purchase_date`
- `subscriptions.<product_identifier>` including `store`, `is_sandbox`, `expires_date`, `grace_period_expires_date`, `purchase_date`, `original_purchase_date`, `refunded_at`, `unsubscribe_detected_at`, `billing_issues_detected_at`, `ownership_type`, `store_transaction_id`, `auto_resume_date`
- `non_subscriptions.<product_identifier>[]` including `id`, `is_sandbox`, `purchase_date`, `store`

Join each entitlement to the subscription or non-subscription with the same `product_identifier`. If that identifier appears in both collections, refuse the subscriber. If it appears in neither, refuse that entitlement.

`request_date` on a wrapped v1 document is the observation time for those grants. It is not a verification time.

## Customers

Customers are project-scoped. `internal/persistence/postgres/migrations/001_catalog.sql` makes `(project_id, external_id)` unique. `PutCustomer` in `internal/persistence/postgres/control.go` inserts or returns the existing row. The importer uses that. It does not insert a second row when the external id already exists, even if the internal id it proposed differs. The stored id wins.

Proposed internal ids are deterministic: `cust-` plus the first 16 bytes of SHA-256 over `project_id`, a separator, and the canonical external id, hex-encoded. A rerun proposes the same id. An older admin-created customer with the same external id is reused, not duplicated.

The external id must pass `core.ValidateExternalCustomerID` (`internal/core/identifiers.go`): non-empty, valid UTF-8, no leading or trailing whitespace, no control characters. RevenueCat's own length advice is not an IAPStack limit. Do not truncate. `$RCAnonymousID:` values are legal identifiers. They are app user ids, not aliases. Import them. `--skip-anonymous` drops ids with that prefix and counts them. Default is to import.

### Alias contract

This plan does not implement merging. It depends on a port that a later alias plan can satisfy:

```text
CreateAlias(project_id, canonical_external_id, alias_external_id) error
```

`docs/plans/customer-migration-alias.md` is the HTTP shape of that port: `PUT /v1/admin/projects/{project_id}/customers/{customer_id}/aliases/{external_id}` with an empty body. `customer_id` is the internal canonical id. The importer resolves the canonical external id through the existing customer read, then calls that route. It never calls `POST /v1/admin/projects/{project_id}/customer-merges`. Merge stays an explicit admin action for two customers who already have grants. An import must not combine them.

`CreateAlias` requirements:

- It does not insert a customer.
- It resolves the alias external id to the canonical customer inside the same project.
- Repeating the same pair is a no-op.
- If `alias_external_id` is already a customer, or is already an alias of a different canonical id, it returns a conflict.

If the alias route is absent:

- Do not insert a customer for the alias id.
- Still import the canonical customer's mapped grants.
- Count the identity as `alias_refused`.
- If that alias id is already some other IAPStack customer's `external_id`, refuse the whole subscriber with `conflicting_subscriber` and write nothing for that subscriber. Attaching the grants only to the canonical id would split one person.

A JSON `aliases` array, when present, is a list of alias identities. The Customer Info schema that was opened does not document that field; webhook text does, at [Identifying Customers](https://www.revenuecat.com/docs/customers/identifying-customers). Honor the field when it is present. Do not invent aliases from thin air.

`original_app_user_id` that differs from the canonical `app_user_id` is one alias identity. It is not a second customer.

## Entitlements and products

Map RevenueCat entitlement identifiers to existing IAPStack entitlement keys. Map `(store, environment, provider_product_id)` to an existing application id and product id. Look up the key in `entitlements` and the product in `products` and `product_entitlements` for the same project. For `app_store` and `play_store`, also require the existing `store_products` row for that application and provider product id.

Do not insert projects, applications, entitlements, products, or store-product rows. A missing key is `unknown_entitlement`. A missing product or store-product row is `unknown_product`. A product that does not grant the mapped entitlement is `entitlement_not_granted`. A product whose kind is `consumable` is `unsupported_product_kind`. v0.1 does not deliver consumables, and this import does not start that work.

`stripe` and `promotional` are not IAPStack providers (`internal/core/catalog.go`). They can still produce an import grant when the mapping names an existing internal product and an existing application that owns that product. The grant's origin store stays the RevenueCat string. It does not become `core.Provider`, and it does not create an application. Map a promotional grant onto its own internal product if a later store denial for the paid product must not clear it. Mapping it onto the paid product means a later observation for that same application and product replaces it.

`FAMILY_SHARED` does not create a customer. The grant follows the same access rules as a purchased row. The report counts these rows. IAPStack ownership on a real observation stays orthogonal to access (`internal/core/purchase.go`); the import grant does not pretend to be that observation.

One transaction can list several entitlement identifiers. Each mapped identifier is its own grant. Several store products that grant the same IAPStack entitlement stay several grants. The projector still emits one current projection per entitlement.

## Historical access

An imported grant is not a purchase observation.

`verification.Service.Verify` and `Reconcile` (`internal/verification/service.go`) call a store adapter, protect evidence, and insert `purchase_evidence` and `purchase_observations`. `customer_entitlements.source_observation_id` references those observations (`internal/persistence/postgres/migrations/003_entitlement_projections.sql`). `CustomerEntitlementSources` builds candidates only from `purchase_observations` (`internal/persistence/postgres/entitlement.go`). Putting a RevenueCat row through that path would describe an export as store-verified. Do not do it. Do not call `Verify` with a synthetic receipt. Do not insert `purchase_evidence`, `verified_artifacts`, `purchase_observations`, or `provider_references` for this import.

ADR-0002 keeps store payloads behind adapters. ADR-0005 says a normalized observation is verified provider evidence and that access on an observation is not itself the entitlement projection. The import writes the projection source directly, with source kind `import`.

### Access decision

Evaluate each grouped lineage at the command clock. Group key is canonical external id, mapped application, mapped product, mapped entitlement, and lineage fingerprint. The fingerprint is SHA-256 of the original store transaction id when that column or field is present, otherwise the store transaction id, otherwise the subscriber non-subscription `id`, otherwise `product_identifier` plus purchase date. Never persist the raw id. Among rows in one group, the greatest `updated_at` wins, then the greatest `effective_end_time`, then the greatest `start_time`.

Transactions feed:

- `refunded_at` set: `denied` / `refunded`. Do this even if `effective_end_time` is still in the future. The feed says `refunded_at` is not set for App Store upgrade partial refunds, so a set value is a real refund. Record `refund_end_disagreement` on the report when the effective end is still future, and still deny.
- `effective_end_time` null and `is_auto_renewable` false: durable grant. `allowed` / `purchase_valid`. No end. The mapped product must be `non_consumable`. A subscription product here is `product_kind_mismatch`.
- `effective_end_time` null and `is_auto_renewable` true: `missing_effective_end`. Do not grant an open subscription.
- Clock at or after `effective_end_time`: `denied`. Reason `paused` when `auto_resume_time` is after the clock, else `billing_issue` when `billing_issues_detected_at` is set, else `expired`.
- Clock before `effective_end_time`: `allowed`. Reason `grace_period` when `grace_period_end_time` is after the clock and `end_time` is null or the clock is at or after `end_time`. Else `canceled_at_period_end` when `unsubscribe_detected_at` is set. Else `purchase_valid`. End is `effective_end_time`.
- `end_time` before `start_time` is legal on the feed for an invalid Play transaction. Do not grant from that pair. Use `effective_end_time`. If that is also missing, `missing_effective_end`.

Subscriber JSON uses the same reasons. The access boundary is `grace_period_expires_date` when it is strictly after `expires_date`, otherwise `expires_date`. A null `expires_date` is durable only when the matched purchase is a non-subscription and the mapped product is `non_consumable`. A null end on a subscription object is `missing_effective_end`.

Allowed grants require a start. Use `start_time` or `purchase_date`. A missing start on an allowed grant refuses that grant.

Do not add an access reason named `import`. The `customer_entitlements` check constraint only allows the existing reasons. Source kind carries the import fact.

### How long it lasts

The stored `access` is the decision at import time. `effective_ends_at` is the boundary above, or null for a durable non-consumable.

`entitlementResponses` in `internal/transport/httpapi/api.go` already denies an `allowed` projection at `effective_ends_at` without writing a new version. `TestEntitlementResponsesFailClosedAtEffectiveEnd` covers that. Imported subscriptions use the same read path. No expiry worker is added. No `entitlement.changed` webhook is emitted merely because the clock passed the end. That matches store-backed projections today.

A durable grant stays `allowed` with a null end until a later import of the same lineage changes it, or a real store observation for that same application and product supersedes it.

### What a later store notification does

`internal/verification/projector.go` selects one candidate per entitlement. Source identity today is `(application_id, product_id)`. Extend candidates with source kind `store_observation` or `import`.

Rank inside one `(application_id, product_id)`:

1. A store observation beats an import grant, even when the grant's `observed_at` is later.
2. Same kind: keep today's rule. Later `observed_at` wins. A tie keeps the smaller source id.

Across different products that grant one entitlement:

- An import never replaces a current projection whose source kind is `store_observation` when that store source is still present and its access is equal or stronger.
- An allowed store observation may replace a current import, including when observed times would otherwise keep the import. Do not apply the "strengths are equal, keep the current source" shortcut when the current source is an import and the challenger is a store observation.
- A denied store observation for a different product does not revoke an allowed import. `preserveIndependentSource` still protects that case.
- A store observation for the same application and product replaces the import even when the observation denies access. The customer loses the imported allowance for that product.

`verification.Service.persist` already reloads sources after it saves observations and then projects. Include import grants in that load. The import command uses the same projector. It does not call `PutEntitlement` for a row the projector would not select.

`observed_at` on a grant is the export's `updated_at` or the subscriber `request_date`. It is not `VerifiedAt`. Logs and reports say `import`, never `verified`.

## Re-verification is a separate slice

The import does not call Apple, Google, or Huawei. A RevenueCat receipt is not evidence for those adapters.

A later optional command may re-check still-active subscriptions only through the existing adapters:

- Apple can be reconciled from an original transaction id by the App Store adapter. That slice must read the protected or operator-supplied id at execution time, call the existing adapter, and persist a normal observation. The projector then supersedes the import. The grant row remains an import grant.
- Google `store_transaction_id` on this feed is an order id. The verified column list has no purchase token. `internal/stores/googleplay` queries by purchase token. Do not pass an order id to that adapter. Google re-verification waits for a client restore through `purchases:verify`.
- No Huawei store value was verified on the transactions feed. Do not guess one. Unmapped store strings fail closed unless the mapping file names them.

That slice is not required for this plan's acceptance, and it must not be described as something the import already did.

## Idempotency

Parse the whole file before the first write. Hash the uncompressed bytes with SHA-256. A truncated gzip stream, a short read, or an unfinished CSV record fails the parse. Exit without writes.

`--batch-id` is required on apply. Persist one batch row per `(project_id, batch_id)` with that hash. The same id and the same hash resumes. The same id and a different hash is `batch_conflict` and writes nothing.

Apply one customer per database transaction: lock the customer, upsert grants, load observation sources and import grants, project, `PutEntitlement` only for selected projections. `PutEntitlement` leaves `Changed` false when the logical projection is unchanged (`internal/persistence/postgres/entitlement.go`), and the version stays. The outbox write in `verification.Service.persist` runs only when `Changed` is true. `SaveOutboxEvent` is also fingerprint-idempotent (`internal/persistence/postgres/outbox.go`). A second apply of the same export therefore adds no projection version and no webhook.

Absence is not deletion. A customer or grant missing from a later file is left in place. A partial export must not expire everyone it does not mention.

A crash after some customers commit is resumed by rerunning the same batch and the same file. Committed customers become unchanged.

Webhooks default off (`--webhooks=false`). Turning them on for a rerun does not backfill events for projections that did not change. Operators who want one event per real change pass `--webhooks` on the first successful apply.

## Secrets

The RevenueCat secret is an operator secret. The default command does not read it, because a local export needs no network.

Do not store it in PostgreSQL. Envelope encryption through `internal/protection` and `internal/credentials` is for data the service must open later. This key is not such data. There is no credential kind, no dashboard field, and no migration column for it.

Optional fetch, off unless requested:

```text
REVENUECAT_SECRET_API_KEY
```

Read that from the environment only. Never from a flag, a mapping file, or the export. Do not log it, do not put it in the report, and do not include `Authorization` in errors. The process holds it in memory for the GET calls and then exits.

Fetch accepts only `--subscriber-ids-file`, one id per line, ids the operator already has. It calls `GET https://api.revenuecat.com/v1/subscribers/{app_user_id}` and writes `subscriber-jsonl` to a path the operator names. [Customers](https://www.revenuecat.com/docs/api-v1/customers) states that this GET creates a customer when the id does not exist. A typo therefore creates a RevenueCat customer. The command prints that fact before the first request and requires `--acknowledge-get-creates-customers`. It does not page a project, expand a prefix, or generate ids. `401` stops the run. `429` and `5xx` retry with bounded jitter and then fail the id. Fetch output is an input file. It is not an apply.

## Command

New module command `cmd/iapstack-revenuecat-import`. Do not add a mode to `cmd/iapstack`. `app.Run` accepts only `server`, `api`, `worker`, and `migrate`. This command opens PostgreSQL itself on apply. It does not call `config.Load`, so unused server settings are not required.

Dry-run, the default:

```text
iapstack-revenuecat-import \
  --project project-1 \
  --mapping ./revenuecat-mapping.json \
  --export ./transactions.csv.gz \
  --format transactions-csv \
  --now 2026-10-26T00:00:00Z \
  --report ./import-report.json
```

Apply:

```text
iapstack-revenuecat-import \
  --project project-1 \
  --mapping ./revenuecat-mapping.json \
  --export ./transactions.csv.gz \
  --format transactions-csv \
  --batch-id revenuecat-2026-10-26 \
  --apply \
  --webhooks=false
```

`--apply` requires `IAPSTACK_DATABASE_URL`. Dry-run does not. `--now` defaults to the process clock in UTC and exists so tests and dry-runs are repeatable. `--format` is `transactions-csv` or `subscriber-jsonl`.

Exit `0` when the file is valid and every subscriber is imported or intentionally skipped. Exit `2` for usage errors, a bad mapping, a truncated file, or a batch hash conflict, always with zero writes. Exit `1` when the file parsed and at least one subscriber was refused. Earlier subscribers in that run may already be committed. A rerun is safe.

### Mapping file

JSON object, `additionalProperties` false. The file contains no secrets and no export rows.

```json
{
  "project_id": "project-1",
  "canonical_id_column": "rc_last_seen_app_user_id_alias",
  "entitlements": {
    "pro_cat": "premium"
  },
  "products": [
    {
      "store": "app_store",
      "environment": "production",
      "provider_product_id": "com.example.annual",
      "application_id": "application-apple",
      "product_id": "product-annual"
    },
    {
      "store": "play_store",
      "environment": "sandbox",
      "provider_product_id": "annual",
      "application_id": "application-play-sandbox",
      "product_id": "product-annual"
    },
    {
      "store": "promotional",
      "environment": "production",
      "provider_product_id": "rc_promo_pro_cat_monthly",
      "application_id": "application-apple",
      "product_id": "product-promo"
    }
  ]
}
```

`canonical_id_column` is required for `transactions-csv` and forbidden for `subscriber-jsonl`. `environment` is `production` or `sandbox` and must equal the configured application's environment. `is_sandbox` true with a production application is `environment_mismatch`. The reverse is the same failure. Do not map sandbox onto `test`.

`project_id` in the file must equal `--project`.

### Report

One JSON object, stdout or `--report`. Secret-free means no API key, no authorization header, no receipt, no purchase token, no raw store transaction id, no subscriber attribute, no price, and no IP.

Include `dry_run`, `project_id`, `batch_id` when set, `export_sha256`, `format`, and counts: `customers_created`, `customers_reused`, `grants_upserted`, `grants_unchanged`, `projections_changed`, `webhooks_enqueued`, `alias_refused`, `anonymous_customers`, `family_shared`, `skipped_anonymous`, `failed`.

Failures are `{line, code, external_id, entitlement_key}`. `external_id` is the operator's own id, so they can fix the mapping. The report is still personal data. Do not commit it. Lineage appears only as `lineage_fingerprint`, hex SHA-256.

## Failures

| Code | Meaning | Writes for that subscriber |
| --- | --- | --- |
| `missing_app_user_id` | Envelope or canonical column empty | None |
| `invalid_external_id` | Fails `ValidateExternalCustomerID` | None |
| `alias_refused` | Alias id not stored. Canonical grants still proceed unless the next row applies | Alias only |
| `conflicting_subscriber` | Alias id is already another customer, or two canonical ids claim one alias | None |
| `unknown_entitlement` | RevenueCat entitlement id is not in the mapping | None |
| `unknown_product` | No mapping, or the mapped product or store-product row is absent | None |
| `entitlement_not_granted` | Mapped product does not grant the mapped entitlement | None |
| `unsupported_product_kind` | Consumable | None |
| `product_kind_mismatch` | Durable row mapped to a subscription, or the reverse | None |
| `environment_mismatch` | `is_sandbox` does not match the application | None |
| `unsupported_store` | Store string has no mapping entry | None |
| `missing_effective_end` | Renewable row without an access boundary | None |
| `invalid_entitlement_list` | `entitlement_identifiers` is not a JSON array | None |
| `batch_conflict` | Batch id reused with a different file hash | None for the run |
| `truncated_export` | Gzip or CSV did not end cleanly | None for the run |

One bad grant fails that subscriber and rolls back only that subscriber's transaction. The rest of the file continues. Fix the mapping and rerun the same batch.

`invalid_entitlement_list` is deliberate. The feed types the column as a string array. This plan accepts only a JSON array such as `["premium"]`. Other serializations are not guessed.

## Persistence

Add one tern migration after the current chain. The chain today ends at `internal/persistence/postgres/migrations/009_purchase_prices.sql`. Do not renumber shipped migrations. The migration is up and down, like the others.

`import_grants`:

- `id` text primary key, `imp-` plus 32 hex characters from the lineage fingerprint and the mapped ids
- `project_id`, `customer_id`, `application_id`, `product_id`, `entitlement_id`
- `origin` constant `revenuecat`
- `origin_store` non-empty text, not a `core.Provider`
- `environment` `production` or `sandbox`
- `access_status`, `access_reason`, `effective_starts_at`, `effective_ends_at` using the same checks as `customer_entitlements`
- `observed_at` non-null
- `export_sha256` bytea length 32
- `lineage_fingerprint` bytea length 32
- unique `(project_id, customer_id, application_id, product_id, entitlement_id, lineage_fingerprint)`
- foreign keys into `customers`, `applications`, `products`, and `product_entitlements` for the same project
- no receipt column and no raw transaction column

`customer_entitlements`:

- add `source_kind` text not null default `store_observation`, check `store_observation` or `import`
- add nullable `source_import_id` referencing `import_grants(id)`
- make `source_observation_id` nullable
- exactly one source id is set, and it matches `source_kind`
- PostgreSQL `MATCH SIMPLE` ignores the observation foreign key when `source_observation_id` is null. Keep that foreign key for store rows
- existing rows stay `store_observation` through the default

`CustomerEntitlements` and `loadCustomerEntitlement` inner-join `purchase_observations` to find `application_id`. That join drops import rows. Read `application_id` from the observation or from the import grant, matching `source_kind`. `CustomerEntitlementSources` unions the latest import grant per `(entitlement, application, product)` with the existing observation query.

Dashboard overview reads `purchase_observations` (`internal/persistence/postgres/admin.go`). Leave that query alone. Import grants must not appear as recent verified transactions. No dashboard change in this plan.

Outbox, only when `--webhooks=true` and `PutEntitlement` reports a change:

- store-backed events stay schema version 1, as `newEntitlementEvent` writes them today
- import-backed events are schema version 2
- version 2 sets `source` to `import` and `source_import_id` to the grant id
- version 2 omits `source_observation_id`
- version 2 does not include `verified_at`

`sdk/go` `EntitlementChange.validate` rejects an empty `source_observation_id`. Shipping version 2 without an SDK change makes current decoders fail closed, which is preferable to labeling a grant as an observation. The webhook slice updates the Go decoder, and the other SDK decoders that share this payload, to accept version 2 only when `source` is `import`. Version 1 stays unchanged. Until that slice lands, the command's default remains `--webhooks=false`.

Do not put the grant id in `source_observation_id`. The field name would describe a purchase observation.

## Tests

Fixtures under the importer package. Synthetic ids only. No test dials `api.revenuecat.com`. The fetch client, if built, talks to `httptest` only.

Parser and mapping, no database:

- canonical column chosen, the other id not emitted as a customer
- missing alias port yields `alias_refused` and still emits the canonical grant
- alias id that collides with another canonical id yields `conflicting_subscriber`
- unknown entitlement, unknown product, consumable, sandbox row pointed at a production application
- expired subscription denied, future end allowed, null end on a non-renewing row durable, null end on a renewable row refused
- `refunded_at` denies
- truncated gzip and a quoted CSV record cut mid-line produce `truncated_export` and an empty write set
- a fixture column of subscriber attributes and a fixture receipt column never appear in the decoded grant
- entitlement cell `['premium']` is `invalid_entitlement_list`; `["premium"]` maps

Projector, in `internal/verification/projector_test.go`:

- an older store observation beats a newer import grant for the same application and product
- a denied store observation for that same product replaces an allowed import
- an allowed import does not replace an independent allowed store source
- a denied store source on another product does not clear an allowed import

PostgreSQL integration:

- apply once, record customer count, grant count, projection version, outbox count
- apply the same file and batch again, assert those versions and the outbox count are unchanged
- a second export that only moves `effective_end_time` changes the grant and, with webhooks on, enqueues one event whose body has `source` `import` and no `source_observation_id`
- a following real observation for that application and product, inserted through the existing verification path, becomes the current projection and the import grant remains stored but not selected
- `GET` entitlements for an imported subscription whose end is in the past returns `denied` / `expired` without a new version
- admin recent-observation query does not list the grant

## Delivery slices

Each slice is independently mergeable and leaves the three-provider gate red/green state untouched.

1. Parser, mapping validation, dry-run report, fixture tests. No migration and no database writes.
2. Migration, grant repository, projector rank, customer read path, `--apply` with webhooks forced off. Integration tests for idempotency and supersession.
3. Opt-in schema version 2 webhook and the SDK decode change. Default remains off.
4. Optional explicit-id fetch against the v1 subscriber URL. Still no database storage of the key.
5. Optional Apple re-verify command that calls the existing App Store adapter and then relies on slice 2's rank. Not part of slices 1–4.

## Acceptance

- A dry-run of a fixture export writes no database row and a secret-free report.
- Apply creates one customer per canonical `app_user_id` and no customer for a non-canonical alias.
- With the alias port missing, alias identities are refused and are not extra customers.
- Entitlement keys and products that are not already in the catalog are refused. Nothing is auto-created from a store product id.
- Imported current access is a projection with source kind `import`. No new `purchase_observations` row exists for it. The public entitlement JSON does not call it store-verified.
- A subscription grant carries `effective_ends_at`. The existing read path denies it at that instant. A non-consumable grant has a null end and stays allowed.
- After a real observation is stored for the same application and product, the current projection cites that observation, including when the observation denies access.
- Reapplying the same export and batch id does not change entitlement versions or outbox cardinality.
- The process can run with no `REVENUECAT_SECRET_API_KEY`. A run that has the key does not persist it.
- `cmd/iapstack`'s usage string is unchanged. No release workflow gains a RevenueCat job.

## Non-goals

- A continuing RevenueCat sync, RevenueCat webhooks, or periodic pull
- Importing paywalls, offerings, packages, or copy
- Importing proceeds, tax, commission, prices, experiments, or country
- Importing subscriber attributes, advertising ids, or IP addresses
- Creating catalog records from the export
- Implementing alias merge, or calling `Merge`
- Consumable fulfillment
- Treating the export as Apple, Google, or Huawei verification
- Google or Huawei re-verification in the import command
- Dashboard changes, public OpenAPI changes on the v1 entitlement object, and any edit to the `v0.1.0-rc.1` gate
- Storing the RevenueCat secret, with or without envelope encryption

## Decisions left open

- Alias merge of two existing customers is specified in `docs/plans/customer-migration-alias.md`. This importer does not call it.
- Whether `source=import` is also an analytics event is outside this plan. The outbox payload above is the contract if webhooks are enabled.
- No operator screen is required. Unresolved aliases stay in the secret-free report.

## Open questions

- The transactions page types `entitlement_identifiers` as a string array but does not spell the CSV encoding. Only JSON arrays are accepted until a real file shows otherwise.
- The Customer Info schema that was opened has `original_app_user_id` and no `aliases` field. Webhook documentation does. The importer handles an `aliases` array when the file contains one, and does not require it.
- Store values confirmed on the transactions feed are `app_store`, `play_store`, `stripe`, and `promotional`. The same page also says `app_store`, `play_store`, and so on. Any other string, including a Huawei value, stays `unsupported_store` until a mapping entry names it.
- The verified transactions columns do not include a Google purchase token. This plan does not invent a column for one.

## Sources read

IAPStack:

- `docs/v0.1-scope.md`
- `docs/adr/0002-store-neutral-core.md`
- `docs/adr/0005-provider-neutral-purchase-observations.md`
- `docs/adr/0006-scoped-authenticated-data-protection.md`
- `docs/adr/0007-application-scoped-provider-credentials.md`
- `docs/api-v1.md`
- `contracts/openapi/v1.yaml` (`Customer`, `Entitlement`, `EntitlementSnapshot`)
- `internal/core/catalog.go`
- `internal/core/identifiers.go`
- `internal/core/purchase.go`
- `internal/verification/service.go`
- `internal/verification/projector.go`
- `internal/persistence/contracts.go`
- `internal/persistence/postgres/control.go`
- `internal/persistence/postgres/entitlement.go`
- `internal/persistence/postgres/migrations/001_catalog.sql`
- `internal/persistence/postgres/migrations/002_purchase_evidence.sql`
- `internal/persistence/postgres/migrations/003_entitlement_projections.sql`
- `internal/transport/httpapi/api.go`
- `internal/transport/httpapi/entitlement_test.go`
- `sdk/go/models.go`
- `cmd/iapstack/main.go`
- `internal/app/app.go`

RevenueCat pages opened:

- [Customer Info Model](https://www.revenuecat.com/docs/api-v1/customer-info-model)
- [Customers](https://www.revenuecat.com/docs/api-v1/customers)
- [Identifying Customers](https://www.revenuecat.com/docs/customers/identifying-customers)
- [Scheduled Data Exports](https://www.revenuecat.com/docs/integrations/scheduled-data-exports)
