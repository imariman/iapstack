package verification_test

import (
	"encoding/json"
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

// TestDefaultProjectorPrefersOlderStoreObservationOverImport verifies a store observation beats a newer import for the same product.
func TestDefaultProjectorPrefersOlderStoreObservationOverImport(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	candidates := []verification.ProjectionCandidate{
		importCandidate("product-1", "imp-11111111111111111111111111111111", core.AccessAllowed, now.Add(time.Hour)),
		{
			Projection:          projectionFor("product-1", "observation-older", core.AccessAllowed),
			SourceApplicationID: verificationApplicationID,
			ObservedAt:          now,
		},
	}
	projections, err := verification.NewDefaultProjector().Project(nil, candidates)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].SourceObservationID != "observation-older" ||
		projections[0].Normalized().SourceKind != persistence.SourceKindStoreObservation {
		t.Fatalf("Project() = %#v, want older store observation", projections)
	}
}

// TestDefaultProjectorDeniedStoreReplacesAllowedImport verifies a denial for the same product supersedes an import.
func TestDefaultProjectorDeniedStoreReplacesAllowedImport(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	current := persistence.CustomerEntitlement{
		Projection:          importProjection("product-1", "imp-22222222222222222222222222222222", core.AccessAllowed),
		SourceApplicationID: verificationApplicationID,
		Key:                 verificationEntitlementKey,
		Version:             3,
	}
	candidates := []verification.ProjectionCandidate{
		importCandidate("product-1", "imp-22222222222222222222222222222222", core.AccessAllowed, now.Add(time.Hour)),
		{
			Projection:          projectionFor("product-1", "observation-denied", core.AccessDenied),
			SourceApplicationID: verificationApplicationID,
			ObservedAt:          now,
		},
	}
	projections, err := verification.NewDefaultProjector().Project([]persistence.CustomerEntitlement{current}, candidates)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].Access != core.AccessDenied ||
		projections[0].SourceObservationID != "observation-denied" {
		t.Fatalf("Project() = %#v, want denied store replacement", projections)
	}
}

// TestDefaultProjectorImportDoesNotReplaceIndependentStore verifies an allowed import keeps an independent store source.
func TestDefaultProjectorImportDoesNotReplaceIndependentStore(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	current := persistence.CustomerEntitlement{
		Projection:          projectionFor("product-store", "observation-store", core.AccessAllowed),
		SourceApplicationID: verificationApplicationID,
		Key:                 verificationEntitlementKey,
		Version:             2,
	}
	candidates := []verification.ProjectionCandidate{
		{
			Projection:          projectionFor("product-store", "observation-store", core.AccessAllowed),
			SourceApplicationID: verificationApplicationID,
			ObservedAt:          now,
		},
		importCandidate("product-import", "imp-33333333333333333333333333333333", core.AccessAllowed, now.Add(time.Hour)),
	}
	projections, err := verification.NewDefaultProjector().Project([]persistence.CustomerEntitlement{current}, candidates)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].SourceProductID != "product-store" ||
		projections[0].SourceObservationID != "observation-store" {
		t.Fatalf("Project() = %#v, want independent store source", projections)
	}
}

// TestDefaultProjectorDeniedStoreDoesNotClearIndependentImport verifies another product's denial cannot revoke an import.
func TestDefaultProjectorDeniedStoreDoesNotClearIndependentImport(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	current := persistence.CustomerEntitlement{
		Projection:          importProjection("product-import", "imp-44444444444444444444444444444444", core.AccessAllowed),
		SourceApplicationID: verificationApplicationID,
		Key:                 verificationEntitlementKey,
		Version:             4,
	}
	candidates := []verification.ProjectionCandidate{
		importCandidate("product-import", "imp-44444444444444444444444444444444", core.AccessAllowed, now),
		{
			Projection:          projectionFor("product-other", "observation-other-denied", core.AccessDenied),
			SourceApplicationID: "application-other",
			ObservedAt:          now.Add(time.Hour),
		},
	}
	projections, err := verification.NewDefaultProjector().Project([]persistence.CustomerEntitlement{current}, candidates)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].Access != core.AccessAllowed ||
		projections[0].SourceKind != persistence.SourceKindImport {
		t.Fatalf("Project() = %#v, want allowed import preserved", projections)
	}
}

