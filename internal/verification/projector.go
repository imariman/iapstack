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

// ProjectionCandidate associates one proposed projection with its purchase lineage and observation time.
type ProjectionCandidate struct {
	Projection          persistence.EntitlementProjection
	SourceApplicationID core.ApplicationID
	// SourceLineage identifies the purchase lineage inside the application product.
	// Candidates with the same lineage, including an empty one, share one access source.
	SourceLineage string
	ObservedAt    time.Time
}

// projectionSource identifies one independent purchase lineage of an application product.
type projectionSource struct {
	ApplicationID core.ApplicationID
	ProductID     core.ProductID
	Lineage       string
}

// entitlementProduct identifies every lineage of one application product that grants one entitlement.
type entitlementProduct struct {
	EntitlementID core.EntitlementID
	ApplicationID core.ApplicationID
	ProductID     core.ProductID
}

// DefaultProjector implements conservative multi-product entitlement selection.
type DefaultProjector struct{}

// NewDefaultProjector returns the default replaceable entitlement selection policy.
func NewDefaultProjector() *DefaultProjector {
	return &DefaultProjector{}
}

// Project selects one candidate per entitlement without revoking an independent allowed source.
// Each purchase lineage is its own source, so an expired lineage cannot revoke a later purchase
// of the same product. A store observation still supersedes every import of its application product,
// whatever their ends. A manual grant is its own source too: store evidence never supersedes it, and
// it never supersedes store evidence. Among the remaining allowed sources the one whose access lasts
// longest is projected whatever its kind, so a lapsed store row cannot hide a current import of
// another product. Equal ends prefer store evidence, then a manual grant, then an import.
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

	storeProducts := make(map[entitlementProduct]struct{}, len(candidates))
	for index, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return nil, fmt.Errorf("projection candidate %d: %w", index, err)
		}
		if candidate.Projection.Normalized().SourceKind == persistence.SourceKindStoreObservation {
			storeProducts[productFromCandidate(candidate)] = struct{}{}
		}
	}
	byEntitlement := make(
		map[core.EntitlementID]map[projectionSource]ProjectionCandidate,
		len(candidates),
	)
	for _, candidate := range candidates {
		if _, superseded := storeProducts[productFromCandidate(candidate)]; superseded &&
			candidate.Projection.Normalized().SourceKind == persistence.SourceKindImport {
			continue
		}
		entitlementID := candidate.Projection.EntitlementID
		sources := byEntitlement[entitlementID]
		if sources == nil {
			sources = make(map[projectionSource]ProjectionCandidate)
			byEntitlement[entitlementID] = sources
		}
		source := sourceFromCandidate(candidate)
		if existing, exists := sources[source]; !exists || newerSourceCandidate(candidate, existing) {
			sources[source] = candidate
		}
	}
	selected := make(map[core.EntitlementID]ProjectionCandidate, len(byEntitlement))
	for entitlementID, sources := range byEntitlement {
		var chosen ProjectionCandidate
		chosenSet := false
		for _, candidate := range sources {
			if !chosenSet || candidatePreferred(candidate, chosen) {
				chosen = candidate
				chosenSet = true
			}
		}
		if current, exists := currentByEntitlement[entitlementID]; exists {
			if currentCandidate, updated := sources[sourceFromCurrent(current)]; updated &&
				accessStrength(currentCandidate.Projection.Access) == accessStrength(chosen.Projection.Access) &&
				compareAllowedEnd(currentCandidate.Projection, chosen.Projection) >= 0 &&
				!importLosesToStore(current.Projection, chosen.Projection) {
				chosen = currentCandidate
			}
		}
		selected[entitlementID] = chosen
	}

	projectionIDs := make([]core.EntitlementID, 0, len(selected))
	for entitlementID, candidate := range selected {
		currentEntitlement, exists := currentByEntitlement[entitlementID]
		if exists {
			currentSourceUpdated := currentSourceReported(
				currentEntitlement, byEntitlement[entitlementID], storeProducts,
			)
			if !currentSourceUpdated && preserveIndependentSource(currentEntitlement, candidate) {
				continue
			}
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

// sourceFromCandidate returns the independent access source of one proposed projection.
func sourceFromCandidate(candidate ProjectionCandidate) projectionSource {
	return projectionSource{
		ApplicationID: candidate.SourceApplicationID,
		ProductID:     candidate.Projection.SourceProductID,
		Lineage:       candidate.SourceLineage,
	}
}

// sourceFromCurrent returns the independent access source of one durable projection.
func sourceFromCurrent(current persistence.CustomerEntitlement) projectionSource {
	return projectionSource{
		ApplicationID: current.SourceApplicationID,
		ProductID:     current.Projection.SourceProductID,
		Lineage:       current.SourceLineage,
	}
}

// productFromCandidate returns the entitlement and application product of one proposed projection.
func productFromCandidate(candidate ProjectionCandidate) entitlementProduct {
	return entitlementProduct{
		EntitlementID: candidate.Projection.EntitlementID,
		ApplicationID: candidate.SourceApplicationID,
		ProductID:     candidate.Projection.SourceProductID,
	}
}

// productFromCurrent returns the entitlement and application product of one stored projection.
func productFromCurrent(current persistence.CustomerEntitlement) entitlementProduct {
	return entitlementProduct{
		EntitlementID: current.Projection.EntitlementID,
		ApplicationID: current.SourceApplicationID,
		ProductID:     current.Projection.SourceProductID,
	}
}

// currentSourceReported reports whether the candidates carry newer state for the current projection source.
// A current import is reported once a store observation exists for its application product.
func currentSourceReported(
	current persistence.CustomerEntitlement,
	sources map[projectionSource]ProjectionCandidate,
	storeProducts map[entitlementProduct]struct{},
) bool {
	if _, updated := sources[sourceFromCurrent(current)]; updated {
		return true
	}
	if current.Projection.Normalized().SourceKind != persistence.SourceKindImport {
		return false
	}
	_, superseded := storeProducts[productFromCurrent(current)]
	return superseded
}

// newerSourceCandidate keeps the latest lifecycle state for one purchase lineage source.
// A store observation outranks an import grant that shares its lineage key.
func newerSourceCandidate(candidate, existing ProjectionCandidate) bool {
	if rank := compareSourceKind(candidate.Projection, existing.Projection); rank != 0 {
		return rank > 0
	}
	if !candidate.ObservedAt.Equal(existing.ObservedAt) {
		return candidate.ObservedAt.After(existing.ObservedAt)
	}
	return projectionSourceIdentity(candidate.Projection) < projectionSourceIdentity(existing.Projection)
}

// candidatePreferred deterministically ranks competing new sources for one entitlement.
// Stronger access wins first. Among allowed sources the one whose access lasts longest wins
// before source kinds are compared, so a lapsed store row that has not been refreshed yet cannot
// hide a current import, a lapsed lineage cannot hide a newer purchase, and a manual grant that
// outlasts store access stays projected until its own end. Equal ends prefer store evidence, then
// a manual grant, then an import; a later observation and then the smaller source id break ties.
// The keys are compared in that fixed order, so the winner does not depend on candidate order.
func candidatePreferred(candidate, existing ProjectionCandidate) bool {
	candidateStrength := accessStrength(candidate.Projection.Access)
	existingStrength := accessStrength(existing.Projection.Access)
	if candidateStrength != existingStrength {
		return candidateStrength > existingStrength
	}
	if rank := compareAllowedEnd(candidate.Projection, existing.Projection); rank != 0 {
		return rank > 0
	}
	if rank := compareSourceKind(candidate.Projection, existing.Projection); rank != 0 {
		return rank > 0
	}
	if !candidate.ObservedAt.Equal(existing.ObservedAt) {
		return candidate.ObservedAt.After(existing.ObservedAt)
	}
	return projectionSourceIdentity(candidate.Projection) < projectionSourceIdentity(existing.Projection)
}

// compareAllowedEnd ranks two allowed projections by when their access ends. No end ranks highest.
// It returns zero when either projection is not allowed.
func compareAllowedEnd(candidate, existing persistence.EntitlementProjection) int {
	if candidate.Access != core.AccessAllowed || existing.Access != core.AccessAllowed {
		return 0
	}
	candidateEnd := candidate.EffectivePeriod.EndsAt
	existingEnd := existing.EffectivePeriod.EndsAt
	switch {
	case candidateEnd == nil && existingEnd == nil:
		return 0
	case candidateEnd == nil:
		return 1
	case existingEnd == nil:
		return -1
	case candidateEnd.After(*existingEnd):
		return 1
	case candidateEnd.Before(*existingEnd):
		return -1
	default:
		return 0
	}
}

// compareSourceKind ranks a store observation above a manual grant, and a manual grant above an import.
func compareSourceKind(candidate, existing persistence.EntitlementProjection) int {
	return sourceKindRank(candidate) - sourceKindRank(existing)
}

// sourceKindRank scores store observations above manual grants and manual grants above import grants.
func sourceKindRank(projection persistence.EntitlementProjection) int {
	switch projection.Normalized().SourceKind {
	case persistence.SourceKindImport:
		return 1
	case persistence.SourceKindManual:
		return 2
	default:
		return 3
	}
}

// projectionSourceIdentity returns the stable id used to break equal observation times.
func projectionSourceIdentity(projection persistence.EntitlementProjection) string {
	switch projection.Normalized().SourceKind {
	case persistence.SourceKindImport:
		return projection.SourceImportID
	case persistence.SourceKindManual:
		return projection.SourceManualGrantID
	default:
		return string(projection.SourceObservationID)
	}
}

// importLosesToStore reports that an equal-strength shortcut must not keep an import over a store observation.
func importLosesToStore(current, chosen persistence.EntitlementProjection) bool {
	return current.Normalized().SourceKind == persistence.SourceKindImport &&
		chosen.Normalized().SourceKind == persistence.SourceKindStoreObservation
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
