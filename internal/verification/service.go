// Package verification coordinates provider-neutral purchase verification use cases.
package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// purchaseSubmissionEvidenceKind identifies client evidence persisted by verification.
	purchaseSubmissionEvidenceKind = "purchase_submission"
	// purchaseEvidencePurpose scopes protection of submitted purchase evidence.
	purchaseEvidencePurpose = "purchase_evidence"
	// verifiedArtifactPurpose scopes protection of authoritative provider artifacts.
	verifiedArtifactPurpose = "verified_artifact"
	// providerReferencePurpose scopes protection of sensitive provider identifiers.
	providerReferencePurpose = "provider_reference"

	// entitlementChangedEventType identifies a changed customer entitlement projection.
	entitlementChangedEventType = "entitlement.changed"
	// customerAggregateType identifies customer-scoped outbox event streams.
	customerAggregateType = "customer"
	// entitlementEventSchemaVersion identifies the initial outbox payload contract.
	entitlementEventSchemaVersion = 1
	// outboxIDPrefix distinguishes deterministic outbox identities from other records.
	outboxIDPrefix = "outbox-"
)

// AdapterRegistry resolves the concrete adapter registered for a provider.
type AdapterRegistry interface {
	// Adapter returns the adapter registered for one provider.
	Adapter(core.Provider) (stores.Adapter, error)
}

// Clock supplies trusted receipt timestamps to the verification use case.
type Clock interface {
	// Now returns the current trusted time.
	Now() time.Time
}

// Command describes one application and customer scoped purchase submission.
type Command struct {
	ProjectID                core.ProjectID
	ApplicationID            core.ApplicationID
	ExternalCustomerID       string
	ClaimedProducts          []core.ProviderProductID
	ExpectedCustomerBindings []core.StoreReference
	Evidence                 stores.Evidence
}

// Result contains the verified time and resulting current entitlement snapshot.
type Result struct {
	VerifiedAt   time.Time
	Customer     core.Customer
	Entitlements []persistence.CustomerEntitlement
}

// Service coordinates catalog resolution, provider verification, protection, and persistence.
type Service struct {
	store     persistence.Store
	adapters  AdapterRegistry
	protector protection.Protector
	projector Projector
	clock     Clock
}

// preparedVerification contains protected writes ready for one durable transaction.
type preparedVerification struct {
	evidence     persistence.EvidenceWrite
	artifacts    []persistence.ArtifactWrite
	observations []preparedObservation
}

// preparedObservation pairs one protected observation write with its provider product identity.
type preparedObservation struct {
	write persistence.ObservationWrite
}

// entitlementEventPayload is the stable JSON contract written to the webhook outbox.
type entitlementEventPayload struct {
	SchemaVersion       int                `json:"schema_version"`
	ProjectID           core.ProjectID     `json:"project_id"`
	ApplicationID       core.ApplicationID `json:"application_id"`
	CustomerID          core.CustomerID    `json:"customer_id"`
	EntitlementID       core.EntitlementID `json:"entitlement_id"`
	EntitlementKey      string             `json:"entitlement_key"`
	Access              core.AccessStatus  `json:"access"`
	AccessReason        core.AccessReason  `json:"access_reason"`
	SourceObservationID core.ObservationID `json:"source_observation_id"`
	SourceApplicationID core.ApplicationID `json:"source_application_id"`
	SourceProductID     core.ProductID     `json:"source_product_id"`
	EffectiveStartsAt   *time.Time         `json:"effective_starts_at,omitempty"`
	EffectiveEndsAt     *time.Time         `json:"effective_ends_at,omitempty"`
	Version             int64              `json:"version"`
}

// NewService validates and assembles the provider-neutral verification use case.
func NewService(
	store persistence.Store,
	adapters AdapterRegistry,
	protector protection.Protector,
	projector Projector,
	clock Clock,
) (*Service, error) {
	if store == nil || isNilDependency(store) {
		return nil, errors.New("verification persistence store is required")
	}
	if adapters == nil || isNilDependency(adapters) {
		return nil, errors.New("verification adapter registry is required")
	}
	if protector == nil || isNilDependency(protector) {
		return nil, errors.New("verification protector is required")
	}
	if projector == nil || isNilDependency(projector) {
		return nil, errors.New("verification entitlement projector is required")
	}
	if clock == nil || isNilDependency(clock) {
		return nil, errors.New("verification clock is required")
	}
	return &Service{
		store:     store,
		adapters:  adapters,
		protector: protector,
		projector: projector,
		clock:     clock,
	}, nil
}