// TestEntitlementChangedEventUsesImportSchema verifies version 2 omits the observation id.
func TestEntitlementChangedEventUsesImportSchema(t *testing.T) {
	t.Parallel()

	endsAt := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	entitlement := persistence.CustomerEntitlement{
		Projection:          importProjection("product-1", "imp-55555555555555555555555555555555", core.AccessAllowed),
		SourceApplicationID: verificationApplicationID,
		Key:                 verificationEntitlementKey,
		Version:             2,
	}
	entitlement.Projection.EffectivePeriod.EndsAt = &endsAt
	event, err := verification.EntitlementChangedEvent(verificationApplicationID, entitlement, endsAt)
	if err != nil {
		t.Fatalf("EntitlementChangedEvent() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if payload["schema_version"] != float64(2) || payload["source"] != "import" ||
		payload["source_import_id"] != "imp-55555555555555555555555555555555" {
		t.Fatalf("payload = %#v, want import schema 2", payload)
	}
	if _, exists := payload["source_observation_id"]; exists {
		t.Fatalf("import payload included source_observation_id: %#v", payload)
	}
	if _, exists := payload["verified_at"]; exists {
		t.Fatalf("import payload included verified_at: %#v", payload)
	}
}

// TestDefaultProjectorKeepsAllowedLineageOverNewerExpiredLineage verifies a refreshed expired lineage cannot revoke a repurchase.
func TestDefaultProjectorKeepsAllowedLineageOverNewerExpiredLineage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	repurchase := lineageCandidate("observation-b-active", "lineage-b", core.AccessAllowed, now, now.AddDate(0, 1, 0))
	current := persistence.CustomerEntitlement{
		Projection:          repurchase.Projection,
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       "lineage-b",
		Key:                 verificationEntitlementKey,
		Version:             2,
	}
	candidates := []verification.ProjectionCandidate{
		lineageCandidate("observation-a-expired", "lineage-a", core.AccessDenied, now.Add(24*time.Hour), time.Time{}),
		repurchase,
	}

	projections, err := verification.NewDefaultProjector().Project([]persistence.CustomerEntitlement{current}, candidates)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].Access != core.AccessAllowed ||
		projections[0].SourceObservationID != "observation-b-active" {
		t.Fatalf("Project() = %#v, want the active lineage", projections)
	}
}

// TestDefaultProjectorPrefersLongerAllowedLineage verifies a lapsed lineage that was not refreshed yet cannot hide a repurchase.
func TestDefaultProjectorPrefersLongerAllowedLineage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	lapsed := lineageCandidate("observation-a-active", "lineage-a", core.AccessAllowed, now.AddDate(0, -1, 0), now.Add(-time.Hour))
	current := persistence.CustomerEntitlement{
		Projection:          lapsed.Projection,
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       "lineage-a",
		Key:                 verificationEntitlementKey,
		Version:             1,
	}
	candidates := []verification.ProjectionCandidate{
		lapsed,
		lineageCandidate("observation-b-active", "lineage-b", core.AccessAllowed, now, now.AddDate(0, 1, 0)),
	}

	projections, err := verification.NewDefaultProjector().Project([]persistence.CustomerEntitlement{current}, candidates)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].SourceObservationID != "observation-b-active" {
		t.Fatalf("Project() = %#v, want the lineage with the later access end", projections)
	}
}

