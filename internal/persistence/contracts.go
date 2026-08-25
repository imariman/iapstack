// Package persistence defines implementation-neutral durable storage ports.
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/protection"
)

const (
	// APIKeyRoleAdmin grants control-plane access across projects.
	APIKeyRoleAdmin APIKeyRole = "admin"
	// APIKeyRoleApplication grants data-plane access to one application.
	APIKeyRoleApplication APIKeyRole = "application"

	// QueueInbox identifies protected provider notification work.
	QueueInbox QueueName = "inbox"
	// QueueOutbox identifies application webhook delivery work.
	QueueOutbox QueueName = "outbox"
	// QueueReconciliation identifies protected provider reconciliation work.
	QueueReconciliation QueueName = "reconciliation"
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

// OperationsStore executes control-plane and durable queue work in atomic units.
type OperationsStore interface {
	// Operate executes one operations callback in an atomic unit of work.
	Operate(context.Context, OperationsFunc) error
}

// OperationsTransaction combines control-plane and queue repositories.
type OperationsTransaction interface {
	CatalogRepository
	CatalogWriter
	OperationsRepository
	QueueRepository
}

// OperationsFunc performs operational durable work that must commit or roll back together.
type OperationsFunc func(OperationsTransaction) error

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

// CatalogWriter creates idempotent control-plane catalog records.
type CatalogWriter interface {
	// PutProject creates one project or confirms an identical existing identity.
	PutProject(context.Context, core.ProjectID) error
	// PutApplication creates one provider application or confirms identical configuration.
	PutApplication(context.Context, core.Application) error
	// PutCustomer creates one external customer identity or returns its existing record.
	PutCustomer(context.Context, core.Customer) (core.Customer, error)
	// PutEntitlementDefinition creates one named entitlement.
	PutEntitlementDefinition(context.Context, core.Entitlement) error
	// PutProduct creates one product and its entitlement grants.
	PutProduct(context.Context, core.Product) error
	// PutStoreProduct creates one application provider-product mapping.
	PutStoreProduct(context.Context, core.ProjectID, core.StoreProduct) error
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

// OperationsRepository stores API identities and protected webhook configuration.
type OperationsRepository interface {
	// APIKey returns one non-revoked API key verifier by public identity.
	APIKey(context.Context, string) (APIKeyRecord, error)
	// APIKeys returns a bounded secret-free API key lifecycle collection.
	APIKeys(context.Context) ([]APIKeySummary, error)
	// PutAPIKey creates one API key verifier without storing the bearer secret.
	PutAPIKey(context.Context, APIKeyRecord) error
	// RevokeAPIKey idempotently disables one key while preserving the final active administrator.
	RevokeAPIKey(context.Context, string, time.Time) (APIKeySummary, error)
	// PutWebhookEndpoint creates or rotates one protected application webhook endpoint.
	PutWebhookEndpoint(context.Context, WebhookEndpointWrite) (WebhookEndpointRecord, error)
	// WebhookEndpoint returns protected delivery configuration for one application.
	WebhookEndpoint(context.Context, core.ProjectID, core.ApplicationID) (WebhookEndpointRecord, error)
}

// QueueRepository stores durable job payloads and their terminal audit outcomes.
type QueueRepository interface {
	// SaveInboxMessage stores one protected notification idempotently.
	SaveInboxMessage(context.Context, InboxMessage) (string, error)
	// SaveReconciliationJob stores one protected reconciliation request idempotently.
	SaveReconciliationJob(context.Context, ReconciliationJob) (string, error)
	// QueueMessage returns one durable payload by its fixed queue and record identity.
	QueueMessage(context.Context, QueueName, string) (QueueMessage, error)
	// CompleteQueue records successful processing or delivery idempotently.
	CompleteQueue(context.Context, QueueCompletion) error
	// FailQueue records one terminal processing or delivery failure idempotently.
	FailQueue(context.Context, QueueFailure) error
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

// APIKeyRole identifies one stable authorization boundary.
type APIKeyRole string

// APIKeyRecord stores a public key identity and memory-hard secret verifier.
type APIKeyRecord struct {
	ID            string
	Role          APIKeyRole
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	SecretSalt    [16]byte
	SecretHash    [32]byte
	CreatedAt     time.Time
}

// APIKeySummary contains lifecycle metadata without a bearer secret or verifier.
type APIKeySummary struct {
	ID            string
	Role          APIKeyRole
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	CreatedAt     time.Time
	RevokedAt     *time.Time
}

// WebhookEndpointWrite describes protected application delivery configuration.
type WebhookEndpointWrite struct {
	ProjectID        core.ProjectID
	ApplicationID    core.ApplicationID
	URL              string
	Secret           protection.Value
	ExpectedRevision int64
}

// WebhookEndpointRecord contains protected delivery configuration and revision metadata.
type WebhookEndpointRecord struct {
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	URL           string
	Secret        protection.Value
	Revision      int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// QueueName identifies one fixed durable queue table.
type QueueName string

// InboxMessage describes one protected provider notification for durable insertion.
type InboxMessage struct {
	ID            string
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	Provider      core.Provider
	Kind          string
	ContentType   string
	Payload       protection.Value
	ReceivedAt    time.Time
	AvailableAt   time.Time
}

// ReconciliationJob describes one protected provider query scheduled for a customer.
type ReconciliationJob struct {
	ID            string
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	CustomerID    core.CustomerID
	Payload       protection.Value
	AvailableAt   time.Time
}

// QueueMessage is one durable inbox, outbox, or reconciliation payload.
type QueueMessage struct {
	Queue            QueueName
	ID               string
	ProjectID        core.ProjectID
	ApplicationID    core.ApplicationID
	CustomerID       core.CustomerID
	Provider         core.Provider
	Kind             string
	ContentType      string
	ProtectedPayload protection.Value
	JSONPayload      json.RawMessage
	OccurredAt       time.Time
	Completed        bool
	Failed           bool
}

// QueueCompletion records one successful terminal job outcome.
type QueueCompletion struct {
	Queue       QueueName
	ID          string
	CompletedAt time.Time
}

// QueueFailure records one permanent terminal job outcome.
type QueueFailure struct {
	Queue     QueueName
	ID        string
	FailedAt  time.Time
	ErrorCode string
}

var (
	// ErrNotFound indicates that a requested durable record does not exist in scope.
	ErrNotFound = errors.New("persistence record not found")
	// ErrConflict indicates that an identity or idempotency key belongs to different data.
	ErrConflict = errors.New("persistence conflict")
	// ErrUnavailable indicates that durable storage could not complete a transient operation safely.
	ErrUnavailable = errors.New("persistence unavailable")
)

// Validate checks API key identity, role scope, verifier bytes, and creation time.
func (record APIKeyRecord) Validate() error {
	var verifierError error
	if record.SecretSalt == ([16]byte{}) || record.SecretHash == ([32]byte{}) {
		verifierError = errors.New("API key verifier is required")
	}
	return errors.Join(
		validateText("API key ID", record.ID),
		validateAPIKeyScope(record.Role, record.ProjectID, record.ApplicationID),
		verifierError,
		validateTime("API key creation time", record.CreatedAt),
	)
}

// Validate checks secret-free API key scope and lifecycle timestamps.
func (summary APIKeySummary) Validate() error {
	var lifecycleError error
	if summary.RevokedAt != nil {
		if summary.RevokedAt.IsZero() {
			lifecycleError = errors.New("API key revocation time must not be zero")
		} else if !summary.CreatedAt.IsZero() && summary.RevokedAt.Before(summary.CreatedAt) {
			lifecycleError = errors.New("API key revocation time cannot precede creation")
		}
	}
	return errors.Join(
		validateText("API key ID", summary.ID),
		validateAPIKeyScope(summary.Role, summary.ProjectID, summary.ApplicationID),
		validateTime("API key creation time", summary.CreatedAt),
		lifecycleError,
	)
}

// Validate checks webhook scope, HTTPS URL, protected secret, and optimistic revision.
func (write WebhookEndpointWrite) Validate() error {
	return errors.Join(
		write.ProjectID.Validate(),
		write.ApplicationID.Validate(),
		validateHTTPSURL(write.URL),
		write.Secret.Validate(),
		validateNonNegative("webhook expected revision", write.ExpectedRevision),
	)
}

// Validate checks stored webhook configuration and durable revision metadata.
func (record WebhookEndpointRecord) Validate() error {
	var revisionError error
	if record.Revision <= 0 {
		revisionError = errors.New("webhook revision must be positive")
	}
	if !record.UpdatedAt.IsZero() && !record.CreatedAt.IsZero() && record.UpdatedAt.Before(record.CreatedAt) {
		revisionError = errors.Join(revisionError, errors.New("webhook update time cannot precede creation"))
	}
	return errors.Join(
		record.ProjectID.Validate(),
		record.ApplicationID.Validate(),
		validateHTTPSURL(record.URL),
		record.Secret.Validate(),
		revisionError,
		validateTime("webhook creation time", record.CreatedAt),
		validateTime("webhook update time", record.UpdatedAt),
	)
}

// Validate checks notification scope, protected payload, metadata, and schedule.
func (message InboxMessage) Validate() error {
	var scheduleError error
	if !message.AvailableAt.IsZero() && !message.ReceivedAt.IsZero() && message.AvailableAt.Before(message.ReceivedAt) {
		scheduleError = errors.New("inbox availability cannot precede receipt")
	}
	return errors.Join(
		validateText("inbox message ID", message.ID),
		message.ProjectID.Validate(),
		message.ApplicationID.Validate(),
		message.Provider.Validate(),
		validateText("inbox kind", message.Kind),
		validateContentType("inbox content type", message.ContentType),
		message.Payload.Validate(),
		validateTime("inbox receipt time", message.ReceivedAt),
		validateTime("inbox availability time", message.AvailableAt),
		scheduleError,
	)
}

// Validate checks reconciliation scope, protected query payload, and schedule.
func (job ReconciliationJob) Validate() error {
	return errors.Join(
		validateText("reconciliation job ID", job.ID),
		job.ProjectID.Validate(),
		job.ApplicationID.Validate(),
		job.CustomerID.Validate(),
		job.Payload.Validate(),
		validateTime("reconciliation availability time", job.AvailableAt),
	)
}

// Validate checks one fixed durable queue name.
func (name QueueName) Validate() error {
	switch name {
	case QueueInbox, QueueOutbox, QueueReconciliation:
		return nil
	default:
		return fmt.Errorf("unsupported queue %q", name)
	}
}

// Validate checks one successful queue outcome.
func (completion QueueCompletion) Validate() error {
	return errors.Join(
		completion.Queue.Validate(),
		validateText("queue record ID", completion.ID),
		validateTime("queue completion time", completion.CompletedAt),
	)
}

// Validate checks one failed queue outcome and its safe error code.
func (failure QueueFailure) Validate() error {
	return errors.Join(
		failure.Queue.Validate(),
		validateText("queue record ID", failure.ID),
		validateTime("queue failure time", failure.FailedAt),
		validateText("queue error code", failure.ErrorCode),
	)
}

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

// validateHTTPSURL accepts only absolute HTTPS webhook destinations without embedded credentials.
func validateHTTPSURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("webhook URL must be an absolute HTTPS URL without user information")
	}
	return nil
}

// validateAPIKeyScope enforces role-specific project and application ownership.
func validateAPIKeyScope(role APIKeyRole, projectID core.ProjectID, applicationID core.ApplicationID) error {
	switch role {
	case APIKeyRoleAdmin:
		if projectID != "" || applicationID != "" {
			return errors.New("admin API key must not have application scope")
		}
		return nil
	case APIKeyRoleApplication:
		return errors.Join(projectID.Validate(), applicationID.Validate())
	default:
		return fmt.Errorf("unsupported API key role %q", role)
	}
}

// validateNonNegative checks optimistic revision inputs that allow zero for creation.
func validateNonNegative(name string, value int64) error {
	if value < 0 {
		return fmt.Errorf("%s must not be negative", name)
	}
	return nil
}
