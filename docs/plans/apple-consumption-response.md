# Apple refund consumption answers

- Status: Implemented, off by default for every application
- Date: 2026-11-18
- Release: Unnumbered post-v0.1 plan. Gap row G3 in the competitor comparison
- Schema: migration `016_apple_consumption.sql`. It needs migrations 014 and 015 in the tree first, because tern refuses a gap

RevenueCat answers App Store consumption requests on its customers' behalf. Adapty (Refund Saver), Qonversion (Refund Keeper), and Superwall (Refund Protection) sell the same thing. This plan is the IAPStack version. It is narrower than those products on purpose: IAPStack sends only what it can prove from its own records.

## Verified Apple contract

Read on 2026-11-18 from developer.apple.com:

- [Send Consumption Information](https://developer.apple.com/documentation/appstoreserverapi/send-consumption-information)
- [ConsumptionRequest](https://developer.apple.com/documentation/appstoreserverapi/consumptionrequest)
- [deliveryStatus](https://developer.apple.com/documentation/appstoreserverapi/deliverystatus)
- [refundPreference](https://developer.apple.com/documentation/appstoreserverapi/refundpreference)
- [consumptionPercentage](https://developer.apple.com/documentation/appstoreserverapi/consumptionpercentage)
- [sampleContentProvided](https://developer.apple.com/documentation/appstoreserverapi/samplecontentprovided)
- [customerConsented](https://developer.apple.com/documentation/appstoreserverapi/customerconsented)
- [Send Consumption Information V1](https://developer.apple.com/documentation/appstoreserverapi/send-consumption-information-v1) and [ConsumptionRequestV1](https://developer.apple.com/documentation/appstoreserverapi/consumptionrequestv1)
- [App Store Server API changelog](https://developer.apple.com/documentation/appstoreserverapi/app-store-server-api-changelog)
- [notificationType](https://developer.apple.com/documentation/appstoreservernotifications/notificationtype), [data](https://developer.apple.com/documentation/appstoreservernotifications/data), and [consumptionRequestReason](https://developer.apple.com/documentation/appstoreservernotifications/consumptionrequestreason)
- [App Store Server Notifications changelog](https://developer.apple.com/documentation/appstoreservernotifications/app-store-server-notifications-changelog)

### There is a version 2 endpoint

API version 1.19 (2025-12-10) added a new Send Consumption Information endpoint and says to use it for In-App Purchases that do not use the Advanced Commerce API. The old endpoint is now titled "Send Consumption Information V1" and carries the same instruction. IAPStack has no Advanced Commerce support, so it calls only version 2:

```http
PUT https://api.storekit.apple.com/inApps/v2/transactions/consumption/{transactionId}
PUT https://api.storekit-sandbox.apple.com/inApps/v2/transactions/consumption/{transactionId}
```

`transactionId` is the one in the notification's signed transaction. The body is JSON. Authentication is the normal App Store Server API ES256 bearer that the adapter already signs.

### ConsumptionRequest (version 2) fields

| Field | Required | Type and values |
| --- | --- | --- |
| `customerConsented` | yes | Boolean. Apple returns `400 InvalidCustomerConsentedError` for anything but `true`. Without consent, "don't respond to the `CONSUMPTION_REQUEST` notification." |
| `deliveryStatus` | yes | `DELIVERED`, `UNDELIVERED_QUALITY_ISSUE`, `UNDELIVERED_WRONG_ITEM`, `UNDELIVERED_SERVER_OUTAGE`, `UNDELIVERED_OTHER`. No undeclared value. |
| `sampleContentProvided` | yes | Boolean. True when the developer gave a free sample or trial, or described how the content works, before the purchase. No undeclared value. |
| `consumptionPercentage` | no | int32 milliunits, 0 to 100000. Must be `0` unless `DELIVERED`. Must be absent for auto-renewable subscriptions (`ConsumptionPercentageAutoRenewableSubscriptionError`). With `GRANT_PRORATED` on other product types the percentage page says it must be above 0 and below 100000. |
| `refundPreference` | no | `DECLINE`, `GRANT_FULL`, `GRANT_PRORATED`. `GRANT_FULL` and `DECLINE` work for every product type. `GRANT_PRORATED` on an auto-renewable subscription must not carry a percentage; Apple computes it from elapsed time. |

Responses: `202` accepted; `400` with an error code (invalid transaction, family transaction, app transaction ID, consent, delivery status, sample content, refund preference, percentage range or subscription rules, Advanced Commerce, assigned transaction); `401` bad JWT; `404 TransactionIdNotFoundError`; `429 RateLimitExceededError`; `500 GeneralInternalError`.

The deadline: "Respond within 12 hours of receiving the `CONSUMPTION_REQUEST` notification."

### The notification

`CONSUMPTION_REQUEST` arrives through App Store Server Notifications V2. Its `data` object has the usual `signedTransactionInfo` and, since 2024-04-11, an optional `consumptionRequestReason`: `UNINTENDED_PURCHASE`, `FULFILLMENT_ISSUE`, `UNSATISFIED_WITH_PURCHASE`, `LEGAL`, `OTHER`.

The pages disagree on scope. The endpoint page says any product type (consumable, non-consumable, non-renewing, auto-renewable). The `notificationType` and `data` pages still say consumable or auto-renewable only. IAPStack accepts all four.

### Fields that are gone

The task asked for account tenure and lifetime purchased and refunded amounts. Those fields (`accountTenure`, `lifetimeDollarsPurchased`, `lifetimeDollarsRefunded`, `playTime`, `platform`, `userStatus`, `consumptionStatus`, `appAccountToken`) exist only in `ConsumptionRequestV1`, which is for Advanced Commerce purchases now. Version 2 has no slot for them, so IAPStack does not derive or send them. That also removes the USD conversion problem: IAPStack never converts currencies (see `financial-reports.md`), and the V1 fields were in USD.

## Field derivation

| Field | What IAPStack sends | From |
| --- | --- | --- |
| `customerConsented` | `true` | Only after the consent check passes. Otherwise nothing is sent. |
| `deliveryStatus` | `DELIVERED` | A stored `purchase_observations` row with `access_status = 'allowed'` that carries this exact `transaction_id` reference, for the same customer or a customer merged into it. Without that row nothing is sent (`no_delivery_evidence`). IAPStack never sends an `UNDELIVERED_*` value; it has no evidence of a failed delivery. |
| `sampleContentProvided` | Operator declaration | `apple_consumption_settings.sample_content_provided`. A required field in the settings write. |
| `refundPreference` | Operator policy, or absent | `none` omits it. `decline` and `grant_full` map directly. `grant_prorated` is sent only for auto-renewable subscriptions and is left out for other types. |
| `consumptionPercentage` | Never sent | IAPStack does not know how much of a purchase was used. |

## Design

### Pipeline

1. The existing Apple notification route verifies the JWS layers. For `CONSUMPTION_REQUEST` the adapter adds a `consumption` block to the normalized envelope: transaction ID, product type, provider product ID, app account token, ownership, and the mapped reason. It is stored only inside the protected inbox payload. Consumables and non-renewing subscriptions are accepted here even though verification does not support them.
2. The inbox worker (`processing.handleAppleInbox`) first stores an `apple_consumption_requests` row and its River job in one transaction, then runs the usual verification for subscriptions and lifetime products. Scheduling first means a failed refresh cannot lose the request.
3. The request id is `deterministicID("consumption", keyed fingerprint of notificationUUID)`. A redelivered notification maps to the same row, and `ON CONFLICT (id) DO NOTHING` keeps the first.
4. The job is due one minute later, so the same notification's refresh can commit first. The deadline is `signedDate + 12h`, from Apple's signed notification.
5. The worker (`processing.HandleAppleConsumption`) runs these checks in order and records the first that ends the run:

| Check | Result |
| --- | --- |
| Request is no longer `pending` | Nothing; a replay |
| Now is at or after the deadline | `expired` / `deadline_passed` |
| Settings never saved or `enabled` false | `skipped` / `disabled`. The protected payload is not opened. |
| Family Sharing transaction | `skipped` / `family_shared` (Apple rejects these) |
| Other ownership, such as an assigned seat | `skipped` / `unsupported_ownership` |
| No recorded yes for the customer (see below) | `skipped` / `no_consent` or `consent_withdrawn` |
| No allowed observation yet, less than one hour after receipt | attempt `transient_error` / `awaiting_delivery_evidence`; River retries |
| No allowed observation for this transaction and customer after that hour | `skipped` / `no_delivery_evidence` |
| Apple answers 2xx | `sent` / `accepted` |
| Apple answers 429, 5xx, or the network fails | attempt `transient_error`; River retries |
| Apple answers 400, 401/403, 404, or another 4xx | `failed` / `apple_rejected`, `provider_unauthorized`, `transaction_not_found`, or `provider_permanent` |
| Retries run out | `failed` / the last transient reason |

### Retry

`apple_consumption` is a fourth River queue with its own worker kind. The job carries its request id and deadline. `NextRetry` doubles from one minute (1, 2, 4, 8, 16, 32 minutes, then one hour) and never schedules past the deadline, so the last retry lands on it and records `expired`. Each job has `MaxAttempts = 10`; the ninth retry is about four hours after the first try. The worker's existing executor stores the terminal failure and closes the request.

### Audit

Every handler run that reaches a decision writes one `apple_consumption_attempts` row: attempt number, time, outcome, reason, HTTP status, Apple's numeric error code, and the exact JSON body sent. The body has no identifier. The worker also logs each attempt with the request id, outcome, reason, and HTTP status, and nothing else. Terminal requests and their attempts are purged with the other queue records after `IAPSTACK_QUEUE_RETENTION` (30 days by default).

### Tables (migration 016)

- `apple_consumption_settings`: one row per application. `enabled`, `refund_preference`, `sample_content_provided`, revision, and the admin key that last saved it. No row means disabled.
- `apple_consumption_consents`: `(application_id, customer_id)` with the latest `consented` value and the application key that recorded it.
- `apple_consumption_requests`: the durable job and its terminal state. Clear columns are catalog and state only (product id, product type, the customer's reason code, times, status, reason, attempt count). The transaction id, app account token, and ownership are in the protected payload.
- `apple_consumption_attempts`: the audit rows.

The down migration refuses to run while a request is `pending`.

## Consent model

Apple puts consent entirely on the developer. IAPStack never assumes it. Consent is opt-in only (maintainer decision, 2026-11-18).

- **A recorded yes is required.** The worker answers only when the customer's latest recorded decision is `consented: true`. No record, an unknown customer, or a recorded no means the request is `skipped` with `no_consent` or `consent_withdrawn`. Apple's guidance is to not respond, and IAPStack does not.
- **Only the host backend records consent.** It calls `PUT /v1/applications/{application_id}/customers/{external_customer_id}/apple-consumption-consent` with the durable application bearer, after the customer answers the app's consent prompt. The route uses `requireApplication`, which accepts stored application API keys only. A short-lived customer session token, the bearer the mobile app holds, is rejected with `401`, and so are admin bearers and dashboard sessions. `TestAppleConsumptionConsentRejectsCustomerSessions` and the HTTP integration test cover this.
- The newest decision wins. Recording `consented: false` is how a customer withdraws.
- There is no operator-level consent declaration. The settings hold only the switch, the refund preference, and the sample declaration.
- Consent is per application, because Apple asks per app. Both the consent route and the worker resolve the app account token through the canonical customer. A decision recorded before a merge stays on the source customer and still counts; the newest decision across the canonical customer and customers merged into it wins.

## Privacy notes

- Off by default for every application. Turning it on is an explicit admin write.
- Only five fields leave the server, and none identifies the customer. The path carries the transaction id, which Apple issued.
- Nothing is inferred. There is no consumption percentage, no undelivered status, and no account or spend history.
- The customer's refund reason is stored as a closed code and shown to operators in the dialog. It is Apple's data about the customer's own request.
- Apple asks developers who adopt this API to update their App Privacy answers and to send data-access or deletion requests about consumption data to privacy.apple.com. That is the operator's job; `docs/operations.md` says so.
- A disabled application never opens the protected request payload.

## API and dashboard

- `GET` and `PUT /v1/admin/projects/{project_id}/applications/{application_id}/apple-consumption`: settings with revision control, and the 20 newest requests. App Store applications only; others get `400`.
- `PUT /v1/applications/{application_id}/customers/{external_customer_id}/apple-consumption-consent`: durable application bearer only; customer sessions get `401`; `404` for an unknown customer.
- The connections dialog of an App Store application has a **Refund consumption answers** section: on or off, refund preference, the sample declaration, and a note that consent is opt-in and recorded by the backend, then a list of recent requests. Each status is a labelled badge with its reason under it.

## Known limits

- Delivery evidence is exact: IAPStack must have observed that `transaction_id` with allowed access. A renewal that no refresh observed is skipped as `no_delivery_evidence`. Consumables are never verified by IAPStack, so they are always skipped this way.
- Missing delivery evidence is retried for one hour after receipt, because the purchase refresh or a client verification may still be retrying. Evidence that arrives later than that does not reopen a skipped request.
- A setting applies when the job runs. A request skipped as `disabled` is not reopened when the operator turns the feature on.
- `Retry-After` from Apple is not used; the doubling backoff applies.
- There is no consent history table; the latest decision and its timestamps are kept.
- The branch only migrates after migrations 014 and 015 land, because tern needs consecutive numbers.

## Decisions

Recorded 2026-11-18 from the maintainer:

1. Consent is opt-in only. The `application_attested` mode was removed from the code, migration 016, OpenAPI, the dashboard, tests, and docs.
2. Only the host backend records consent, with the durable application bearer. Customer sessions are rejected.
3. There is no "delivered by default" declaration. `DELIVERED` stays tied to a stored allowed observation for that exact transaction; anything else is skipped with its reason recorded.

## Open questions

1. Should the customer's refund reason be shown in the dashboard, or only kept in the API?
2. Do we need a consent history for audits, rather than the latest value?
3. Should requests skipped as `disabled` be stored at all? They show the operator what Apple is asking, but they are records about a customer's refund request.
4. Which product owns the refund policy wording in the dialog? Apple's wording for `DECLINE` and `GRANT_FULL` is used as is.

## Tests

- `internal/stores/apple/consumption_test.go`: the request builder, refund preference mapping per product type, consent and delivery gating, the V2 path, JWT, and exact body against an `httptest` server, and the response classes.
- `internal/processing/apple_consumption_test.go`: every skip reason, merged customers, the deadline, transient and permanent errors, replays, and scheduling from the inbox.
- `internal/jobs/consumption_test.go`: the backoff and that all attempts fit inside 12 hours.
- PostgreSQL (`-tags=integration`): repository revisions, consent, attempts and terminal states, listing, retention, the delivery lookup, the dirty-down guard, the full inbox-to-attempt pipeline, the HTTP routes, and the compact process closing a request through River.