// Verify authenticates one purchase with its provider and atomically persists the result.
func (service *Service) Verify(ctx context.Context, command Command) (Result, error) {
	if err := command.Validate(); err != nil {
		return Result{}, validation.Wrap(err)
	}
	receivedAt := service.clock.Now().UTC()
	if receivedAt.IsZero() {
		return Result{}, errors.New("verification clock returned zero time")
	}

	application, customer, err := service.loadScope(ctx, command)
	if err != nil {
		return Result{}, err
	}
	adapter, err := service.adapters.Adapter(application.Store.Provider)
	if err != nil {
		return Result{}, fmt.Errorf("resolve store adapter: %w", err)
	}
	request := stores.VerificationRequest{
		Application:              application,
		CustomerID:               customer.ID,
		ClaimedProducts:          append([]core.ProviderProductID(nil), command.ClaimedProducts...),
		ExpectedCustomerBindings: append([]core.StoreReference(nil), command.ExpectedCustomerBindings...),
		Evidence:                 command.Evidence,
	}
	providerResult, err := adapter.Verify(ctx, request)
	if err != nil {
		return Result{}, fmt.Errorf("verify purchase with %s: %w", application.Store.Provider, err)
	}
	if err := providerResult.ValidateForVerification(request); err != nil {
		return Result{}, fmt.Errorf("validate provider verification result: %w", err)
	}
	var postCommitter stores.PostCommitter
	var postCommitRequest stores.PostCommitRequest
	if len(providerResult.PostCommitActions) > 0 {
		var supported bool
		postCommitter, supported = adapter.(stores.PostCommitter)
		if !supported {
			return Result{}, errors.New("store adapter returned unsupported post-commit actions")
		}
		postCommitRequest = stores.PostCommitRequest{
			Application: application,
			Actions:     clonePostCommitActions(providerResult.PostCommitActions),
		}
		if err := postCommitRequest.Validate(); err != nil {
			return Result{}, fmt.Errorf("validate provider post-commit request: %w", err)
		}
	}

	prepared, err := service.prepare(ctx, command, customer, providerResult, receivedAt)
	if err != nil {
		return Result{}, err
	}
	result, err := service.persist(ctx, command, application, customer, providerResult, prepared)
	if err != nil {
		return Result{}, err
	}
	if postCommitter == nil {
		return result, nil
	}
	if err := postCommitter.PostCommit(ctx, postCommitRequest); err != nil {
		return Result{}, fmt.Errorf("complete purchase with %s: %w", application.Store.Provider, err)
	}
	return result, nil
}

// Validate checks command scope, external customer identity, product claims, bindings, and evidence.
func (command Command) Validate() error {
	if err := errors.Join(
		command.ProjectID.Validate(),
		command.ApplicationID.Validate(),
		validateText("external customer ID", command.ExternalCustomerID),
		command.Evidence.Validate(),
	); err != nil {
		return err
	}

	seenProducts := make(map[core.ProviderProductID]struct{}, len(command.ClaimedProducts))
	for _, productID := range command.ClaimedProducts {
		if err := productID.Validate(); err != nil {
			return err
		}
		if _, duplicate := seenProducts[productID]; duplicate {
			return fmt.Errorf("duplicate claimed provider product ID %q", productID)
		}
		seenProducts[productID] = struct{}{}
	}
	for index, reference := range command.ExpectedCustomerBindings {
		if err := reference.Validate(); err != nil {
			return fmt.Errorf("expected customer binding %d: %w", index, err)
		}
		if reference.Role != core.ReferenceCustomerBinding {
			return fmt.Errorf("expected customer binding %d has role %q", index, reference.Role)
		}
		for previous := 0; previous < index; previous++ {
			if reference.Equal(command.ExpectedCustomerBindings[previous]) {
				return errors.New("duplicate expected customer binding")
			}
		}
	}
	return nil
}

