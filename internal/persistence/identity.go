package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// CustomerMatchPrimary means the external id is the canonical customer's own external id.
	CustomerMatchPrimary = "primary"
	// CustomerMatchAlias means the external id is an additional alias of the canonical customer.
	CustomerMatchAlias = "alias"
	// CustomerMatchMerged means the external id still belongs to a customer merged into the canonical customer.
	CustomerMatchMerged = "merged"
)

// IdentityRepository stores canonical customer aliases and append-only merges.
type IdentityRepository interface {
	// ResolveCustomer maps one external id to its canonical customer inside one project.
	ResolveCustomer(context.Context, core.ProjectID, string) (CustomerResolution, error)
	// PutCustomerAlias attaches one external id to a canonical customer idempotently.
	PutCustomerAlias(context.Context, CustomerAliasWrite) (CustomerResolution, error)
	// MergeCustomers records one customer merge or returns the stored audit row.
	MergeCustomers(context.Context, CustomerMergeWrite) (CustomerMergeResult, error)
}

// CustomerResolution is the canonical owner of one external customer id.
type CustomerResolution struct {
	ProjectID           core.ProjectID
	ExternalID          string
	CanonicalCustomerID core.CustomerID
	Match               string
}

// CustomerAliasWrite attaches one external id that is not already a customer primary id.
type CustomerAliasWrite struct {
	ProjectID           core.ProjectID
	CanonicalCustomerID core.CustomerID
	ExternalID          string
	ActorRole           APIKeyRole
	ActorKeyID          string
}

// CustomerMergeWrite requests one append-only merge of a source customer into a canonical customer.
type CustomerMergeWrite struct {
	ProjectID           core.ProjectID
	CanonicalCustomerID core.CustomerID
	SourceCustomerID    core.CustomerID
	IdempotencyKey      string
	ActorKeyID          string
}

// CustomerMerge is one stored merge audit row.
type CustomerMerge struct {
	ID                  string
	ProjectID           core.ProjectID
	CanonicalCustomerID core.CustomerID
	SourceCustomerID    core.CustomerID
	IdempotencyKey      string
	CreatedAt           time.Time
}

// CustomerMergeResult is a newly stored merge or a safe replay of the original audit row.
type CustomerMergeResult struct {
	Merge  CustomerMerge
	Replay bool
}

// Validate checks a resolved canonical customer identity.
func (resolution CustomerResolution) Validate() error {
	return errors.Join(
		resolution.ProjectID.Validate(),
		core.ValidateExternalCustomerID(resolution.ExternalID),
		resolution.CanonicalCustomerID.Validate(),
		validateCustomerMatch(resolution.Match),
	)
}

// Validate checks an alias write before it reaches PostgreSQL.
func (write CustomerAliasWrite) Validate() error {
	return errors.Join(
		write.ProjectID.Validate(),
		write.CanonicalCustomerID.Validate(),
		core.ValidateExternalCustomerID(write.ExternalID),
		validateAliasActor(write.ActorRole, write.ActorKeyID),
	)
}

// Validate checks a merge write before it reaches PostgreSQL.
func (write CustomerMergeWrite) Validate() error {
	return errors.Join(
		write.ProjectID.Validate(),
		write.CanonicalCustomerID.Validate(),
		write.SourceCustomerID.Validate(),
		core.ValidateOpaqueIdentifier("merge idempotency key", write.IdempotencyKey),
		core.ValidateOpaqueIdentifier("actor key ID", write.ActorKeyID),
	)
}

// Validate checks one stored merge audit row.
func (merge CustomerMerge) Validate() error {
	return errors.Join(
		core.ValidateOpaqueIdentifier("customer merge ID", merge.ID),
		merge.ProjectID.Validate(),
		merge.CanonicalCustomerID.Validate(),
		merge.SourceCustomerID.Validate(),
		core.ValidateOpaqueIdentifier("merge idempotency key", merge.IdempotencyKey),
		validateTime("customer merge creation time", merge.CreatedAt),
	)
}

// validateCustomerMatch accepts only the three public resolution outcomes.
func validateCustomerMatch(match string) error {
	switch match {
	case CustomerMatchPrimary, CustomerMatchAlias, CustomerMatchMerged:
		return nil
	default:
		return fmt.Errorf("unsupported customer match %q", match)
	}
}

// validateAliasActor requires a stored admin or application key.
func validateAliasActor(role APIKeyRole, keyID string) error {
	switch role {
	case APIKeyRoleAdmin, APIKeyRoleApplication:
	default:
		return fmt.Errorf("unsupported customer alias actor role %q", role)
	}
	return core.ValidateOpaqueIdentifier("actor key ID", keyID)
}
