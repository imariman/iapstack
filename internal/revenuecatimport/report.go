package revenuecatimport

import (
	"time"

	"github.com/imariman/iapstack/internal/core"
)

// Report is the secret-free summary of one dry-run or apply.
type Report struct {
	DryRun       bool      `json:"dry_run"`
	ProjectID    string    `json:"project_id"`
	BatchID      string    `json:"batch_id,omitempty"`
	ExportSHA256 string    `json:"export_sha256,omitempty"`
	Format       string    `json:"format"`
	Counts       Counts    `json:"counts"`
	Failures     []Failure `json:"failures"`
}

// Counts are the operator-facing totals. They never include receipts or prices.
type Counts struct {
	CustomersCreated      int `json:"customers_created"`
	CustomersReused       int `json:"customers_reused"`
	GrantsUpserted        int `json:"grants_upserted"`
	GrantsUnchanged       int `json:"grants_unchanged"`
	ProjectionsChanged    int `json:"projections_changed"`
	WebhooksEnqueued      int `json:"webhooks_enqueued"`
	AliasRefused          int `json:"alias_refused"`
	AnonymousCustomers    int `json:"anonymous_customers"`
	FamilyShared          int `json:"family_shared"`
	SkippedAnonymous      int `json:"skipped_anonymous"`
	Failed                int `json:"failed"`
	RefundEndDisagreement int `json:"refund_end_disagreement"`
	TrialPeriods          int `json:"trial_periods"`
}

// Failure identifies one subscriber or grant the import refused.
type Failure struct {
	Line               int    `json:"line"`
	Code               string `json:"code"`
	ExternalID         string `json:"external_id,omitempty"`
	EntitlementKey     string `json:"entitlement_key,omitempty"`
	LineageFingerprint string `json:"lineage_fingerprint,omitempty"`
}

// Subscriber is the planned canonical customer and the grants that would be written.
type Subscriber struct {
	ExternalID         string
	CustomerID         core.CustomerID
	Aliases            []string
	Grants             []Grant
	Failures           []Failure
	Skipped            bool
	Anonymous          bool
	Fatal              bool
	Created            bool
	Reused             bool
	GrantsUpserted     int
	GrantsUnchanged    int
	ProjectionsChanged int
	WebhooksEnqueued   int
}

// Grant is one mapped lineage after access is decided. It has no raw store transaction id.
type Grant struct {
	ID                 string
	Line               int
	EntitlementKey     string
	EntitlementID      core.EntitlementID
	ApplicationID      core.ApplicationID
	ProductID          core.ProductID
	OriginStore        string
	Environment        core.Environment
	Access             core.AccessStatus
	Reason             core.AccessReason
	StartsAt           time.Time
	EndsAt             *time.Time
	ObservedAt         time.Time
	LineageFingerprint [32]byte
}
