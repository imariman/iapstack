# Subscription metrics

- Status: Implemented on `feat/subscription-metrics`, awaiting review
- Date: 2026-11-18
- Release: Unnumbered post-v0.1 plan

This plan closes gap rows E1 (subscription health) and E2 (trials and conversion) from the comparison with RevenueCat, Adapty, Qonversion, and Superwall. It is not part of the `v0.1.0-rc.1` release gate. Migration `014_subscription_offer_phase.sql` moves `LatestVersion` in `internal/persistence/postgres/migrate.go` from 13 to 14, so a version-13 binary refuses to boot against a migrated database.

## Invariant

Every figure is a count of stored, provider-signed purchase observations. Nothing is projected past the generation time. A signal the stored evidence does not carry is stated per provider, never shown as zero. The panel and the endpoint carry no money figure.

## Verified facts: this repository

- `internal/core` had no trial, introductory, or offer-type field before this change. `PurchaseObservation` had state, access, renewal, period, price, and references only.
- `purchase_observations` rows are immutable snapshots. `refreshObservationTime` in `internal/persistence/postgres/purchase.go` moves `observed_at` forward with `GREATEST` each time an identical snapshot is seen again, so `observed_at` is the last sighting. `created_at` is the first time the row was stored.
- `observationLineageSQL` in `internal/persistence/postgres/entitlement.go` derives one lineage key per observation: the `lineage` reference (Apple `original_transaction_id`, Google Play `purchase_token`), or the Huawei `query` `purchase_token`, which stays stable across Huawei renewals.
- Transaction references are Apple `transaction_id`, Google Play `order_id` (`latestSuccessfulOrderId`, else `latestOrderId`), and Huawei `order_id`.
- Google Play subscription observations set `EffectivePeriod.StartsAt` to the subscription `startTime`, which does not change at renewal. Apple uses the transaction `purchaseDate` and Huawei the order `purchaseTime`.
- Provider reference fingerprints are scoped by purpose `provider_reference:<role>:<kind>` (`internal/verification/service.go`). SQL therefore cannot match an Apple transaction id to its original transaction id, or a Google Play `linkedPurchaseToken` to the earlier lineage token.
- `processing.reconciliationInterval` is 24 hours. Subscriptions are re-read from the provider about daily, so a missed notification is usually observed within a day.
- RevenueCat imports are `import_grants` rows (migration 012), not observations.
- Google Play subscription observations store no price. Apple stores the signed transaction `price` (milliunits). Huawei stores `price` × 10.

## Verified facts: providers