// TestDefaultProjectorStoreSupersedesEveryImportLineage verifies a store observation replaces imports from every lineage of its product.
func TestDefaultProjectorStoreSupersedesEveryImportLineage(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	current := persistence.CustomerEntitlement{
		Projection:          importProjection("product-1", "imp-88888888888888888888888888888888", core.AccessAllowed),
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       "import:lineage-1",
		Key:                 verificationEntitlementKey,
		Version:             1,
	}
	firstImport := importCandidate("product-1", "imp-88888888888888888888888888888888", core.AccessAllowed, now)
	firstImport.SourceLineage = "import:lineage-1"
	secondImport := importCandidate("product-1", "imp-99999999999999999999999999999999", core.AccessAllowed, now)
	secondImport.SourceLineage = "import:lineage-2"
	candidates := []verification.ProjectionCandidate{
		firstImport,
		secondImport,
		{
			Projection:          projectionFor("product-1", "observation-store-denied", core.AccessDenied),
			SourceApplicationID: verificationApplicationID,
			SourceLineage:       "reference:1",
			ObservedAt:          now.Add(-time.Hour),
		},
		{
			Projection:          projectionFor("product-other", "observation-other-denied", core.AccessDenied),
			SourceApplicationID: "application-other",
			SourceLineage:       "reference:2",
			ObservedAt:          now.Add(time.Hour),
		},
	}

	projections, err := verification.NewDefaultProjector().Project([]persistence.CustomerEntitlement{current}, candidates)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].Access != core.AccessDenied ||
		projections[0].Normalized().SourceKind != persistence.SourceKindStoreObservation {
		t.Fatalf("Project() = %#v, want a store denial instead of any import lineage", projections)
	}
}

// TestDefaultProjectorRanksStoreAndImportByAllowedEnd verifies store evidence does not
// outrank an import of another product before their access ends are compared.
func TestDefaultProjectorRanksStoreAndImportByAllowedEnd(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	lapsed := now.Add(-24 * time.Hour)
	current := now.AddDate(0, 0, 13)
	tests := map[string]struct {
		storeEnd   *time.Time
		importEnd  *time.Time
		wantImport bool
	}{
		"lapsed store and current import":     {storeEnd: &lapsed, importEnd: &current, wantImport: true},
		"current store and lapsed import":     {storeEnd: &current, importEnd: &lapsed},
		"equal ends":                          {storeEnd: &current, importEnd: &current},
		"store without end and ending import": {storeEnd: nil, importEnd: &current},
		"ending store and import without end": {storeEnd: &lapsed, importEnd: nil, wantImport: true},
		"store and import without end":        {},
	}
	for name, test := range tests {
		store := endedCandidate(verification.ProjectionCandidate{
			Projection:          projectionFor("product-store", "observation-store", core.AccessAllowed),
			SourceApplicationID: verificationApplicationID,
			SourceLineage:       "reference:1",
			ObservedAt:          now.Add(-time.Hour),
		}, now.AddDate(0, -1, 0), test.storeEnd)
		imported := importCandidate("product-import", "imp-66666666666666666666666666666666", core.AccessAllowed, now)
		imported.SourceLineage = "import:lineage-1"
		imported = endedCandidate(imported, now.AddDate(0, -1, 0), test.importEnd)
		want := store.Projection
		if test.wantImport {
			want = imported.Projection
		}

		for order, candidates := range map[string][]verification.ProjectionCandidate{
			"store first":  {store, imported},
			"import first": {imported, store},
		} {
			projections, err := verification.NewDefaultProjector().Project(nil, candidates)
			if err != nil {
				t.Fatalf("%s, %s: Project() error = %v", name, order, err)
			}
			if len(projections) != 1 || projections[0] != want {
				t.Fatalf("%s, %s: Project() = %#v, want %#v", name, order, projections, want)
			}
		}

		for currentName, currentCandidate := range map[string]verification.ProjectionCandidate{
			"current store":  store,
			"current import": imported,
		} {
			current := persistence.CustomerEntitlement{
				Projection:          currentCandidate.Projection,
				SourceApplicationID: currentCandidate.SourceApplicationID,
				SourceLineage:       currentCandidate.SourceLineage,
				Key:                 verificationEntitlementKey,
				Version:             1,
			}
			projections, err := verification.NewDefaultProjector().Project(
				[]persistence.CustomerEntitlement{current}, []verification.ProjectionCandidate{store, imported},
			)
			if err != nil {
				t.Fatalf("%s, %s: Project() error = %v", name, currentName, err)
			}
			if len(projections) != 1 || projections[0] != want {
				t.Fatalf("%s, %s: Project() = %#v, want %#v", name, currentName, projections, want)
			}
		}
	}
}

