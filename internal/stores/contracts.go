package stores

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

type VerificationRequest struct {
	Application              core.Application
	CustomerID               core.CustomerID
	ClaimedProducts          []core.ProviderProductID
	ExpectedCustomerBindings []core.StoreReference
	Evidence                 Evidence
}

type ReconciliationRequest struct {
	Application              core.Application
	CustomerID               core.CustomerID
	ExpectedProducts         []core.ProviderProductID
	ExpectedProductKind      core.ProductKind
	ExpectedCustomerBindings []core.StoreReference
	QueryReferences          []core.StoreReference
}

// PostCommitAction describes one provider operation that is safe only after durable entitlement persistence.
type PostCommitAction struct {
	Kind            string
	ProductID       core.ProviderProductID
	ProductKind     core.ProductKind
	QueryReferences []core.StoreReference
}

// PostCommitRequest scopes provider operations to one authoritative application.
type PostCommitRequest struct {
	Application core.Application
	Actions     []PostCommitAction
}

type VerificationResult struct {
	VerifiedAt        time.Time
	Artifacts         []VerifiedArtifact
	Observations      []core.PurchaseObservation
	PostCommitActions []PostCommitAction
}

type Verifier interface {
	// Verify authenticates purchase evidence and returns normalized observations.
	Verify(context.Context, VerificationRequest) (VerificationResult, error)
}

type Reconciler interface {
	// Reconcile queries authoritative provider state from stored purchase references.
	Reconcile(context.Context, ReconciliationRequest) (VerificationResult, error)
}

// PostCommitter executes provider operations after verified state is durably committed.
type PostCommitter interface {
	// PostCommit completes provider actions that correspond to an already persisted result.
	PostCommit(context.Context, PostCommitRequest) error
}

type Adapter interface {
	// Provider identifies the purchase provider implemented by the adapter.
	Provider() core.Provider
	Verifier
	Reconciler
}

// Validate checks a post-commit action's kind, product identity, and opaque query references.
func (action PostCommitAction) Validate() error {
	if action.Kind == "" {
		return errors.New("post-commit action kind is required")
	}
	if strings.TrimSpace(action.Kind) != action.Kind {
		return errors.New("post-commit action kind must not have leading or trailing whitespace")
	}
	if err := errors.Join(action.ProductID.Validate(), action.ProductKind.Validate()); err != nil {
		return err
	}
	if len(action.QueryReferences) == 0 {
		return errors.New("post-commit action requires at least one query reference")
	}
	return validateReferencesWithRole(action.QueryReferences, core.ReferenceQuery)
}

// Validate checks a post-commit request's application scope and action set.
func (request PostCommitRequest) Validate() error {
	if err := request.Application.Validate(); err != nil {
		return err
	}
	if len(request.Actions) == 0 {
		return errors.New("post-commit request requires at least one action")
	}
	for index, action := range request.Actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("post-commit action %d: %w", index, err)
		}
		for previous := 0; previous < index; previous++ {
			if samePostCommitAction(action, request.Actions[previous]) {
				return fmt.Errorf("duplicate post-commit action %d", index)
			}
		}
	}
	return nil
}

// Validate checks verification scope, customer identity, claims, bindings, and evidence.
func (request VerificationRequest) Validate() error {
	if err := errors.Join(
		request.Application.Validate(),
		request.CustomerID.Validate(),
		request.Evidence.Validate(),
	); err != nil {
		return err
	}
	if err := validateProductSet(request.ClaimedProducts); err != nil {
		return err
	}
	return validateReferencesWithRole(request.ExpectedCustomerBindings, core.ReferenceCustomerBinding)
}

// Validate checks reconciliation scope, expected products, bindings, and query references.
func (request ReconciliationRequest) Validate() error {
	if err := errors.Join(
		request.Application.Validate(),
		request.CustomerID.Validate(),
		request.ExpectedProductKind.Validate(),
	); err != nil {
		return err
	}
	if err := validateProductSet(request.ExpectedProducts); err != nil {
		return err
	}
	if err := validateReferencesWithRole(request.ExpectedCustomerBindings, core.ReferenceCustomerBinding); err != nil {
		return err
	}
	if len(request.QueryReferences) == 0 {
		return errors.New("reconciliation requires at least one query reference")
	}
	return validateReferencesWithRole(request.QueryReferences, core.ReferenceQuery)
}

// ValidateForVerification checks a result against its original verification request.
func (result VerificationResult) ValidateForVerification(request VerificationRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("verification request: %w", err)
	}
	return result.validate(
		request.Application,
		request.ClaimedProducts,
		request.ExpectedCustomerBindings,
		nil,
	)
}

// ValidateForReconciliation checks a result against its reconciliation request.
func (result VerificationResult) ValidateForReconciliation(request ReconciliationRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("reconciliation request: %w", err)
	}
	if err := result.validate(
		request.Application,
		request.ExpectedProducts,
		request.ExpectedCustomerBindings,
		request.QueryReferences,
	); err != nil {
		return err
	}
	for index, observation := range result.Observations {
		if observation.ProductKind != request.ExpectedProductKind {
			return fmt.Errorf("observation %d has unexpected product kind %q", index, observation.ProductKind)
		}
	}
	return nil
}

