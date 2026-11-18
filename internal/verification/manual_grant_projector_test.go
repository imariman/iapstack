package verification_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// manualGrantIDOne identifies the operator grant used by projector tests.
	manualGrantIDOne = "mgr-0123456789abcdef0123456789abcdef"
)

// TestDefaultProjectorManualGrantOutlastsShorterStoreAccess verifies a longer manual grant is projected over shorter store access.
func TestDefaultProjectorManualGrantOutlastsShorterStoreAccess(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	store := lineageCandidate("observation-active", "reference:1", core.AccessAllowed, now, now.AddDate(0, 0, 5))
	current := persistence.CustomerEntitlement{
		Projection: store.Projection, SourceApplicationID: verificationApplicationID,
		SourceLineage: "reference:1", Key: verificationEntitlementKey, Version: 1,
	}
	manual := manualCandidate(verificationProductID, manualGrantIDOne, core.AccessAllowed, now, now.AddDate(0, 0, 30))

	projections, err := verification.NewDefaultProjector().Project(
		[]persistence.CustomerEntitlement{current}, []verification.ProjectionCandidate{store, manual},
	)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].SourceKind != persistence.SourceKindManual ||
		projections[0].SourceManualGrantID != manualGrantIDOne || projections[0].SourceObservationID != "" {
		t.Fatalf("Project() = %#v, want the longer manual grant", projections)
	}
}

// TestDefaultProjectorStoreBeatsManualGrantWithEqualOrLongerAccess verifies store evidence stays projected when it lasts as long.
func TestDefaultProjectorStoreBeatsManualGrantWithEqualOrLongerAccess(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	ends := now.AddDate(0, 0, 30)
	for name, store := range map[string]verification.ProjectionCandidate{
		"lifetime": {
			Projection:          projectionFor("product-lifetime", "observation-lifetime", core.AccessAllowed),
			SourceApplicationID: verificationApplicationID,
			SourceLineage:       "reference:9",
			ObservedAt:          now.Add(-time.Hour),
		},
		"same end": lineageCandidate("observation-same-end", "reference:1", core.AccessAllowed, now.Add(-time.Hour), ends),
	} {
		manual := manualCandidate(verificationProductID, manualGrantIDOne, core.AccessAllowed, now, ends)
		projections, err := verification.NewDefaultProjector().Project(nil, []verification.ProjectionCandidate{manual, store})
		if err != nil {
			t.Fatalf("%s Project() error = %v", name, err)
		}
		if len(projections) != 1 || projections[0].Normalized().SourceKind != persistence.SourceKindStoreObservation {
			t.Fatalf("%s Project() = %#v, want store evidence", name, projections)
		}
	}
}

// TestDefaultProjectorStoreDenialDoesNotSupersedeManualGrant verifies store evidence for the same product never clears a manual grant.
func TestDefaultProjectorStoreDenialDoesNotSupersedeManualGrant(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	manual := manualCandidate(verificationProductID, manualGrantIDOne, core.AccessAllowed, now, now.AddDate(0, 0, 30))
	current := persistence.CustomerEntitlement{
		Projection: manual.Projection, SourceApplicationID: verificationApplicationID,
		SourceLineage: manual.SourceLineage, Key: verificationEntitlementKey, Version: 1,
	}
	denied := verification.ProjectionCandidate{
		Projection:          projectionFor(verificationProductID, "observation-expired", core.AccessDenied),
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       "reference:1",
		ObservedAt:          now.Add(time.Hour),
	}

	projections, err := verification.NewDefaultProjector().Project(
		[]persistence.CustomerEntitlement{current}, []verification.ProjectionCandidate{manual, denied},
	)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].Access != core.AccessAllowed ||
		projections[0].SourceKind != persistence.SourceKindManual {
		t.Fatalf("Project() = %#v, want the manual grant to keep access", projections)
	}
}

// TestDefaultProjectorRevokedManualGrantFallsBackToStoreAccess verifies revocation hands access back to another source.
func TestDefaultProjectorRevokedManualGrantFallsBackToStoreAccess(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	allowed := manualCandidate(verificationProductID, manualGrantIDOne, core.AccessAllowed, now, now.AddDate(0, 0, 30))
	current := persistence.CustomerEntitlement{
		Projection: allowed.Projection, SourceApplicationID: verificationApplicationID,
		SourceLineage: allowed.SourceLineage, Key: verificationEntitlementKey, Version: 1,
	}
	revoked := manualCandidate(verificationProductID, manualGrantIDOne, core.AccessDenied, now.Add(time.Minute), now.AddDate(0, 0, 30))
	store := lineageCandidate("observation-active", "reference:1", core.AccessAllowed, now, now.AddDate(0, 0, 5))

	projections, err := verification.NewDefaultProjector().Project(
		[]persistence.CustomerEntitlement{current}, []verification.ProjectionCandidate{revoked, store},
	)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].Access != core.AccessAllowed ||
		projections[0].SourceObservationID != "observation-active" {
		t.Fatalf("Project() = %#v, want the remaining store access", projections)
	}

	expired := verification.ProjectionCandidate{
		Projection:          projectionFor(verificationProductID, "observation-expired", core.AccessDenied),
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       "reference:1",
		ObservedAt:          now,
	}
	projections, err = verification.NewDefaultProjector().Project(
		[]persistence.CustomerEntitlement{current}, []verification.ProjectionCandidate{revoked, expired},
	)
	if err != nil {
		t.Fatalf("Project() without store access error = %v", err)
	}
	if len(projections) != 1 || projections[0].Access != core.AccessDenied ||
		projections[0].AccessReason != core.AccessReasonRevoked || projections[0].SourceKind != persistence.SourceKindManual {
		t.Fatalf("Project() without store access = %#v, want the revoked manual grant", projections)
	}
}

