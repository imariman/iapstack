package verification

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// Projector selects provider-neutral current entitlement projections from verified candidates.
type Projector interface {
	// Project returns projections that should be applied to the current customer snapshot.
	Project([]persistence.CustomerEntitlement, []ProjectionCandidate) ([]persistence.EntitlementProjection, error)
}

// ProjectionCandidate associates one proposed projection with its observation time.
type ProjectionCandidate struct {
	Projection          persistence.EntitlementProjection
	SourceApplicationID core.ApplicationID
	ObservedAt          time.Time
}

// DefaultProjector implements conservative multi-product entitlement selection.
type DefaultProjector struct{}

// NewDefaultProjector returns the default replaceable entitlement selection policy.
func NewDefaultProjector() *DefaultProjector {
	return &DefaultProjector{}
}

// Project selects one candidate per entitlement without revoking an independent allowed source.
func (projector *DefaultProjector) Project(
	current []persistence.CustomerEntitlement,
	candidates []ProjectionCandidate,
) ([]persistence.EntitlementProjection, error) {
	if projector == nil {
		return nil, errors.New("entitlement projector is not initialized")
	}
	currentByEntitlement := make(map[core.EntitlementID]persistence.CustomerEntitlement, len(current))
	for index, entitlement := range current {
		if err := entitlement.Projection.Validate(); err != nil {
			return nil, fmt.Errorf("current entitlement %d: %w", index, err)
		}
		if err := entitlement.SourceApplicationID.Validate(); err != nil {
			return nil, fmt.Errorf("current entitlement %d source application: %w", index, err)
		}
		if _, duplicate := currentByEntitlement[entitlement.Projection.EntitlementID]; duplicate {
			return nil, fmt.Errorf("duplicate current entitlement %q", entitlement.Projection.EntitlementID)
		}
		currentByEntitlement[entitlement.Projection.EntitlementID] = entitlement
	}

	selected := make(map[core.EntitlementID]ProjectionCandidate, len(candidates))
	for index, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return nil, fmt.Errorf("projection candidate %d: %w", index, err)
		}
		entitlementID := candidate.Projection.EntitlementID
		if existing, exists := selected[entitlementID]; !exists || candidatePreferred(candidate, existing) {
			selected[entitlementID] = candidate
		}
	}

	projectionIDs := make([]core.EntitlementID, 0, len(selected))
	for entitlementID, candidate := range selected {
		currentEntitlement, exists := currentByEntitlement[entitlementID]
		if exists && preserveIndependentSource(currentEntitlement, candidate) {
			continue
		}
		projectionIDs = append(projectionIDs, entitlementID)
	}
	sort.Slice(projectionIDs, func(left, right int) bool {
		return projectionIDs[left] < projectionIDs[right]
	})

	projections := make([]persistence.EntitlementProjection, 0, len(projectionIDs))
	for _, entitlementID := range projectionIDs {
		projections = append(projections, selected[entitlementID].Projection)
	}
	return projections, nil
}

// Validate checks candidate projection invariants and its observation time.
func (candidate ProjectionCandidate) Validate() error {
	if candidate.ObservedAt.IsZero() {
		return errors.New("projection candidate observation time is required")
	}
	return errors.Join(candidate.Projection.Validate(), candidate.SourceApplicationID.Validate())
}

// candidatePreferred deterministically ranks competing new sources for one entitlement.
func candidatePreferred(candidate, existing ProjectionCandidate) bool {
	candidateStrength := accessStrength(candidate.Projection.Access)
	existingStrength := accessStrength(existing.Projection.Access)
	if candidateStrength != existingStrength {
		return candidateStrength > existingStrength
	}
	if !candidate.ObservedAt.Equal(existing.ObservedAt) {
		return candidate.ObservedAt.After(existing.ObservedAt)
	}
	return candidate.Projection.SourceObservationID < existing.Projection.SourceObservationID
}

// preserveIndependentSource keeps stronger or equal access from another internal product.
func preserveIndependentSource(
	current persistence.CustomerEntitlement,
	candidate ProjectionCandidate,
) bool {
	if current.SourceApplicationID == candidate.SourceApplicationID &&
		current.Projection.SourceProductID == candidate.Projection.SourceProductID {
		return false
	}
	return accessStrength(current.Projection.Access) >= accessStrength(candidate.Projection.Access)
}

// accessStrength ranks allowed, unresolved, and denied projections for source selection.
func accessStrength(access core.AccessStatus) int {
	switch access {
	case core.AccessAllowed:
		return 3
	case core.AccessUnresolved:
		return 2
	case core.AccessDenied:
		return 1
	default:
		return 0
	}
}
