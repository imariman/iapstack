package persistence

import (
	"context"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// AdminCustomerMatchExternalID means the query equals the customer's own external id.
	AdminCustomerMatchExternalID AdminCustomerMatch = "external_id"
	// AdminCustomerMatchCustomerID means the query equals the internal customer id.
	AdminCustomerMatchCustomerID AdminCustomerMatch = "customer_id"
	// AdminCustomerMatchAlias means the query equals an alias external id of the canonical customer.
	AdminCustomerMatchAlias AdminCustomerMatch = "alias"
	// AdminCustomerMatchTransactionID means the query equals a stored provider transaction reference.
	AdminCustomerMatchTransactionID AdminCustomerMatch = "transaction_id"
	// AdminCustomerMatchOriginalTransactionID means the query equals a stored provider lineage reference.
	AdminCustomerMatchOriginalTransactionID AdminCustomerMatch = "original_transaction_id"
	// AdminCustomerMatchExternalIDPrefix means the customer's external id starts with the query.
	AdminCustomerMatchExternalIDPrefix AdminCustomerMatch = "external_id_prefix"
	// AdminCustomerMatchCustomerIDPrefix means the internal customer id starts with the query.
	AdminCustomerMatchCustomerIDPrefix AdminCustomerMatch = "customer_id_prefix"

	// AdminCustomerSearchMaxQueryBytes bounds untrusted search text before any lookup.
	AdminCustomerSearchMaxQueryBytes = 512
	// AdminCustomerSearchDefaultLimit is the page size used when a request names none.
	AdminCustomerSearchDefaultLimit = 25
	// AdminCustomerSearchMaxLimit bounds one search page.
	AdminCustomerSearchMaxLimit = 100
	// AdminCustomerTimelineLimit bounds the merged timeline returned for one customer.
	AdminCustomerTimelineLimit = 200
	// AdminCustomerTimelineSourceLimit bounds rows read from each timeline source table.
	AdminCustomerTimelineSourceLimit = 100

	// AdminTimelineCustomerCreated records when a customer row was created.
	AdminTimelineCustomerCreated AdminTimelineKind = "customer_created"
	// AdminTimelineAliasAdded records an alias audit row.
	AdminTimelineAliasAdded AdminTimelineKind = "alias_added"
	// AdminTimelineCustomerMerged records a merge audit row.
	AdminTimelineCustomerMerged AdminTimelineKind = "customer_merged"
	// AdminTimelinePurchaseObserved records one verified store observation and its lifecycle state.
	AdminTimelinePurchaseObserved AdminTimelineKind = "purchase_observed"
	// AdminTimelineImportGrantRecorded records one imported historical grant.
	AdminTimelineImportGrantRecorded AdminTimelineKind = "import_grant_recorded"
	// AdminTimelineManualGrantCreated records one operator grant.
	AdminTimelineManualGrantCreated AdminTimelineKind = "manual_grant_created"
	// AdminTimelineManualGrantRevoked records one early revocation of an operator grant.
	AdminTimelineManualGrantRevoked AdminTimelineKind = "manual_grant_revoked"
	// AdminTimelineEntitlementChanged records one entitlement projection change.
	AdminTimelineEntitlementChanged AdminTimelineKind = "entitlement_changed"
	// AdminTimelineWebhookDelivered records one successful webhook delivery.
	AdminTimelineWebhookDelivered AdminTimelineKind = "webhook_delivered"
	// AdminTimelineWebhookFailed records one terminal webhook delivery failure.
	AdminTimelineWebhookFailed AdminTimelineKind = "webhook_failed"
	// AdminTimelineReconciliation records one durable reconciliation job outcome.
	AdminTimelineReconciliation AdminTimelineKind = "reconciliation"
)

// AdminCustomerStore exposes the bounded customer support read model for administrators.
type AdminCustomerStore interface {
	// AdminSearchCustomers returns one page of project customers matching untrusted search text.
	AdminSearchCustomers(context.Context, AdminCustomerSearch) (AdminCustomerSearchPage, error)
	// AdminCustomerDetail returns identity, current access, grants, and a bounded timeline for one customer.
	AdminCustomerDetail(context.Context, core.ProjectID, core.CustomerID) (AdminCustomerDetail, error)
}

// AdminReferenceFingerprinter derives scoped lookup fingerprints of search text for provider references.
type AdminReferenceFingerprinter interface {
	// ReferenceFingerprints returns the search text's fingerprints in each application scope.
	ReferenceFingerprints(context.Context, core.ProjectID, []core.ApplicationID) ([]AdminReferenceFingerprint, error)
}

// AdminCustomerMatch explains why one customer appears in a search page.
type AdminCustomerMatch string