// TestDefaultProjectorManualGrantOutranksImportOnEqualEnd verifies equal ends prefer an audited operator grant over history.
func TestDefaultProjectorManualGrantOutranksImportOnEqualEnd(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	ends := now.AddDate(0, 0, 30)
	manual := manualCandidate("product-manual", manualGrantIDOne, core.AccessAllowed, now, ends)
	imported := importCandidate("product-import", "imp-55555555555555555555555555555555", core.AccessAllowed, now.Add(time.Hour))
	end := ends
	imported.Projection.EffectivePeriod = core.EffectivePeriod{StartsAt: now.AddDate(0, -1, 0), EndsAt: &end}

	projections, err := verification.NewDefaultProjector().Project(nil, []verification.ProjectionCandidate{imported, manual})
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if len(projections) != 1 || projections[0].SourceKind != persistence.SourceKindManual {
		t.Fatalf("Project() = %#v, want the manual grant", projections)
	}
}

// TestEntitlementChangedEventUsesManualSchema verifies version 3 names the manual grant and no observation.
func TestEntitlementChangedEventUsesManualSchema(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	candidate := manualCandidate(verificationProductID, manualGrantIDOne, core.AccessAllowed, now, now.AddDate(0, 0, 30))
	entitlement := persistence.CustomerEntitlement{
		Projection: candidate.Projection, SourceApplicationID: verificationApplicationID,
		Key: verificationEntitlementKey, Version: 3,
	}
	event, err := verification.EntitlementChangedEvent(verificationApplicationID, entitlement, now)
	if err != nil {
		t.Fatalf("EntitlementChangedEvent() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if payload["schema_version"] != float64(3) || payload["source"] != "manual" ||
		payload["source_manual_grant_id"] != manualGrantIDOne || payload["access"] != "allowed" ||
		payload["access_reason"] != "manual_grant" {
		t.Fatalf("payload = %#v, want manual schema 3", payload)
	}
	for _, field := range []string{"source_observation_id", "source_import_id", "verified_at", "reason"} {
		if _, exists := payload[field]; exists {
			t.Fatalf("manual payload included %s: %#v", field, payload)
		}
	}
	if event.AggregateID != string(verificationCustomerID) || event.ApplicationID != verificationApplicationID {
		t.Fatalf("event = %#v, want the normal customer outbox stream", event)
	}
}

// TestEntitlementChangedEventSchemaVersionsBySource verifies only manual grants use schema version 3.
func TestEntitlementChangedEventSchemaVersionsBySource(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	for name, test := range map[string]struct {
		projection persistence.EntitlementProjection
		version    float64
	}{
		"store observation": {projection: projectionFor(verificationProductID, "observation-1", core.AccessAllowed), version: 1},
		"import":            {projection: importProjection(verificationProductID, "imp-55555555555555555555555555555555", core.AccessAllowed), version: 2},
		"manual grant": {
			projection: manualCandidate(verificationProductID, manualGrantIDOne, core.AccessAllowed, now, now.AddDate(0, 0, 30)).Projection,
			version:    3,
		},
		"revoked manual grant": {
			projection: manualCandidate(verificationProductID, manualGrantIDOne, core.AccessDenied, now, now.AddDate(0, 0, 30)).Projection,
			version:    3,
		},
	} {
		event, err := verification.EntitlementChangedEvent(verificationApplicationID, persistence.CustomerEntitlement{
			Projection: test.projection, SourceApplicationID: verificationApplicationID,
			Key: verificationEntitlementKey, Version: 1,
		}, now)
		if err != nil {
			t.Fatalf("%s EntitlementChangedEvent() error = %v", name, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("%s unmarshal event: %v", name, err)
		}
		if payload["schema_version"] != test.version {
			t.Fatalf("%s schema_version = %v, want %v", name, payload["schema_version"], test.version)
		}
		if test.version != 3 && payload["access_reason"] == "manual_grant" {
			t.Fatalf("%s payload carries the manual grant reason: %#v", name, payload)
		}
	}
}

// manualCandidate builds one manual grant candidate. A denied candidate is a revoked grant.
func manualCandidate(
	productID core.ProductID,
	grantID string,
	access core.AccessStatus,
	observedAt time.Time,
	endsAt time.Time,
) verification.ProjectionCandidate {
	end := endsAt
	reason := core.AccessReasonManualGrant
	if access != core.AccessAllowed {
		reason = core.AccessReasonRevoked
	}
	return verification.ProjectionCandidate{
		Projection: persistence.EntitlementProjection{
			ProjectID:           verificationProjectID,
			CustomerID:          verificationCustomerID,
			EntitlementID:       verificationEntitlementID,
			SourceKind:          persistence.SourceKindManual,
			SourceManualGrantID: grantID,
			SourceProductID:     productID,
			Access:              access,
			AccessReason:        reason,
			EffectivePeriod:     core.EffectivePeriod{StartsAt: endsAt.AddDate(0, 0, -30), EndsAt: &end},
		},
		SourceApplicationID: verificationApplicationID,
		SourceLineage:       "manual:" + grantID,
		ObservedAt:          observedAt,
	}
}
