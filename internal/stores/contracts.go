package stores

import (
	"context"
	"errors"
	"fmt"
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
	ExpectedCustomerBindings []core.StoreReference
	QueryReferences          []core.StoreReference
}

type VerificationResult struct {
	VerifiedAt   time.Time
	Artifacts    []VerifiedArtifact
	Observations []core.PurchaseObservation
}

type Verifier interface {
	// Verify authenticates purchase evidence and returns normalized observations.
	Verify(context.Context, VerificationRequest) (VerificationResult, error)
}

type Reconciler interface {
	// Reconcile queries authoritative provider state from stored purchase references.
	Reconcile(context.Context, ReconciliationRequest) (VerificationResult, error)
}

type Adapter interface {
	// Provider identifies the purchase provider implemented by the adapter.
	Provider() core.Provider
	Verifier
	Reconciler
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
	if err := errors.Join(request.Application.Validate(), request.CustomerID.Validate()); err != nil {
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
	return result.validate(
		request.Application,
		request.ExpectedProducts,
		request.ExpectedCustomerBindings,
		request.QueryReferences,
	)
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