// loadScope resolves the authoritative application and customer before a provider call.
func (service *Service) loadScope(
	ctx context.Context,
	command Command,
) (core.Application, core.Customer, error) {
	var application core.Application
	var customer core.Customer
	err := service.store.Transact(ctx, func(repository persistence.Transaction) error {
		var err error
		application, err = repository.Application(ctx, command.ProjectID, command.ApplicationID)
		if err != nil {
			return err
		}
		customer, err = repository.CustomerByExternalID(ctx, command.ProjectID, command.ExternalCustomerID)
		if err != nil {
			return err
		}
		if len(command.ClaimedProducts) > 0 {
			_, err = repository.CatalogProducts(
				ctx,
				command.ProjectID,
				command.ApplicationID,
				command.ClaimedProducts,
			)
		}
		return err
	})
	if err != nil {
		return core.Application{}, core.Customer{}, fmt.Errorf("resolve verification scope: %w", err)
	}
	return application, customer, nil
}

// prepare protects submitted evidence, verified artifacts, and provider references.
func (service *Service) prepare(
	ctx context.Context,
	command Command,
	customer core.Customer,
	result stores.VerificationResult,
	receivedAt time.Time,
) (preparedVerification, error) {
	protectedEvidence, err := service.protect(
		ctx,
		command.ProjectID,
		command.ApplicationID,
		purchaseEvidencePurpose,
		command.Evidence.Bytes(),
	)
	if err != nil {
		return preparedVerification{}, err
	}
	prepared := preparedVerification{
		evidence: persistence.EvidenceWrite{
			ProjectID:     command.ProjectID,
			ApplicationID: command.ApplicationID,
			CustomerID:    customer.ID,
			Kind:          purchaseSubmissionEvidenceKind,
			ContentType:   command.Evidence.ContentType,
			Payload:       protectedEvidence,
			ReceivedAt:    receivedAt,
		},
		artifacts:    make([]persistence.ArtifactWrite, 0, len(result.Artifacts)),
		observations: make([]preparedObservation, 0, len(result.Observations)),
	}

	for index, artifact := range result.Artifacts {
		protectedArtifact, err := service.protect(
			ctx,
			command.ProjectID,
			command.ApplicationID,
			verifiedArtifactPurpose+":"+artifact.Kind,
			artifact.Evidence.Bytes(),
		)
		if err != nil {
			return preparedVerification{}, fmt.Errorf("protect verified artifact %d: %w", index, err)
		}
		prepared.artifacts = append(prepared.artifacts, persistence.ArtifactWrite{
			Kind:        artifact.Kind,
			ContentType: artifact.Evidence.ContentType,
			Payload:     protectedArtifact,
		})
	}

	for observationIndex, observation := range result.Observations {
		protectedReferences := make([]persistence.ProtectedReference, 0, len(observation.References))
		for referenceIndex, reference := range observation.References {
			protectedReference, err := service.protect(
				ctx,
				command.ProjectID,
				command.ApplicationID,
				providerReferencePurpose+":"+string(reference.Role)+":"+reference.Kind,
				[]byte(reference.Value()),
			)
			if err != nil {
				return preparedVerification{}, fmt.Errorf(
					"protect observation %d reference %d: %w",
					observationIndex,
					referenceIndex,
					err,
				)
			}
			protectedReferences = append(protectedReferences, persistence.ProtectedReference{
				Role:  reference.Role,
				Kind:  reference.Kind,
				Value: protectedReference,
			})
		}
		prepared.observations = append(prepared.observations, preparedObservation{
			write: persistence.ObservationWrite{
				ProjectID:   command.ProjectID,
				CustomerID:  customer.ID,
				Observation: observation,
				References:  protectedReferences,
			},
		})
	}
	return prepared, nil
}

// protect invokes the explicit protection boundary and validates its safe output.
func (service *Service) protect(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	purpose string,
	plaintext []byte,
) (protection.Value, error) {
	request, err := protection.NewRequest(protection.Scope{
		ProjectID:     projectID,
		ApplicationID: applicationID,
		Purpose:       purpose,
	}, plaintext)
	if err != nil {
		return protection.Value{}, err
	}
	protected, err := service.protector.Protect(ctx, request)
	if err != nil {
		return protection.Value{}, fmt.Errorf("protect %s: %w", purpose, err)
	}
	if err := protected.Validate(); err != nil {
		return protection.Value{}, fmt.Errorf("validate protected %s: %w", purpose, err)
	}
	return protected, nil
}

