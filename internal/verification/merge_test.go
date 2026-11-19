package verification_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/verification"
)

// mergeTestSourceCustomerID identifies the customer merged into verificationCustomerID.
const mergeTestSourceCustomerID core.CustomerID = "customer-merge-source"

// mergeCustomer is one customer's stored snapshot and access sources in a merge test.
type mergeCustomer struct {
	current []verification.ProjectionCandidate
	sources []verification.ProjectionCandidate
}

// mergeTransaction serves the reads and writes of verification.MergeCustomers in memory.
type mergeTransaction struct {
	persistence.Transaction
	customers map[core.CustomerID]mergeCustomer
	replay    bool
	merged    bool
	written   []persistence.EntitlementProjection
}

// TestMergeCustomersChecksAccessFromSnapshotsAndSources verifies the merge decision: dual
// access hidden behind a stale snapshot and access a superseding store observation would
// end are refused before anything is projected, and kept access is written as checked.
func TestMergeCustomersChecksAccessFromSnapshotsAndSources(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	lapsed, current := now.Add(-24*time.Hour), now.AddDate(0, 0, 10)
	currentImport := mergeImportCandidate("product-a", "imp-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", now, &current)
	lapsedStore := endedCandidate(verification.ProjectionCandidate{
		Projection:          projectionFor("product-store", "observation-lapsed", core.AccessAllowed),
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       "reference:1",
		ObservedAt:          now.Add(-48 * time.Hour),
	}, now.AddDate(0, -1, 0), &lapsed)
	otherCurrentImport := mergeImportCandidate("product-b", "imp-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", now, &current)
	productImport := mergeImportCandidate(verificationProductID, "imp-cccccccccccccccccccccccccccccccc", now, &current)
	lapsedOtherImport := mergeImportCandidate("product-q", "imp-dddddddddddddddddddddddddddddddd", now, &lapsed)
	deniedProductStore := verification.ProjectionCandidate{
		Projection:          projectionFor(verificationProductID, "observation-denied", core.AccessDenied),
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       "reference:2",
		ObservedAt:          now.Add(-time.Hour),
	}

	tests := map[string]struct {
		canonical    mergeCustomer
		source       mergeCustomer
		wantConflict bool
		wantWritten  string
	}{
		"dual access hidden behind a stale snapshot": {
			canonical: mergeCustomer{
				current: []verification.ProjectionCandidate{currentImport},
				sources: []verification.ProjectionCandidate{currentImport},
			},
			source: mergeCustomer{
				current: []verification.ProjectionCandidate{lapsedStore},
				sources: []verification.ProjectionCandidate{lapsedStore, otherCurrentImport},
			},
			wantConflict: true,
		},
		"access an unprojected store observation would supersede": {
			canonical: mergeCustomer{
				current: []verification.ProjectionCandidate{productImport},
				sources: []verification.ProjectionCandidate{productImport},
			},
			source: mergeCustomer{
				current: []verification.ProjectionCandidate{lapsedOtherImport},
				sources: []verification.ProjectionCandidate{deniedProductStore, lapsedOtherImport},
			},
			wantConflict: true,
		},
		"current access kept over a lapsed store row": {
			canonical: mergeCustomer{
				current: []verification.ProjectionCandidate{currentImport},
				sources: []verification.ProjectionCandidate{currentImport},
			},
			source: mergeCustomer{
				current: []verification.ProjectionCandidate{lapsedStore},
				sources: []verification.ProjectionCandidate{lapsedStore},
			},
			wantWritten: currentImport.Projection.SourceImportID,
		},
	}
	for name, test := range tests {
		repository := &mergeTransaction{customers: map[core.CustomerID]mergeCustomer{
			verificationCustomerID: test.canonical, mergeTestSourceCustomerID: test.source,
		}}
		_, err := verification.MergeCustomers(
			context.Background(), repository, verification.NewDefaultProjector(), mergeTestWrite(), now,
		)
		if test.wantConflict {
			if !errors.Is(err, persistence.ErrEntitlementConflict) || len(repository.written) != 0 {
				t.Fatalf("%s: MergeCustomers() error = %v, written %#v; want a conflict and no writes",
					name, err, repository.written)
			}
			continue
		}
		if err != nil || len(repository.written) != 1 || repository.written[0].SourceImportID != test.wantWritten {
			t.Fatalf("%s: MergeCustomers() error = %v, written %#v; want %s", name, err, repository.written, test.wantWritten)
		}
	}
}

