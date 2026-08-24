// Package persistence defines implementation-neutral durable storage ports.
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/protection"
)

// Store owns durable storage resources and starts atomic units of work.
type Store interface {
	// Ping verifies that the durable store is reachable.
	Ping(context.Context) error
	// Transact executes one callback in an atomic unit of work.
	Transact(context.Context, TransactionFunc) error
	// Close releases resources owned by the durable store.
	Close()
}

// Transaction combines the repositories available inside one atomic unit of work.
type Transaction interface {
	CatalogRepository
	CredentialRepository
	PurchaseRepository
	EntitlementRepository
	OutboxRepository
}

// CredentialRepository stores and resolves protected application credential packages.
type CredentialRepository interface {
	// Credential returns one protected credential in its project and application scope.
	Credential(context.Context, CredentialKey) (CredentialRecord, error)
	// PutCredential creates or rotates one credential with optimistic revision control.
	PutCredential(context.Context, CredentialWrite) (CredentialRecord, error)
}

// TransactionFunc performs durable operations that must commit or roll back together.
type TransactionFunc func(Transaction) error

// CatalogRepository resolves application, customer, product, and entitlement configuration.
type CatalogRepository interface {
	// Application returns one application inside its owning project.
	Application(context.Context, core.ProjectID, core.ApplicationID) (core.Application, error)
	// CustomerByExternalID returns one customer by its project-scoped external identity.
	CustomerByExternalID(context.Context, core.ProjectID, string) (core.Customer, error)
	// CatalogProducts resolves provider product mappings and their granted entitlements.
	CatalogProducts(
		context.Context,
		core.ProjectID,
		core.ApplicationID,
		[]core.ProviderProductID,
	) ([]CatalogProduct, error)
}

// PurchaseRepository stores encrypted evidence and immutable normalized observations.
type PurchaseRepository interface {
	// SaveEvidence stores client evidence idempotently and returns its durable identity.
	SaveEvidence(context.Context, EvidenceWrite) (int64, error)
	// SaveArtifact stores one encrypted provider artifact idempotently.
	SaveArtifact(context.Context, ArtifactWrite) error
	// SaveObservation stores one immutable observation and its protected references idempotently.
	SaveObservation(context.Context, ObservationWrite) error
}

// EntitlementRepository stores and reads current customer entitlement projections.
type EntitlementRepository interface {
	// PutEntitlement creates or replaces one current projection with optimistic versioning.
	PutEntitlement(context.Context, EntitlementProjection) (EntitlementWriteResult, error)
	// CustomerEntitlements returns the current project-scoped entitlement snapshot.
	CustomerEntitlements(context.Context, core.ProjectID, core.CustomerID) ([]CustomerEntitlement, error)
}

// OutboxRepository appends application events to the durable delivery queue.
type OutboxRepository interface {
	// SaveOutboxEvent stores one logical event idempotently and returns its durable identity.
	SaveOutboxEvent(context.Context, OutboxEvent) (string, error)
}

// CatalogProduct joins one provider product mapping to its internal product and entitlements.
type CatalogProduct struct {
	Mapping      core.StoreProduct
	Product      core.Product
	Entitlements []core.Entitlement
}

// CredentialKey identifies one provider-owned credential package within an application.
type CredentialKey struct {
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	Kind          string
}

// CredentialWrite describes one protected credential creation or rotation attempt.
type CredentialWrite struct {
	CredentialKey
	ContentType      string
	SchemaVersion    int
	Payload          protection.Value
	ExpectedRevision int64
}