// persist validates current scope and atomically stores one complete verification result.
func (service *Service) persist(
	ctx context.Context,
	command Command,
	application core.Application,
	customer core.Customer,
	providerResult stores.VerificationResult,
	prepared preparedVerification,
) (Result, error) {
	verificationResult := Result{VerifiedAt: providerResult.VerifiedAt, Customer: customer}
	err := service.store.Transact(ctx, func(repository persistence.Transaction) error {
		currentApplication, err := repository.Application(ctx, command.ProjectID, command.ApplicationID)
		if err != nil {
			return err
		}
		currentCustomer, err := repository.CustomerByExternalID(ctx, command.ProjectID, command.ExternalCustomerID)
		if err != nil {
			return err
		}
		if currentApplication != application || currentCustomer != customer {
			return fmt.Errorf("verification scope changed: %w", persistence.ErrConflict)
		}

		providerProductIDs := observationProductIDs(providerResult.Observations)
		catalogProducts, err := repository.CatalogProducts(
			ctx,
			command.ProjectID,
			command.ApplicationID,
			providerProductIDs,
		)
		if err != nil {
			return err
		}
		catalogByProviderID, err := indexCatalogProducts(catalogProducts, providerResult.Observations)
		if err != nil {
			return err
		}

		evidenceID, err := repository.SaveEvidence(ctx, prepared.evidence)
		if err != nil {
			return err
		}
		for _, artifact := range prepared.artifacts {
			artifact.EvidenceID = evidenceID
			if err := repository.SaveArtifact(ctx, artifact); err != nil {
				return err
			}
		}

		candidates := make([]ProjectionCandidate, 0)
		for _, preparedObservation := range prepared.observations {
			observation := preparedObservation.write.Observation
			catalogProduct := catalogByProviderID[observation.ProductID]
			observationWrite := preparedObservation.write
			observationWrite.EvidenceID = evidenceID
			observationWrite.ProductID = catalogProduct.Product.ID
			if err := repository.SaveObservation(ctx, observationWrite); err != nil {
				return err
			}
			for _, entitlement := range catalogProduct.Entitlements {
				candidates = append(candidates, ProjectionCandidate{
					Projection: persistence.EntitlementProjection{
						ProjectID:           command.ProjectID,
						CustomerID:          customer.ID,
						EntitlementID:       entitlement.ID,
						SourceObservationID: observation.ID,
						SourceProductID:     catalogProduct.Product.ID,
						Access:              observation.Access,
						AccessReason:        observation.AccessReason,
						EffectivePeriod:     observation.EffectivePeriod,
					},
					SourceApplicationID: observation.ApplicationID,
					ObservedAt:          observation.ObservedAt,
				})
			}
		}

		currentEntitlements, err := repository.CustomerEntitlements(ctx, command.ProjectID, customer.ID)
		if err != nil {
			return err
		}
		projections, err := service.projector.Project(currentEntitlements, candidates)
		if err != nil {
			return err
		}
		for _, projection := range projections {
			writeResult, err := repository.PutEntitlement(ctx, projection)
			if err != nil {
				return err
			}
			if !writeResult.Changed {
				continue
			}
			event, err := newEntitlementEvent(application.ID, writeResult.Entitlement, providerResult.VerifiedAt)
			if err != nil {
				return err
			}
			if _, err := repository.SaveOutboxEvent(ctx, event); err != nil {
				return err
			}
		}

		verificationResult.Entitlements, err = repository.CustomerEntitlements(
			ctx,
			command.ProjectID,
			customer.ID,
		)
		return err
	})
	if err != nil {
		return Result{}, fmt.Errorf("persist verification result: %w", err)
	}
	return verificationResult, nil
}

