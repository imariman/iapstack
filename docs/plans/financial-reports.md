# Financial reports

Unnumbered post-v0.1 plan. This work is not part of the v0.1.0-rc.1 release gate and must not be required for that gate to pass. `docs/v0.1-scope.md` still lists official store financial-report ingestion as a v0.1 non-goal. Do not add the migration, routes, or dashboard copy below to the release-candidate tree to make the gate green.

No product version is assigned here.

## Invariant

IAPStack never estimates accounting revenue from client-supplied prices, purchase tokens, or a currency conversion. Sandbox and test value stay separate from recognized proceeds. A production project with no accepted store report stays `store_reports_required`.

Purchase observations are not a ledger. `purchase_observations.price_milliunits` (migration `009_purchase_prices.sql`) is provider-signed test price metadata. The importer must not write that column, and the overview must not sum it into recognized proceeds.

## What the code does today

`docs/operations.md` (Operations dashboard) and `docs/api-v1.md` (Administrative read model) already state the rule: sandbox projects show zero real revenue; production projects report `store_reports_required` until official Apple, Google Play, or Huawei financial reports are ingested; the API does not estimate revenue from client prices or purchase counts.

`internal/persistence/admin.go` defines three statuses only: `sandbox_only`, `store_reports_required`, `no_applications`. `AdminRevenue` is one nullable `RecognizedMinorUnits` plus one nullable `Currency`. `AdminSandboxValue` is a separate per-currency list of milliunits.

`internal/persistence/postgres/admin.go` `adminAnalytics` does not read a report table. If `production_application_count > 0` it always sets `store_reports_required` and leaves the recognized amount nil. Otherwise it sets recognized minor units to zero for `sandbox_only` or `no_applications`. `adminSandboxValue` sums the latest non-reversed purchased snapshot on `sandbox` and `test` applications only, grouped by `price_currency`, with no conversion.

`contracts/openapi/v1.yaml` `AdminRevenue` repeats that closed enum. `recognized_minor_units` is a single non-negative int64. That shape cannot represent more than one currency, a negative refund period, or a named report period.

`dashboard/app.js` `renderRevenue` shows an em dash and "not calculated" for `store_reports_required`. Any other status except `no_applications` falls through to the label "Sandbox only". A new status shipped without a dashboard change would be mislabeled. `formatRevenue` divides one minor-unit total by the currency exponent. `formatMilliunitValue` divides by 1000 and is only for signed test prices. Do not reuse it for store reports.

The commerce chart is a 30-day UTC verification series. It is not a financial period. Apple fiscal months, Google earnings months, and Huawei settlement periods are not that window and are not the same as each other.

## What "recognized" means

Recognized proceeds are an amount printed by an official store report, restricted to production applications in the project.

- Use the store's post-commission developer proceeds when the report defines that figure.
- Otherwise keep the gross figure and set commission state to `unknown`. Do not invent a commission rate.
- Tax state is always `not_itemized`. IAPStack does not calculate, withhold, remit, or file tax. A store figure that already nets tax is still `not_itemized`: the dashboard must not claim the tax was handled here.
- Refunds and returns stay inside the store figure. Do not drop negative rows. A currency total may be negative. Do not clamp at zero.
- Do not convert currencies. Do not pick a payout currency. Do not apply a rate from the report, from the operator, or from a third party.
- Do not treat a missing file as zero. Apple generates a financial report only when the fiscal period had purchases or refunds. An absent file is an absent file.

Commission state is `included` or `unknown`. No other values.

## Report sources

Only the reports below are in scope. Sales and Trends, Google estimated sales, tax-only summaries, ad earnings, and order-detail exports are not revenue inputs.

### Apple financial reports

Official field list, read for this plan:

