package verification_test

import (
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/verification"
)

// TestDefaultProjectorDoesNotLoseCurrentSourceDenial verifies another denied source cannot preserve stale access.
func TestDefaultProjectorDoesNotLoseCurrentSourceDenial(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	current := persistence.CustomerEntitlement{
		Projection:          projectionFor("product-current", "observation-current", core.AccessAllowed),
		SourceApplicationID: verificationApplicationID,
		Key:                 verificationEntitlementKey,
		Version:             1,
	}
	candidates := []verification.ProjectionCandidate{
		{
			Projection:          projectionFor("product-current", "observation-current-denied", core.AccessDenied),
			SourceApplicationID: verificationApplicationID,
			ObservedAt:          now,
		},
		{
			Projection:          projectionFor("product-other", "observation-other-denied", core.AccessDenied),
			SourceApplicationID: "application-other",
			ObservedAt:          now.Add(time.Minute),
		},
	}

	projections, err := verification.NewDefaultProjector().Project(
		[]persistence.CustomerEntitlement{current}, candidates,
	)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].Access != core.AccessDenied ||
		projections[0].SourceProductID != "product-current" {
		t.Fatalf("Project() = %#v, want current-source denial", projections)
	}
}

// TestDefaultProjectorUsesNewestLifecyclePerSource verifies stale access cannot outrank a newer denial.
func TestDefaultProjectorUsesNewestLifecyclePerSource(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	candidates := []verification.ProjectionCandidate{
		{
			Projection:          projectionFor("product-1", "observation-allowed", core.AccessAllowed),
			SourceApplicationID: verificationApplicationID,
			ObservedAt:          now,
		},
		{
			Projection:          projectionFor("product-1", "observation-denied", core.AccessDenied),
			SourceApplicationID: verificationApplicationID,
			ObservedAt:          now.Add(time.Minute),
		},
	}

	projections, err := verification.NewDefaultProjector().Project(nil, candidates)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].Access != core.AccessDenied ||
		projections[0].SourceObservationID != "observation-denied" {
		t.Fatalf("Project() = %#v, want newest same-source denial", projections)
	}
}