// TestDefaultProjectorRanksEveryAllowedSourceByEndFirst verifies one deterministic winner when
// store, manual, and import sources of different products end at different times.
func TestDefaultProjectorRanksEveryAllowedSourceByEndFirst(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	storeEnd := now.AddDate(0, 0, 1)
	importEnd := now.AddDate(0, 0, 30)
	store := endedCandidate(verification.ProjectionCandidate{
		Projection:          projectionFor("product-store", "observation-store", core.AccessAllowed),
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       "reference:1",
		ObservedAt:          now,
	}, now.AddDate(0, -1, 0), &storeEnd)
	manual := manualCandidate("product-manual", manualGrantIDOne, core.AccessAllowed, now, now.AddDate(0, 0, 10))
	imported := importCandidate("product-import", "imp-77777777777777777777777777777777", core.AccessAllowed, now)
	imported.SourceLineage = "import:lineage-1"
	imported = endedCandidate(imported, now.AddDate(0, -1, 0), &importEnd)

	// The selection loop walks a map, so repeat it to cover several iteration orders.
	for attempt := range 64 {
		projections, err := verification.NewDefaultProjector().Project(
			nil, []verification.ProjectionCandidate{store, manual, imported},
		)
		if err != nil {
			t.Fatalf("attempt %d: Project() error = %v", attempt, err)
		}
		if len(projections) != 1 || projections[0] != imported.Projection {
			t.Fatalf("attempt %d: Project() = %#v, want the import that lasts longest", attempt, projections)
		}
	}
}

// TestDefaultProjectorStoreStillSupersedesImportOfItsProduct verifies a lapsed store row
// still replaces a current import of the same application product.
func TestDefaultProjectorStoreStillSupersedesImportOfItsProduct(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	lapsed := now.Add(-24 * time.Hour)
	current := now.AddDate(0, 0, 13)
	store := endedCandidate(verification.ProjectionCandidate{
		Projection:          projectionFor(verificationProductID, "observation-store", core.AccessAllowed),
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       "reference:1",
		ObservedAt:          now.Add(-time.Hour),
	}, now.AddDate(0, -1, 0), &lapsed)
	imported := importCandidate(verificationProductID, "imp-66666666666666666666666666666666", core.AccessAllowed, now)
	imported.SourceLineage = "import:lineage-1"
	imported = endedCandidate(imported, now.AddDate(0, -1, 0), &current)

	projections, err := verification.NewDefaultProjector().Project(nil, []verification.ProjectionCandidate{imported, store})
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0] != store.Projection {
		t.Fatalf("Project() = %#v, want the store observation of the same product", projections)
	}
}

// endedCandidate returns candidate with an allowed period from startsAt to endsAt; a nil end has no expiry.
func endedCandidate(
	candidate verification.ProjectionCandidate,
	startsAt time.Time,
	endsAt *time.Time,
) verification.ProjectionCandidate {
	var end *time.Time
	if endsAt != nil {
		value := *endsAt
		end = &value
	}
	candidate.Projection.EffectivePeriod = core.EffectivePeriod{StartsAt: startsAt, EndsAt: end}
	return candidate
}

// lineageCandidate builds one store candidate for a purchase lineage of the shared product.
// An allowed candidate starts a month before endsAt.
func lineageCandidate(
	observationID core.ObservationID,
	lineage string,
	access core.AccessStatus,
	observedAt time.Time,
	endsAt time.Time,
) verification.ProjectionCandidate {
	projection := projectionFor(verificationProductID, observationID, access)
	if access == core.AccessAllowed {
		end := endsAt
		projection.EffectivePeriod = core.EffectivePeriod{StartsAt: endsAt.AddDate(0, -1, 0), EndsAt: &end}
	}
	return verification.ProjectionCandidate{
		Projection:          projection,
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       lineage,
		ObservedAt:          observedAt,
	}
}

// importProjection builds one allowed or denied import projection.
func importProjection(productID core.ProductID, grantID string, access core.AccessStatus) persistence.EntitlementProjection {
	projection := projectionFor(productID, "", access)
	projection.SourceObservationID = ""
	projection.SourceKind = persistence.SourceKindImport
	projection.SourceImportID = grantID
	return projection
}

// importCandidate builds one import projection candidate at a fixed observation time.
func importCandidate(productID core.ProductID, grantID string, access core.AccessStatus, observedAt time.Time) verification.ProjectionCandidate {
	return verification.ProjectionCandidate{
		Projection:          importProjection(productID, grantID, access),
		SourceApplicationID: verificationApplicationID,
		ObservedAt:          observedAt,
	}
}