// validate enforces shared result, scope, product, binding, and reference invariants.
func (result VerificationResult) validate(
	application core.Application,
	expectedProducts []core.ProviderProductID,
	expectedCustomerBindings []core.StoreReference,
	expectedQueryReferences []core.StoreReference,
) error {
	if result.VerifiedAt.IsZero() {
		return errors.New("verification time is required")
	}
	if len(result.Observations) == 0 {
		return errors.New("verification result requires at least one observation")
	}
	if len(result.Artifacts) == 0 {
		return errors.New("verification result requires at least one verified artifact")
	}
	for i, artifact := range result.Artifacts {
		if err := artifact.Validate(); err != nil {
			return fmt.Errorf("verified artifact %d: %w", i, err)
		}
	}

	observedProducts := make(map[core.ProviderProductID]struct{}, len(result.Observations))
	observedIDs := make(map[core.ObservationID]struct{}, len(result.Observations))
	for i, observation := range result.Observations {
		if err := observation.Validate(); err != nil {
			return fmt.Errorf("observation %d: %w", i, err)
		}
		if observation.ApplicationID != application.ID {
			return fmt.Errorf("observation %d belongs to a different application", i)
		}
		if observation.Store != application.Store {
			return fmt.Errorf("observation %d belongs to a different provider application scope", i)
		}
		if _, duplicate := observedIDs[observation.ID]; duplicate {
			return fmt.Errorf("duplicate observation ID %q", observation.ID)
		}
		observedIDs[observation.ID] = struct{}{}
		observedProducts[observation.ProductID] = struct{}{}
		if len(expectedCustomerBindings) > 0 && !hasMatchingReference(
			observation.ReferencesFor(core.ReferenceCustomerBinding),
			expectedCustomerBindings,
		) {
			return fmt.Errorf("observation %d does not match an expected customer binding", i)
		}
		if len(expectedQueryReferences) > 0 && !hasMatchingReference(
			observation.ReferencesFor(core.ReferenceQuery),
			expectedQueryReferences,
		) {
			return fmt.Errorf("observation %d does not match a reconciliation query reference", i)
		}
	}
	for index, action := range result.PostCommitActions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("post-commit action %d: %w", index, err)
		}
		matched := false
		for _, observation := range result.Observations {
			if action.ProductID == observation.ProductID &&
				action.ProductKind == observation.ProductKind &&
				hasMatchingReference(action.QueryReferences, observation.ReferencesFor(core.ReferenceQuery)) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("post-commit action %d does not match an observation", index)
		}
		for previous := 0; previous < index; previous++ {
			if samePostCommitAction(action, result.PostCommitActions[previous]) {
				return fmt.Errorf("duplicate post-commit action %d", index)
			}
		}
	}

	if len(expectedProducts) == 0 {
		return nil
	}
	if len(observedProducts) != len(expectedProducts) {
		return errors.New("observed products do not match expected products")
	}
	for _, expectedProduct := range expectedProducts {
		if _, observed := observedProducts[expectedProduct]; !observed {
			return fmt.Errorf("expected product %q was not observed", expectedProduct)
		}
	}
	return nil
}

// samePostCommitAction reports whether two actions target the same provider operation and opaque references.
func samePostCommitAction(left, right PostCommitAction) bool {
	if left.Kind != right.Kind || left.ProductID != right.ProductID ||
		left.ProductKind != right.ProductKind || len(left.QueryReferences) != len(right.QueryReferences) {
		return false
	}
	for _, reference := range left.QueryReferences {
		if !hasMatchingReference([]core.StoreReference{reference}, right.QueryReferences) {
			return false
		}
	}
	return true
}

// validateReferencesWithRole checks validity, role consistency, and uniqueness.
func validateReferencesWithRole(references []core.StoreReference, role core.ReferenceRole) error {
	for i, reference := range references {
		if err := reference.Validate(); err != nil {
			return fmt.Errorf("%s reference %d: %w", role, i, err)
		}
		if reference.Role != role {
			return fmt.Errorf("reference %d must use role %q", i, role)
		}
		for j := 0; j < i; j++ {
			if reference.Equal(references[j]) {
				return fmt.Errorf("duplicate %s reference", role)
			}
		}
	}
	return nil
}

// hasMatchingReference reports whether two reference sets share an exact opaque reference.
func hasMatchingReference(actual, expected []core.StoreReference) bool {
	for _, actualReference := range actual {
		for _, expectedReference := range expected {
			if actualReference.Equal(expectedReference) {
				return true
			}
		}
	}
	return false
}

// validateProductSet checks provider product identifiers and rejects duplicates.
func validateProductSet(products []core.ProviderProductID) error {
	seen := make(map[core.ProviderProductID]struct{}, len(products))
	for _, product := range products {
		if err := product.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[product]; duplicate {
			return fmt.Errorf("duplicate provider product ID %q", product)
		}
		seen[product] = struct{}{}
	}
	return nil
}