// CredentialRecord contains protected credential data and durable revision metadata.
type CredentialRecord struct {
	CredentialKey
	ContentType   string
	SchemaVersion int
	Payload       protection.Value
	Revision      int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// EvidenceWrite describes encrypted purchase evidence received from a client or provider.
type EvidenceWrite struct {
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	CustomerID    core.CustomerID
	Kind          string
	ContentType   string
	Payload       protection.Value
	ReceivedAt    time.Time
}

// ArtifactWrite describes an encrypted artifact verified by a provider adapter.
type ArtifactWrite struct {
	EvidenceID  int64
	Kind        string
	ContentType string
	Payload     protection.Value
}

// ProtectedReference contains non-secret reference metadata and an encrypted value.
type ProtectedReference struct {
	Role  core.ReferenceRole
	Kind  string
	Value protection.Value
}

// ObservationWrite binds a normalized observation to its internal catalog and protected references.
type ObservationWrite struct {
	EvidenceID  int64
	ProjectID   core.ProjectID
	CustomerID  core.CustomerID
	ProductID   core.ProductID
	Observation core.PurchaseObservation
	References  []ProtectedReference
}

// EntitlementProjection describes the current access decision derived from one observation.
type EntitlementProjection struct {
	ProjectID           core.ProjectID
	CustomerID          core.CustomerID
	EntitlementID       core.EntitlementID
	SourceObservationID core.ObservationID
	SourceProductID     core.ProductID
	Access              core.AccessStatus
	AccessReason        core.AccessReason
	EffectivePeriod     core.EffectivePeriod
}

// CustomerEntitlement is a versioned current projection returned to application use cases.
type CustomerEntitlement struct {
	Projection          EntitlementProjection
	SourceApplicationID core.ApplicationID
	Key                 string
	Version             int64
}

// EntitlementWriteResult reports the durable projection and whether it changed.
type EntitlementWriteResult struct {
	Entitlement CustomerEntitlement
	Changed     bool
}

// OutboxEvent describes one JSON object queued for application delivery.
type OutboxEvent struct {
	ID                 string
	ProjectID          core.ProjectID
	ApplicationID      core.ApplicationID
	EventType          string
	AggregateType      string
	AggregateID        string
	Payload            json.RawMessage
	PayloadFingerprint [32]byte
	OccurredAt         time.Time
	AvailableAt        time.Time
}

var (
	// ErrNotFound indicates that a requested durable record does not exist in scope.
	ErrNotFound = errors.New("persistence record not found")
	// ErrConflict indicates that an identity or idempotency key belongs to different data.
	ErrConflict = errors.New("persistence conflict")
)

// Validate checks the project, application, and provider-owned credential kind.
func (key CredentialKey) Validate() error {
	return errors.Join(
		key.ProjectID.Validate(),
		key.ApplicationID.Validate(),
		validateText("credential kind", key.Kind),
	)
}

// Validate checks protected credential metadata and optimistic revision input.
func (write CredentialWrite) Validate() error {
	var schemaVersionError error
	if write.SchemaVersion <= 0 {
		schemaVersionError = errors.New("credential schema version must be positive")
	}
	var revisionError error
	if write.ExpectedRevision < 0 {
		revisionError = errors.New("credential expected revision must not be negative")
	}
	return errors.Join(
		write.CredentialKey.Validate(),
		validateContentType("credential content type", write.ContentType),
		schemaVersionError,
		write.Payload.Validate(),
		revisionError,
	)
}

// Validate checks stored credential metadata, protected data, revision, and timestamps.
func (record CredentialRecord) Validate() error {
	var schemaVersionError error
	if record.SchemaVersion <= 0 {
		schemaVersionError = errors.New("credential schema version must be positive")
	}
	var revisionError error
	if record.Revision <= 0 {
		revisionError = errors.New("credential revision must be positive")
	}
	if !record.CreatedAt.IsZero() && !record.UpdatedAt.IsZero() && record.UpdatedAt.Before(record.CreatedAt) {
		revisionError = errors.Join(revisionError, errors.New("credential update time cannot precede creation"))
	}
	return errors.Join(
		record.CredentialKey.Validate(),
		validateContentType("credential content type", record.ContentType),
		schemaVersionError,
		record.Payload.Validate(),
		revisionError,
		validateTime("credential creation time", record.CreatedAt),
		validateTime("credential update time", record.UpdatedAt),
	)
}

// Clone returns a defensive copy of protected credential bytes.
func (record CredentialRecord) Clone() CredentialRecord {
	record.Payload = record.Payload.Clone()
	return record
}

// Validate checks evidence scope, metadata, protected payload, and receipt time.
func (write EvidenceWrite) Validate() error {
	return errors.Join(
		write.ProjectID.Validate(),
		write.ApplicationID.Validate(),
		write.CustomerID.Validate(),
		validateText("evidence kind", write.Kind),
		validateText("evidence content type", write.ContentType),
		write.Payload.Validate(),
		validateTime("evidence receipt time", write.ReceivedAt),
	)
}

// Validate checks artifact ownership, metadata, and protected payload.
func (write ArtifactWrite) Validate() error {
	var evidenceError error
	if write.EvidenceID <= 0 {
		evidenceError = errors.New("artifact evidence ID must be positive")
	}
	return errors.Join(
		evidenceError,
		validateText("artifact kind", write.Kind),
		validateText("artifact content type", write.ContentType),
		write.Payload.Validate(),
	)
}

// Validate checks protected reference metadata and encrypted value.
func (reference ProtectedReference) Validate() error {
	return errors.Join(
		validateText("reference role", string(reference.Role)),
		validateText("reference kind", reference.Kind),
		reference.Value.Validate(),
	)
}

// Validate checks observation scope, catalog identity, domain invariants, and protected references.
func (write ObservationWrite) Validate() error {
	var evidenceError error
	if write.EvidenceID <= 0 {
		evidenceError = errors.New("observation evidence ID must be positive")
	}
	if err := errors.Join(
		evidenceError,
		write.ProjectID.Validate(),
		write.CustomerID.Validate(),
		write.ProductID.Validate(),
		write.Observation.Validate(),
	); err != nil {
		return err
	}
	if len(write.References) != len(write.Observation.References) {
		return errors.New("every observation reference must have one protected value")
	}
	for index, reference := range write.References {
		if err := reference.Validate(); err != nil {
			return fmt.Errorf("protected reference %d: %w", index, err)
		}
		domainReference := write.Observation.References[index]
		if reference.Role != domainReference.Role || reference.Kind != domainReference.Kind {
			return fmt.Errorf("protected reference %d metadata does not match observation", index)
		}
	}
	return nil
}

// Validate checks projection scope, source identity, access decision, and effective period.
func (projection EntitlementProjection) Validate() error {
	return errors.Join(
		projection.ProjectID.Validate(),
		projection.CustomerID.Validate(),
		projection.EntitlementID.Validate(),
		projection.SourceObservationID.Validate(),
		projection.SourceProductID.Validate(),
		projection.Access.Validate(),
		projection.AccessReason.Validate(),
		projection.EffectivePeriod.Validate(),
	)
}

// Validate checks event scope, identity, JSON payload, fingerprint, and schedule.
func (event OutboxEvent) Validate() error {
	var payloadObject map[string]json.RawMessage
	payloadError := json.Unmarshal(event.Payload, &payloadObject)
	if payloadError == nil && payloadObject == nil {
		payloadError = errors.New("outbox payload must be a JSON object")
	}
	if event.PayloadFingerprint == ([32]byte{}) {
		payloadError = errors.Join(payloadError, errors.New("outbox payload fingerprint is required"))
	}
	if !event.AvailableAt.IsZero() && !event.OccurredAt.IsZero() && event.AvailableAt.Before(event.OccurredAt) {
		payloadError = errors.Join(payloadError, errors.New("outbox availability cannot precede occurrence"))
	}
	return errors.Join(
		validateText("outbox event ID", event.ID),
		event.ProjectID.Validate(),
		event.ApplicationID.Validate(),
		validateText("outbox event type", event.EventType),
		validateText("outbox aggregate type", event.AggregateType),
		validateText("outbox aggregate ID", event.AggregateID),
		payloadError,
		validateTime("outbox occurrence time", event.OccurredAt),
		validateTime("outbox availability time", event.AvailableAt),
	)
}

// validateText enforces the database-safe shape shared by persistence metadata.
func validateText(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have leading or trailing whitespace", name)
	}
	return nil
}

// validateContentType enforces a non-empty, trimmed media type at persistence boundaries.
func validateContentType(name, value string) error {
	if err := validateText(name, value); err != nil {
		return err
	}
	if _, _, err := mime.ParseMediaType(value); err != nil {
		return fmt.Errorf("%s must be a valid media type", name)
	}
	return nil
}

// validateTime rejects missing timestamps at persistence boundaries.
func validateTime(name string, value time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}