- [Download financial reports](https://developer.apple.com/help/app-store-connect/getting-paid/download-financial-reports/)
- [Financial report fields](https://developer.apple.com/help/app-store-connect/reference/reporting/financial-report-fields/)
- [Financial report regions and currencies](https://developer.apple.com/help/app-store-connect/reference/reporting/financial-report-regions-and-currencies/)
- [View payments and proceeds](https://developer.apple.com/help/app-store-connect/getting-paid/view-payments-and-proceeds/)
- [Sales and Finance](https://developer.apple.com/documentation/appstoreconnectapi/sales-and-finance)

Payments and Financial Reports are final proceeds. Sales and Trends are next-day estimates, including a USD estimate from a rolling exchange-rate average. Do not import Sales and Trends. Do not import the transaction tax report as proceeds.

The file IAPStack accepts is the consolidated financial report: "All Countries or Regions (Single File)". Region code `ZZ` is the documented code for consolidated financial reports and is multi-currency. The download is a gzip of a tab-delimited text file. The report follows Apple's fiscal calendar, not a calendar month. Rows carry `Start Date` and `End Date` as `MM/DD/YYYY`.

Documented columns the Apple parser must require, by the names on the financial-report fields page:

| Field | Use |
| --- | --- |
| Start Date | Period start. One distinct pair per accepted file. |
| End Date | Period end. |
| Apple Identifier | Match to the production application's provider application id. |
| Quantity | Integrity check only. Do not multiply again. |
| Partner Share | Documented as proceeds per unit: customer price minus applicable taxes and Apple's commission. Not the recognized total by itself. |
| Extended Partner Share | Recognized amount. Documented as quantity times Partner Share. Signed. |
| Partner Share Currency | Grouping currency. ISO code of the amount earned, not the bank payout currency. |
| Sale or Return | `S` or `R`. |
| Customer Price | Must not be summed into recognized proceeds. |
| Customer Currency | Must not replace Partner Share Currency. |

`Partner Share` is already after Apple's commission and after taxes Apple says it collects and remits. Commission state for an accepted Apple total is `included`. Tax state stays `not_itemized`. IAPStack does not recompute either figure and does not show the bank-account total. That bank total is converted. It is out of scope.

Reject the file when:

- the header is missing or a required name above is absent
- more than one Start Date / End Date pair appears
- a money field is not an exact decimal
- `Sale or Return` is `S` and Extended Partner Share is negative, or `R` and Extended Partner Share is positive
- Partner Share Currency is not three ASCII letters
- the same Apple Identifier matches more than one production application in the project

Sum Extended Partner Share per Apple Identifier and Partner Share Currency. Then keep only identifiers that match exactly one production `apple_app_store` application in the project. Rows for any other app are excluded, not allocated. A production Apple application with no matching row is uncovered. Do not synthesize a zero row for it.

The detailed report (`Z1`, "All Countries or Regions (Detailed)") adds Transaction Date, Settlement Date, and Order Type. Apple's field note says Order Type can carry an order id for promotional offers. Do not accept the detailed report in this plan. Do not persist Order Type, promo codes, vendor names, or titles.

The finance-report API page did not render its parameter table (the markdown export only restates the download help page). Region codes for the API are documented, including `ZZ` and `Z1`. Do not hard-code query parameter names from blogs or forum posts. File upload does not need those parameters. A pull client is specified only after the official parameter table is captured into the repo next to the fixture.

Fiscal-month availability (first Friday of the next fiscal month, ten-year retention) is Apple's schedule. IAPStack must not poll on that schedule and must not mark a period missing because a Friday has passed.

### Google Play earnings reports

Official pages, read for this plan:

- [Download sales and payout reports](https://support.google.com/googleplay/android-developer/answer/2482017)
- [Download and export monthly reports](https://support.google.com/googleplay/android-developer/answer/6135870)
- [BigQuery Data Transfer Service data source change log](https://cloud.google.com/bigquery/docs/transfer-changes) (Google Play Console, 1 December 2025 earnings changes)
- [Load Google Play data into BigQuery](https://cloud.google.com/bigquery/docs/play-transfer)

Use the earnings report only. The same Play help page says estimated sales are the amount paid by buyers, do not deduct taxes or Google fees, and are not for accounting. Do not import `sales/salesreport_YYYYMM.zip`.

What is verified:

- Play Console path: Download reports, Financial, Earnings reports, one `.csv`. The payout page also says financial data on that page is UTC.
- Cloud Storage object: `gs://[developer_bucket_id]/earnings/earnings_YYYYMM.zip`. Reports are accumulated into a monthly CSV. Access requires the global "View financial data" permission. The monthly-reports page says a BigQuery import must convert the CSV from UTF-16 to UTF-8, so the parser must not assume UTF-8.
- Earnings reports contain the prior month and are typically available by the fifth of the next month. Google may later publish a separate adjustment file. IAPStack must not add an adjustment file to an earlier file.
- The payout page names a "Transaction Type" column and the values "Charge", "Charge refund", "Google fee", and "Google fee refund". It tells the reader to sum a column it calls "Merchant Currency" for those filters. It does not publish a full header row, and it does not say whether "Merchant Currency" is an amount or a currency code.
- The monthly-reports page warns that in October 2026 the "Fee Description" column gains values `APPS_GAMES_PROGRAM` and `APPS_GAMES_PROGRAM_NEW_INSTALL`.
- The BigQuery changelog says the earnings report itself changes on 1 December 2025: `Base_Plan_ID` to `Base_Plan_or_Purchase_Option_ID`, `Product_id` to `Package_ID`, `Product_Type` from a number to a string, plus a new `Sales_Channel`. Those identifiers are the transfer schema, not a verified CSV header. Do not treat them as the file's column titles.

The earnings layout is not stable, and this plan did not verify a header row. The Google parser must stay disabled until a fixture captured from a real earnings CSV is checked in. That fixture is the column contract. Until then an uploaded earnings file is stored as an import record with status `unrecognized` and a reason `fixture_required`. It does not cover any application and does not change overview status.

Open questions, to be answered only from that fixture, not from memory:

- the exact header row and encoding
- which column is the merchant-currency amount, and which column is the currency code
- which Transaction Type values are developer proceeds after the Google fee
- whether a "Tax" line is part of the amount the developer receives
- which column is the package name used to match `provider_application_id`
- whether a second file for the same `YYYYMM` is an adjustment (supersede, do not add) or a duplicate

Description values in earnings files are order ids (`GPA…`, including renewal suffixes). Even after the parser is enabled, those cells are discarded. They are not stored, logged, or used as metric labels.

Account-level files include every app in the Play developer account. Rows whose package is not a production application in this project are excluded. If the package column is not in the pinned fixture, the file cannot be recognized.

### Huawei AppGallery settlement reports

Official pages used for this plan:

- [获取对账结算数据](https://developer.huawei.com/consumer/cn/doc/App/reconciliation-data-0000002348239424) (indexed text; a direct fetch of the page on 2026-10-26 returned an empty shell)
- [财务结算](https://developer.huawei.com/consumer/cn/doc/app/financial-settlement-0000001931995732)
- [Benefit Analysis and Reports](https://developer.huawei.com/consumer/en/doc/harmonyos-guides/iap-data-analysis) (navigation only; the direct fetch returned no body)
- [Self-service Settlement Guide](https://developer.huawei.com/consumer/en/doc/start/checkoutguide-0000001053128363) (last updated 2021-06-02)

The indexed reconciliation API is:

`POST https://connect-api.cloud.huawei.com/api/apdoms/v1/open-api/finance-report/list`

with a JSON `timeRange` of `start` and `end` (`YYYY-MM-DD`). The indexed response lists `financeReports[]` fields `startDate`, `endDate`, `totalAmount` (described as the merchant's received amount for the period), `settleCurrency`, `reconciliationUrl`, and `settlementFileUrl`.

That field list is not confirmed by a page body we could re-read, and nothing in it proves the total is limited to one app, one IAPStack project, or a known commission treatment. `totalAmount` is therefore not recognized proceeds in this plan. A pull may be implemented only against a captured response fixture, and even then the amount stays `unrecognized` until a reviewed note says the figure is this project's developer proceeds.

Do not download `reconciliationUrl` or `settlementFileUrl` in the first implementation. The English settlement guide describes the details file in prose (period, app name, settlement amount, payment channel, profit-sharing ratio, settlement currency) and refers to spreadsheet column letters in a screenshot. It does not publish a stable header row. The 2021 guide and the later Chinese console docs also disagree about when the sheet appears (the 15th versus the fifth business day). IAPStack must not encode either date as a deadline.

The HUAWEI IAP payment report under My Reports is an order export. It is not a settlement. Do not import it.

The Huawei file parser must be pinned to a fixture captured from a real settlement file. Columns are an open question. Until that fixture exists, an upload is `unrecognized` / `fixture_required` and covers nothing.

Huawei Pay merchant settlement bills are a different product. Do not use their CSV fields for AppGallery.

## Import

Import is explicit. Nothing in verification, notification handling, or the 30-day job imports a report.

Two entry points:

1. The operator uploads one file for one project.
2. The operator requests one provider pull for one period. The worker performs that request. There is no cron.

Each accepted or unrecognized import stores:

- project id
- provider: `apple_app_store`, `google_play`, or `huawei_appgallery` (the values in `internal/core/catalog.go`)
- report kind: `apple_financial_consolidated`, `google_play_earnings`, or `huawei_settlement`
- period start and period end, taken from the file or from the captured API record, never from the 30-day analytics window
- one row per currency actually present, not a converted total
- SHA-256 of the received bytes
- byte size, content type, parser version, status, and the reason when status is not `accepted`
- which production applications were covered, for an accepted Apple import

Do not store the raw file after the request finishes. The bytes exist only in the handler's memory, are hashed, parsed, and discarded. Re-parsing means the operator uploads again. Keeping the file would keep order ids at rest. Do not extend the ADR-0006 protection scope for this plan.

Idempotency key: project, provider, report kind, period start, period end, and payload SHA-256. The same digest returns the existing row. A different digest for the same period does not sum with the old one. The operator must explicitly supersede. Only the latest `accepted` row for that period is eligible for coverage. Google adjustment files follow the same rule.

Reject, with no partial commit:

- a body over the configured byte limit (default 32 MiB of received bytes) or over the configured row limit
- a truncated parse
- a second money scale mixed into one currency
- any attempt to pass a purchase token, a client price, or a `price_milliunits` value as the amount

Limits fail the import. They must not silently drop trailing rows.

Decimals stay exact. Parse into a decimal type, never a float. Persist `amount_decimal` as a numeric value and the original scale. Fill `minor_units` only when the scale matches a checked-in ISO 4217 exponent table. If the file has more fractional digits than that exponent, reject the import. Apple documents two decimal places; do not assume every currency in a `ZZ` file has exponent 2, and do not divide Apple proceeds by 1000. Milliunits remain the sandbox price unit only.

Apple coverage:

- environment must be `production`
- provider must be `apple_app_store`
- `Apple Identifier` must equal that application's provider application id
- every production Apple application in the project must match at least one summed row

A sandbox or test application is never covered and never required.

## Overview status

`GET /v1/admin/projects/{project_id}/overview` keeps the current statuses and adds one:

| Status | When |
| --- | --- |
| `no_applications` | No applications. Recognized amount stays zero. Unchanged. |
| `sandbox_only` | Every application is `sandbox` or `test`. Recognized amount stays zero. Sandbox value is unchanged. An imported report does not turn test value into revenue. |
| `store_reports_required` | Any production application exists and the recognized set is incomplete. |
| `reports_imported` | Every production application is covered by the latest accepted import of its provider. |

`reports_imported` is the only status that returns recognized amounts. The amounts are per provider, per that import's own period, per currency. They are not one number. `recognized_minor_units` and `currency` stay null in this status so an old client cannot show a false single total.

The overview stays `store_reports_required`, and omits amounts, when any of these are true:

- a production application has no accepted, covering import (including a production app of a provider whose parser is still `fixture_required`)
- one production provider is imported and another production provider on the project is not
- the operator supplied `financial_period_start` and `financial_period_end` and any production provider lacks an accepted import whose period equals that range
- the only rows available are `unrecognized`, rejected, or superseded
- the matching report exists but does not include that production application

Do not substitute an older period when the requested period is missing. On the unprompted overview, IAPStack also does not guess that a newer fiscal or calendar month "should" exist. It either has an accepted covering import or it does not. Staleness is shown by printing each import's period, not by inventing a gap.

`financial_period_start` and `financial_period_end` are optional query fields added with this plan. They filter which accepted imports may cover the project. They do not change the 30-day activity series. Omitting them means "latest accepted import per provider", and only if every production provider has one.

Sandbox value is still computed for the 30-day window and still labeled as test value. It is present beside `reports_imported` and beside `store_reports_required`. It is never copied into `amounts`.

## Admin API

All routes are administrator-authenticated, project-scoped, and secret-free in the response. Cookie mutations keep the existing same-origin rule.

- `POST /v1/admin/projects/{project_id}/financial-reports` uploads one file. Form fields: `provider`, `report_kind`, and the file. Period and currency come from the file, not from the form, for Apple. Google and Huawei uploads are accepted only as `unrecognized` until their fixture is pinned; the operator may pass the statement period so the list view can show what was attempted.
- `POST /v1/admin/projects/{project_id}/financial-reports/{import_id}:supersede` marks a newer accepted import as the one that counts for that period. Refuses when the new row is not `accepted`.
- `POST /v1/admin/projects/{project_id}/financial-reports:pull` asks for one pull. Body: `provider`, `report_kind`, `period_start`, `period_end`. Apple pull stays unimplemented until the official finance-report parameter table is captured; the route returns a validation error, not a guessed URL. Google pull is a single GCS object read for `earnings_YYYYMM.zip` using a financial-report credential, then the same disabled parser. Huawei pull is the finance-report list call, then `unrecognized`. Pull is never implied by a verify call.
- `GET /v1/admin/projects/{project_id}/financial-reports` lists metadata: id, provider, report kind, period, currencies, decimal amounts only when `accepted`, digest, status, reason, covered application ids. No file body, no row text, no order id.

Reporting credentials are not the purchase-verification credentials. Apple needs an App Store Connect Team key that can call finance reports (Finance role; a Team key, not an individual key). Google needs Cloud Storage access to the earnings prefix with global view-financial-data. Huawei needs the finance-report API credential. Store them through the existing application credential path only if the scope stays the application that owns the store account; do not reuse a verify-only key by adding a hidden scope. If one credential cannot be expressed per application, stop and ask before inventing a second credential store. That decision is not made here.

The list and overview responses omit API keys, ciphertext, and report bodies, consistent with the current admin read model in `docs/api-v1.md`.

## Dashboard

`renderRevenue` must handle `reports_imported` before the sandbox fall-through. Required copy:

| Status | Badge | Value | Note |
| --- | --- | --- | --- |
| `store_reports_required` | Reports required | — | Official store financial reports are required. Purchase prices are not revenue. |
| `reports_imported` | Reports imported | One line per provider, period, and currency. No plus-sign total. | Imported store proceeds for the periods shown. Currencies are not converted. Tax is not calculated. |
| `sandbox_only` | Sandbox only | 0 real revenue, as today | Test purchases never create real store revenue. |
| `no_applications` | No applications | 0 | Unchanged. |

The revenue card eyebrow "Authoritative total" is wrong once the value can be an em dash or several currencies. Use "Not calculated" for `store_reports_required` and "Store-reported proceeds" for `reports_imported`.

Do not use the words estimated, approximate, projected, forecast, MRR, or ARR on this card. Do not say IAPStack calculated tax or commission. The sandbox note stays: provider-signed test value, not real proceeds or accounting revenue.

When status is `store_reports_required`, the note may name uncovered providers ("Google Play has no accepted earnings report."). It must not print that provider's unrecognized amount in the value slot.

The activity chart caption stays "Daily verification outcomes". Do not relabel it as revenue.

## Privacy

Financial reports are account documents, not customer profiles. Still:

- Do not persist customer names. None of the accepted Apple columns are customer names. Do not add a column later to capture one.
- Apple Order Type, Google Description, and Huawei order-detail fields are order ids when present. Discard them during parse. A parse error names the row number and the column name, never the cell, when the column can hold an order id.
- Do not put order ids, vendor numbers, package-unrelated account ids, or raw digest-bearing filenames into metric labels or logs. Log import id, project id, provider, report kind, period, status, and a failure code.
- Covered application ids and store product identifiers are enough for coverage. Product titles are not stored.
- The overview and the list route never return source bytes.

## Schema

Add one migration after `009_purchase_prices.sql` when implementation starts. Do not ship it inside the v0.1.0-rc.1 migration set.

Two tables are enough:

`financial_report_imports`

- id, project_id, provider, report_kind
- period_start, period_end
- source: `upload` or `provider_api`
- received_sha256, byte_size, parser_version
- status: `accepted`, `unrecognized`, `rejected`, `superseded`
- reason (empty when accepted)
- supersedes_import_id nullable
- created_at

`financial_report_amounts`

- import_id, currency
- amount_decimal (numeric, not float), amount_scale
- minor_units nullable bigint (negative allowed)
- commission_state: `included` or `unknown`
- tax_state: `not_itemized` only
- covered_application_id (one row per application and currency; the overview sums applications of one provider only after coverage is complete)

Constraints:

- currency matches `^[A-Z]{3}$`, same as migration 009
- unique (project_id, provider, report_kind, period_start, period_end, received_sha256)
- at most one non-superseded `accepted` row per (project_id, provider, report_kind, period_start, period_end)
- no foreign key to `purchase_observations`
- amounts rows exist only for `accepted` imports

No raw payload column.

## Delivery slices

Ship them in this order. Do not flip overview status in an earlier slice.

1. Schema. Migration, store functions, and tests that round-trip an accepted Apple amount and an unrecognized Google row. Overview behavior stays the current hard-coded `store_reports_required`. No release-gate change.
2. Importer. Apple consolidated parser against the synthetic fixture. Google and Huawei parsers return `fixture_required` and do not guess a column. Shared decimal and coverage checks. No purchase-price input.
3. Admin API. Upload, list, supersede, and a pull route that validates the request and refuses Apple, Google, and Huawei pulls that are not yet pinned. Wire the administrator auth already used by the dashboard session.
4. Overview and dashboard, together. Status rules above, OpenAPI enum extended with `reports_imported`, `amounts` added, `recognized_minor_units` documented as null for that status, dashboard copy and the fall-through fix. Update `docs/api-v1.md` and the revenue paragraphs in `docs/operations.md` in the same change. Leave the v0.1 non-goal sentence in place until a later scope edit; do not pretend v0.1 grew this feature.

Relative size: schema is small, the Apple parser and status rules are the bulk, Google and Huawei recognition are blocked on fixtures rather than on more code, dashboard copy is small but must ship with the status change.

## Tests

Checked-in synthetic fixtures only. No live store credentials, no recorded customer export, no real order id.

- Apple fixture: tab-delimited text with the documented header names, two Partner Share Currencies, one `S` row, one `R` row with a negative Extended Partner Share, one Apple Identifier that is not in the project, and one identifier that matches a sandbox application. Expected recognized totals ignore the unknown id and the sandbox id, keep both currencies, and keep the negative return.
- A second Apple fixture with a bad header, a mixed period, and an `S` row with a negative Extended Partner Share. Each rejects the whole import.
- Google and Huawei fixture files are not invented. A test feeds a small CSV and a small JSON document and asserts status `unrecognized` and reason `fixture_required`.
- Overview: production app and no import stays `store_reports_required` with amounts absent, even when `price_milliunits` is populated on that project's sandbox app.
- Overview: Apple production app covered and Google production app present stays `store_reports_required`.
- Overview: explicit period with no import stays `store_reports_required` even if another period is accepted.
- Overview: only production Apple apps, all covered, one file, two currencies, returns `reports_imported` with two amount rows and null `recognized_minor_units`. Assert the sandbox milliunit sum is unchanged and is not equal to a shortcut into recognized amounts.
- Dashboard contract: `reports_imported` does not render "Sandbox only"; `store_reports_required` renders an em dash; the revenue card strings do not contain "estimated".
- Logs from a failed Apple row do not contain the Apple Identifier cell. The test can use a distinctive sentinel in a column the parser must not echo.

## Acceptance

- A production project with no accepted covering import still returns `store_reports_required`, an em dash, and no client-price estimate.
- A sandbox-only project still returns zero real revenue and a separate test-value list. Importing a report does not change that.
- An accepted Apple consolidated file changes status to `reports_imported` only when every production application in the project is covered.
- Recognized Apple amounts equal the summed Extended Partner Share per currency for matched production apps, including negatives, with commission state `included` and tax state `not_itemized`.
- No code path converts currency or reads `price_milliunits` to fill recognized amounts.
- Google and Huawei files cannot reach `accepted` until a real-report fixture is added and the open questions above are closed in this document.
- List and overview payloads contain no report body and no order id.
- v0.1.0-rc.1 remains shippable without this migration.

## Non-goals

- Attribution of proceeds to campaigns, users, or IAPStack customers
- Ads ROAS or any ad-network report
- Tax filing, tax categories, invoices, or the Apple transaction tax report
- Payouts to the operator's bank, payout currency, or exchange rates shown in Payments and Financial Reports
- Consumable accounting, deferred revenue, and recognition schedules
- Estimating a missing month as zero
- Sales and Trends, Google estimated sales, and Huawei order-detail exports
- Merging this work into the v0.1.0-rc.1 gate

## Open questions

- Apple finance-report query parameter names, until the official API page is captured. Upload is not blocked on this.
- Google earnings header, amount column, package column, tax lines, and adjustment-file behavior. Parser stays disabled.
- Huawei settlement file headers, whether `totalAmount` is this project's post-commission proceeds, and whether that total can be limited to the project's production applications. Parser stays disabled.
- Whether a future reporting credential fits the application-scoped credential store. Do not invent a second store inside the importer slice.