// TestMergeCustomersReplayRefreshesNothing verifies a replayed merge skips the access check and the refresh.
func TestMergeCustomersReplayRefreshesNothing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	current := now.AddDate(0, 0, 10)
	both := mergeImportCandidate("product-a", "imp-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", now, &current)
	repository := &mergeTransaction{replay: true, customers: map[core.CustomerID]mergeCustomer{
		verificationCustomerID:    {current: []verification.ProjectionCandidate{both}, sources: []verification.ProjectionCandidate{both}},
		mergeTestSourceCustomerID: {current: []verification.ProjectionCandidate{both}, sources: []verification.ProjectionCandidate{both}},
	}}
	result, err := verification.MergeCustomers(
		context.Background(), repository, verification.NewDefaultProjector(), mergeTestWrite(), now,
	)
	if err != nil || !result.Replay || len(repository.written) != 0 {
		t.Fatalf("MergeCustomers() = %#v, %v, written %#v; want a replay without writes", result, err, repository.written)
	}
	if _, err := verification.MergeCustomers(context.Background(), repository, nil, mergeTestWrite(), now); err == nil {
		t.Fatal("MergeCustomers() without a projector succeeded")
	}
}

// LockCustomer accepts every customer lock.
func (repository *mergeTransaction) LockCustomer(context.Context, core.ProjectID, core.CustomerID) error {
	return nil
}

// LockCustomerClosure accepts every closure lock.
func (repository *mergeTransaction) LockCustomerClosure(context.Context, core.ProjectID, core.CustomerID) error {
	return nil
}

// MergeCustomers records the merge so the canonical closure then exposes both customers' sources.
func (repository *mergeTransaction) MergeCustomers(
	_ context.Context,
	write persistence.CustomerMergeWrite,
) (persistence.CustomerMergeResult, error) {
	repository.merged = !repository.replay
	return persistence.CustomerMergeResult{
		Merge: persistence.CustomerMerge{
			ID: "merge-1", ProjectID: write.ProjectID, CanonicalCustomerID: write.CanonicalCustomerID,
			SourceCustomerID: write.SourceCustomerID, IdempotencyKey: write.IdempotencyKey,
		},
		Replay: repository.replay,
	}, nil
}

// CustomerEntitlementSources returns one customer's sources, and both customers' after a merge.
func (repository *mergeTransaction) CustomerEntitlementSources(
	_ context.Context,
	_ core.ProjectID,
	customerID core.CustomerID,
) ([]persistence.EntitlementSource, error) {
	candidates := repository.customers[customerID].sources
	if repository.merged && customerID == verificationCustomerID {
		candidates = append(append([]verification.ProjectionCandidate{}, candidates...),
			repository.customers[mergeTestSourceCustomerID].sources...)
	}
	sources := make([]persistence.EntitlementSource, 0, len(candidates))
	for _, candidate := range candidates {
		sources = append(sources, persistence.EntitlementSource{
			Projection: candidate.Projection, SourceApplicationID: candidate.SourceApplicationID,
			SourceLineage: candidate.SourceLineage, ObservedAt: candidate.ObservedAt,
		})
	}
	return sources, nil
}

// CustomerEntitlements returns one customer's stored snapshot.
func (repository *mergeTransaction) CustomerEntitlements(
	_ context.Context,
	_ core.ProjectID,
	customerID core.CustomerID,
) ([]persistence.CustomerEntitlement, error) {
	current := make([]persistence.CustomerEntitlement, 0)
	for _, candidate := range repository.customers[customerID].current {
		current = append(current, persistence.CustomerEntitlement{
			Projection: candidate.Projection, SourceApplicationID: candidate.SourceApplicationID,
			SourceLineage: candidate.SourceLineage, Key: verificationEntitlementKey, Version: 1,
		})
	}
	return current, nil
}

// PutEntitlement records one written projection without reporting a change.
func (repository *mergeTransaction) PutEntitlement(
	_ context.Context,
	projection persistence.EntitlementProjection,
) (persistence.EntitlementWriteResult, error) {
	repository.written = append(repository.written, projection)
	return persistence.EntitlementWriteResult{}, nil
}

// mergeImportCandidate builds one allowed import of productID that ends at endsAt.
func mergeImportCandidate(
	productID core.ProductID,
	grantID string,
	observedAt time.Time,
	endsAt *time.Time,
) verification.ProjectionCandidate {
	candidate := importCandidate(productID, grantID, core.AccessAllowed, observedAt)
	candidate.SourceLineage = "import:" + grantID
	return endedCandidate(candidate, observedAt.AddDate(0, -1, 0), endsAt)
}

// mergeTestWrite merges mergeTestSourceCustomerID into verificationCustomerID.
func mergeTestWrite() persistence.CustomerMergeWrite {
	return persistence.CustomerMergeWrite{
		ProjectID: verificationProjectID, CanonicalCustomerID: verificationCustomerID,
		SourceCustomerID: mergeTestSourceCustomerID, IdempotencyKey: "merge-test", ActorKeyID: "admin-key",
	}
}
