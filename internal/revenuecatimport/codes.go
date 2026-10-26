package revenuecatimport

import "errors"

const (
	// FormatTransactionsCSV selects RevenueCat's scheduled transactions feed.
	FormatTransactionsCSV = "transactions-csv"
	// FormatSubscriberJSONL selects one Customer Info object per line.
	FormatSubscriberJSONL = "subscriber-jsonl"

	// CodeMissingAppUserID means the canonical app user id is empty.
	CodeMissingAppUserID = "missing_app_user_id"
	// CodeInvalidExternalID means an id fails IAPStack's external customer rules.
	CodeInvalidExternalID = "invalid_external_id"
	// CodeAliasRefused means an alias was not stored and the canonical grants still proceed.
	CodeAliasRefused = "alias_refused"
	// CodeConflictingSubscriber means an alias belongs to another customer, so nothing is written.
	CodeConflictingSubscriber = "conflicting_subscriber"
	// CodeUnknownEntitlement means the RevenueCat entitlement id is not in the mapping.
	CodeUnknownEntitlement = "unknown_entitlement"
	// CodeUnknownProduct means the product or store-product row is not already in the catalog.
	CodeUnknownProduct = "unknown_product"
	// CodeEntitlementNotGranted means the mapped product does not grant the mapped entitlement.
	CodeEntitlementNotGranted = "entitlement_not_granted"
	// CodeUnsupportedProductKind means the mapped product is consumable.
	CodeUnsupportedProductKind = "unsupported_product_kind"
	// CodeProductKindMismatch means a durable row was mapped to a subscription, or the reverse.
	CodeProductKindMismatch = "product_kind_mismatch"
	// CodeEnvironmentMismatch means sandbox state does not match the application.
	CodeEnvironmentMismatch = "environment_mismatch"
	// CodeUnsupportedStore means the store string has no mapping entry.
	CodeUnsupportedStore = "unsupported_store"
	// CodeMissingEffectiveEnd means a renewable row has no access boundary.
	CodeMissingEffectiveEnd = "missing_effective_end"
	// CodeInvalidEntitlementList means entitlement_identifiers is not a JSON array.
	CodeInvalidEntitlementList = "invalid_entitlement_list"
	// CodeBatchConflict means a batch id was reused with a different file hash.
	CodeBatchConflict = "batch_conflict"
	// CodeTruncatedExport means gzip or CSV did not end cleanly.
	CodeTruncatedExport = "truncated_export"

	// anonymousPrefix marks RevenueCat anonymous app user ids. They are customers unless skipped.
	anonymousPrefix = "$RCAnonymousID:"
	// columnOriginalAppUserID is the transactions feed's original app user id.
	columnOriginalAppUserID = "rc_original_app_user_id"
	// columnLastSeenAlias is the transactions feed's other app user id.
	columnLastSeenAlias = "rc_last_seen_app_user_id_alias"
)

var (
	// ErrUsage reports a bad invocation, mapping, or export that must not write.
	ErrUsage = errors.New("revenuecat import usage is invalid")
	// ErrTruncated reports a gzip stream or CSV record that did not end cleanly.
	ErrTruncated = errors.New("revenuecat export is truncated")
	// ErrBatchConflict reports a batch id reused with a different export hash.
	ErrBatchConflict = errors.New("revenuecat import batch conflicts with a different export")
	// errSubscriberConflict rolls back one subscriber whose alias belongs to someone else.
	errSubscriberConflict = errors.New("revenuecat subscriber conflicts with an existing customer")
)