Apple [`JWSTransactionDecodedPayload`](https://developer.apple.com/documentation/appstoreserverapi/jwstransactiondecodedpayload), read for this plan:

- [`offerType`](https://developer.apple.com/documentation/appstoreserverapi/offertype): 1 introductory, 2 promotional, 3 offer code, 4 win-back, 5 retention. Optional.
- [`offerDiscountType`](https://developer.apple.com/documentation/appstoreserverapi/offerdiscounttype): `FREE_TRIAL`, `PAY_AS_YOU_GO`, `PAY_UP_FRONT`, `ONE_TIME`.
- `offerIdentifier`, `offerPeriod`, and [`transactionReason`](https://developer.apple.com/documentation/appstoreserverapi/transactionreason) (`PURCHASE`, `RENEWAL`; an upgrade is `PURCHASE`, a downgrade takes effect as `RENEWAL`).
- The page says `price` and `currency` are not for revenue reconciliation or recognition.
- The adapter decodes the JWS payload with a non-strict `json.Unmarshal`, so these fields were present and ignored before this change.

Google Play [`purchases.subscriptionsv2`](https://developers.google.com/android-publisher/api-ref/rest/v3/purchases.subscriptionsv2), read for this plan:

- `SubscriptionPurchaseLineItem.offerDetails`: `basePlanId`, `offerId` (discounted offers only), `offerTags`.
- `SubscriptionPurchaseLineItem.offerPhase`: a union of `basePrice`, `freeTrial`, `introductoryPrice`, and `prorationPeriod` (with `originalOfferPhaseType`).
- `autoRenewingPlan.recurringPrice` excludes discounts and taxes. IAPStack does not store it.
- `canceledStateContext`, present for canceled and expired subscriptions, is a union of `userInitiatedCancellation`, `systemInitiatedCancellation`, `developerInitiatedCancellation`, and `replacementCancellation`. The adapter does not decode it.

Huawei: the official `InAppPurchaseData` reference renders only as a navigation index, so its field table was not read. A third-party Go client documents `trialFlag` and `introductoryFlag` (0 or 1) and cites Huawei's server data model. That is not a verified contract, and no captured Huawei response in this repository carries either field. Huawei offer phase is not mapped.

## Offer phase

`core.OfferPhase` is an optional normalized field on `PurchaseObservation`, stored in `purchase_observations.offer_phase`. NULL means not recorded. A CHECK limits it to subscriptions and to the values below. ADR-0005 allows a new optional normalized field for a cross-provider semantic; every provider here has one.

| Phase | Apple | Google Play |
| --- | --- | --- |
| `base` | no `offerType` | `basePrice` |
| `free_trial` | `offerDiscountType` `FREE_TRIAL` with any `offerType` | `freeTrial` |
| `discounted` | `PAY_AS_YOU_GO`, `PAY_UP_FRONT`, `ONE_TIME` | `introductoryPrice` |
| `proration` | none | `prorationPeriod` |
| `unknown` | an `offerType` with an absent or unrecognized payment mode | a union with no known member |
| not recorded | non-subscription | no `offerPhase` in the response |

`stores.RecordsOfferPhase` names Apple and Google Play. Huawei, Amazon, and Samsung are not available.

Persistence inserts the phase with a new snapshot and fills a NULL phase on an identical snapshot (`enrichObservationOfferPhase`). The phase never takes part in `observationMatches`: Apple identity covers the transaction id and Google Play identity covers the order id and expiry, and an analytics field must not turn an identical access snapshot into a conflict. The first stored phase is kept.

## Unit and scope

A subscription is one lineage of a `subscription` product in one application of the selected environment (`production`, `sandbox`, or `test`; default `production`). Family-shared observations are not counted. Import grants are not counted. Lineages are counted per application, so the same customer on two applications is two subscriptions.

## Window

`window_days` is 1 to 90 (default 30) whole UTC days ending today. `window_start` is the first midnight. `window_end` is the midnight after today. `measured_at` is the generation time, which is before `window_end`. Every end-of-window figure is evaluated at `measured_at`.

## Definitions

- **Allowed period.** A snapshot with access `allowed` and both period bounds. If the same lineage has a `refunded` or `revoked` snapshot with access `denied` for the same transaction, stored after the period started, the period ends at that snapshot's `created_at`. The observation does not keep the provider's revocation date.
- **Active at an instant T.** Some allowed period of the lineage has start ≤ T < end. Free trials, billing grace, and canceled-at-period-end periods are active while they are allowed. The product at T is the covering period with the latest start.
- **Active at start / active now.** Active at `window_start` / at `measured_at`.
- **In a free trial now.** Active now, and the covering period's phase is `free_trial`. Counted only where the phase is recorded.
- **Active during the window.** Some allowed period starts before `measured_at` and ends after `window_start`.
- **New subscription.** The lineage's earliest stored allowed period starts in [`window_start`, `measured_at`), the lineage has no `linked_lineage` reference (Google Play upgrade, downgrade, or resubscribe), and no other lineage of the same canonical customer in the same application has a period that started before and ends at or after that start. Trials count as new.
- **Renewal.** A provider transaction first stored in the window, in a lineage that already had an earlier stored transaction. The first paid period after a free trial is a renewal. The first transaction IAPStack ever saw in a lineage is never a renewal.
- **Ended.** The lineage's last allowed period ends after `window_start` and at or before `measured_at`.
- **Replaced.** Ended, and another lineage of the same canonical customer and application has a period that started at or before that end and ends after it. Not churn.
- **Paused.** Ended, and the latest snapshot is `paused`. Not churn.
- **Churned.** Ended and neither replaced nor paused. The reason is the first that matches:
  1. reversed: the latest snapshot is `refunded` or `revoked` with access `denied`;
  2. billing: the last live snapshot (latest of `active`, `grace_period`, `on_hold`, `canceled`) is `grace_period` or `on_hold`, or its reason is `billing_issue`;
  3. voluntary: that snapshot has renewal `disabled`, is `canceled`, or its reason is `canceled_at_period_end`;
  4. unclassified: otherwise, including a lineage with no live snapshot.
  "Latest" orders by `observed_at`, then `created_at`, then id.
- **Churn rate.** Churned ÷ active during the window. Null when nothing was active. The numerator is a subset of the denominator.
- **Trial started.** The earliest start of the lineage's `free_trial` snapshots is in [`window_start`, `measured_at`).
- **Trial ended.** The latest end of those snapshots is after `window_start` and at or before `measured_at`.
- **Converted.** An ended trial whose lineage has an allowed snapshot with phase `base` or `discounted`, stored after the first trial snapshot.
- **Outcome not recorded.** An ended trial that did not convert but has a later allowed snapshot whose phase is not recorded, `unknown`, or `proration`.
- **Not converted.** An ended trial with neither.
- **Trial conversion rate.** Converted ÷ (converted + not converted). Outcome-not-recorded trials are left out of both sides.
- **Offer phase coverage.** Lineages active during the window with at least one snapshot carrying a phase, out of all lineages active during the window, per provider.
- **Daily active.** For each UTC day, active at the next midnight, or at `measured_at` for today.
- **Breakdown.** Active now counts on the product at `measured_at`, new on the first period's product, a renewal on the product of the snapshot that first carried that transaction, churn on the last period's product. Rows are sorted by application and product and capped at 200; totals include every row.

## Known limits

- An Apple lineage whose first stored transaction is a renewal (for example, an existing subscriber who opens an app after IAPStack is adopted) counts as new on that renewal's purchase date. `originalPurchaseDate` and `transactionReason` are not stored.
- An Apple upgrade inside one subscription group creates a new transaction in the same lineage and counts as a renewal.
- A Google Play plan change is replaced only when the old and new token periods meet or overlap. A gap counts as churn plus a linked, not-new subscription.
- A Google Play allowed snapshot covers the span from the subscription `startTime` to the current expiry, so a hold that later recovered is not a gap in the history.
- Huawei renewals count only if the order id changes at renewal. No captured Huawei renewal fixture confirms it.
- Counts follow what IAPStack has observed. An unobserved renewal or expiry is missing until the next notification or reconciliation, and a past window can change when late evidence arrives.
- Observations stored before migration 014 have no phase. Coverage grows as reconciliation re-reads active subscriptions.
- Google Play prepaid plans have no renewal status, so their expiry is unclassified churn.
- Each request reads every subscription observation of the environment once (CTEs joined on application and lineage, a partial index on `(project_id, application_id) WHERE product_kind = 'subscription'`). There is no sampling and no cache.

## Money

Left out. A monthly run rate needs each subscription's billing period, and the signed transaction carries only purchase and expiry dates. Inferring the plan length from those dates would be a guess (Apple grace periods move the end, sandbox periods are shortened, and Huawei `purchaseTime` semantics for renewals are unverified). Google Play stores no subscription price at all. Apple's own reference says its price is not for revenue reconciliation. The revenue card stays limited to imported store reports.

## API

`GET /v1/admin/projects/{project_id}/subscription-metrics?environment=&window_days=` (`getAdminSubscriptionMetrics`). Admin bearer or dashboard session. Unknown projects return the scoped `not_found` envelope. A bad environment or window returns `invalid_request`. The response has no customer id, provider reference, price, or currency. A provider that is not available has `availability: not_available` and null trial counts.

## Dashboard

The Subscriptions panel sits under Revenue & activity: an environment select, a 7/30/90-day control, the vitals row, daily active bars, churn reasons, per-provider trials, a product table, and a one-paragraph definition. `DESIGN.md` records the panel and the segmented control.

## Tests

- `internal/persistence/subscription_metrics_test.go`: one scripted lineage set pins every definition above without a database, plus the no-future rule and query bounds.
- `internal/persistence/postgres/subscription_metrics_test.go`: stores snapshots through `SaveObservation` and checks the SQL against the definitions, phase enrichment, environment and family-sharing exclusion, and `not_found`.
- `internal/persistence/postgres/migrate_test.go`: migration 014 lifecycle and constraints.
- Apple and Google Play adapter tests: field names decoded from signed JSON and every mapping row.
- `internal/transport/httpapi`: defaults, bounds, 401, 404, null trial counts for Huawei, OpenAPI conformance, and the `-tags=integration` HTTP path.
- `dashboard`: route, parameters, and fields on the contract; the stated definitions in the markup; no revenue wording in the panel.

## Open questions

- Huawei offer phase. Capture a real `InAppPurchaseData` response carrying `trialFlag` and `introductoryFlag` into a fixture next to the adapter, then map them. Until then Huawei stays `not_available`.
- Apple `transactionReason` and `originalPurchaseDate`. Storing them as optional normalized fields would remove the first two known limits. Is a second analytics field acceptable under ADR-0005, or should it wait for a provider-neutral "period reason"?
- Google Play replacement. Should the adapter also emit the linked token under the lineage purpose, so SQL can join a replacement to the lineage it replaced instead of relying on meeting periods?
- Churn reason. Apple `expirationIntent` (already decoded in the renewal payload, not normalized) and Google Play `canceledStateContext` name the reason directly, and `replacementCancellation` marks a Google Play plan change without relying on meeting periods. Normalizing one cancellation reason would shrink `unclassified` and make `replaced` exact.
- Trial cohort. Should conversion also be reported per start cohort (trials started in the window), as RevenueCat does, in addition to trials ended in the window?
- Scale. Is a per-request scan acceptable for the largest expected self-hosted project, or should a daily rollup table be added?
- Money. Should IAPStack ever show a list-price run rate? It would need Google Play `recurringPrice` and a stored billing period for every provider, and it must stay out of the revenue card.