// observationProductIDs returns each observed provider product ID once in result order.
func observationProductIDs(observations []core.PurchaseObservation) []core.ProviderProductID {
	productIDs := make([]core.ProviderProductID, 0, len(observations))
	seen := make(map[core.ProviderProductID]struct{}, len(observations))
	for _, observation := range observations {
		if _, exists := seen[observation.ProductID]; exists {
			continue
		}
		seen[observation.ProductID] = struct{}{}
		productIDs = append(productIDs, observation.ProductID)
	}
	return productIDs
}

// clonePostCommitActions defensively copies opaque provider references before adapter execution.
func clonePostCommitActions(actions []stores.PostCommitAction) []stores.PostCommitAction {
	cloned := make([]stores.PostCommitAction, len(actions))
	for index, action := range actions {
		cloned[index] = action
		cloned[index].QueryReferences = append([]core.StoreReference(nil), action.QueryReferences...)
	}
	return cloned
}

// indexCatalogProducts validates product kind consistency and indexes provider mappings.
func indexCatalogProducts(
	catalogProducts []persistence.CatalogProduct,
	observations []core.PurchaseObservation,
) (map[core.ProviderProductID]persistence.CatalogProduct, error) {
	catalogByProviderID := make(map[core.ProviderProductID]persistence.CatalogProduct, len(catalogProducts))
	for _, catalogProduct := range catalogProducts {
		catalogByProviderID[catalogProduct.Mapping.ProviderID] = catalogProduct
	}
	for index, observation := range observations {
		catalogProduct, exists := catalogByProviderID[observation.ProductID]
		if !exists {
			return nil, fmt.Errorf("observation %d product %q: %w", index, observation.ProductID, persistence.ErrNotFound)
		}
		if observation.ProductKind != catalogProduct.Product.Kind {
			return nil, fmt.Errorf(
				"observation %d product kind %q does not match catalog kind %q",
				index,
				observation.ProductKind,
				catalogProduct.Product.Kind,
			)
		}
	}
	return catalogByProviderID, nil
}

// newEntitlementEvent creates a deterministic outbox event for one changed projection.
func newEntitlementEvent(
	applicationID core.ApplicationID,
	entitlement persistence.CustomerEntitlement,
	occurredAt time.Time,
) (persistence.OutboxEvent, error) {
	projection := entitlement.Projection
	payloadContract := entitlementEventPayload{
		SchemaVersion:       entitlementEventSchemaVersion,
		ProjectID:           projection.ProjectID,
		ApplicationID:       applicationID,
		CustomerID:          projection.CustomerID,
		EntitlementID:       projection.EntitlementID,
		EntitlementKey:      entitlement.Key,
		Access:              projection.Access,
		AccessReason:        projection.AccessReason,
		SourceObservationID: projection.SourceObservationID,
		SourceApplicationID: entitlement.SourceApplicationID,
		SourceProductID:     projection.SourceProductID,
		Version:             entitlement.Version,
	}
	if !projection.EffectivePeriod.StartsAt.IsZero() {
		startsAt := projection.EffectivePeriod.StartsAt
		payloadContract.EffectiveStartsAt = &startsAt
	}
	if projection.EffectivePeriod.EndsAt != nil {
		endsAt := *projection.EffectivePeriod.EndsAt
		payloadContract.EffectiveEndsAt = &endsAt
	}
	payload, err := json.Marshal(payloadContract)
	if err != nil {
		return persistence.OutboxEvent{}, fmt.Errorf("marshal entitlement outbox event: %w", err)
	}
	fingerprint := sha256.Sum256(payload)
	return persistence.OutboxEvent{
		ID:                 outboxIDPrefix + hex.EncodeToString(fingerprint[:]),
		ProjectID:          projection.ProjectID,
		ApplicationID:      applicationID,
		EventType:          entitlementChangedEventType,
		AggregateType:      customerAggregateType,
		AggregateID:        string(projection.CustomerID),
		Payload:            payload,
		PayloadFingerprint: fingerprint,
		OccurredAt:         occurredAt,
		AvailableAt:        occurredAt,
	}, nil
}

// validateText enforces non-empty trimmed use-case metadata.
func validateText(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have leading or trailing whitespace", name)
	}
	return nil
}

// isNilDependency detects typed nil values stored inside dependency interfaces.
func isNilDependency(dependency any) bool {
	value := reflect.ValueOf(dependency)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