// AdminTimelineKind identifies the stored record behind one timeline event.
type AdminTimelineKind string

// AdminReferenceFingerprint is one keyed fingerprint of the search text in one reference scope.
type AdminReferenceFingerprint struct {
	ApplicationID core.ApplicationID
	Role          core.ReferenceRole
	Kind          string
	Fingerprint   [32]byte
}

// AdminCustomerSearchCursor is the keyset position after the last returned search result.
type AdminCustomerSearchCursor struct {
	Rank       int
	CreatedAt  time.Time
	CustomerID core.CustomerID
}

// AdminCustomerSearch selects one bounded page. Empty Query lists customers newest first.
type AdminCustomerSearch struct {
	ProjectID core.ProjectID
	Query     string
	Limit     int
	After     *AdminCustomerSearchCursor
	// References computes provider reference fingerprints. Nil skips transaction matching.
	References AdminReferenceFingerprinter
}

// AdminCustomerSearchResult is one matched customer with its best match reason.
type AdminCustomerSearchResult struct {
	Customer AdminCustomer
	// Match is empty when the page lists customers without a query.
	Match AdminCustomerMatch
	Rank  int
}

// AdminCustomerSearchPage is one ordered result page and the cursor for the next one.
type AdminCustomerSearchPage struct {
	Results []AdminCustomerSearchResult
	Next    *AdminCustomerSearchCursor
}

// AdminCustomerIdentity is one stored customer row without derived counts.
type AdminCustomerIdentity struct {
	ID                   core.CustomerID
	ExternalID           string
	MergedIntoCustomerID *core.CustomerID
	CreatedAt            time.Time
}

// AdminCustomerAlias is one stored alias audit row.
type AdminCustomerAlias struct {
	ExternalID          string
	CanonicalCustomerID core.CustomerID
	ActorRole           APIKeyRole
	ActorKeyID          string
	CreatedAt           time.Time
}

// AdminCustomerMergeRecord is one stored merge audit row with the source customer's external id.
type AdminCustomerMergeRecord struct {
	ID                  string
	CanonicalCustomerID core.CustomerID
	SourceCustomerID    core.CustomerID
	SourceExternalID    string
	ActorKeyID          string
	CreatedAt           time.Time
}

// AdminCustomerEntitlement is one current projection with the access source it cites.
type AdminCustomerEntitlement struct {
	EntitlementID       core.EntitlementID
	Key                 string
	SourceKind          SourceKind
	SourceID            string
	SourceApplicationID core.ApplicationID
	SourceProductID     core.ProductID
	SourceCustomerID    core.CustomerID
	Access              core.AccessStatus
	AccessReason        core.AccessReason
	EffectivePeriod     core.EffectivePeriod
	Version             int64
	UpdatedAt           time.Time
}

// AdminTimelineEvent is one stored record rendered on the customer timeline. Unused fields stay empty.
type AdminTimelineEvent struct {
	Kind                   AdminTimelineKind
	OccurredAt             time.Time
	CustomerID             core.CustomerID
	SourceID               string
	SourceKind             SourceKind
	ApplicationID          core.ApplicationID
	ProductID              core.ProductID
	ProviderProductID      core.ProviderProductID
	EntitlementID          core.EntitlementID
	EntitlementKey         string
	LifecycleState         core.LifecycleState
	PreviousLifecycleState core.LifecycleState
	Access                 core.AccessStatus
	AccessReason           core.AccessReason
	EffectiveEndsAt        *time.Time
	Version                int64
	Status                 string
	ErrorCode              string
	ActorRole              APIKeyRole
	ActorKeyID             string
	ExternalID             string
	RelatedCustomerID      core.CustomerID
	Reason                 string
	OriginStore            string
}

// AdminGrantOption is one product that grants one entitlement and can therefore back a manual grant.
type AdminGrantOption struct {
	EntitlementID  core.EntitlementID
	EntitlementKey string
	ProductID      core.ProductID
	ProductKind    core.ProductKind
	// ApplicationIDs lists applications that map the product to a store product.
	ApplicationIDs []core.ApplicationID
}

// AdminCustomerDetail is one customer closure with identity, access, grants, and a bounded timeline.
type AdminCustomerDetail struct {
	GeneratedAt         time.Time
	Customer            AdminCustomerIdentity
	CanonicalCustomerID core.CustomerID
	Members             []AdminCustomerIdentity
	Aliases             []AdminCustomerAlias
	Merges              []AdminCustomerMergeRecord
	Entitlements        []AdminCustomerEntitlement
	ManualGrants        []ManualGrant
	GrantOptions        []AdminGrantOption
	Timeline            []AdminTimelineEvent
	TimelineTruncated   bool
}
