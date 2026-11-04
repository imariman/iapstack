package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/verification"
)

// lineageReferenceCase names the provider reference that carries one purchase lineage.
type lineageReferenceCase struct {
	name  string
	role  core.ReferenceRole
	kind  string
	query bool
}

// lineageSnapshot describes one stored subscription snapshot in a purchase lineage.
type lineageSnapshot struct {
	id          core.ObservationID
	lineage     string
	transaction string
	access      core.AccessStatus
	startsAt    time.Time
	endsAt      time.Time
	observedAt  time.Time
}

// TestEntitlementSourcesKeepEveryPurchaseLineage verifies a refreshed expired lineage cannot revoke a later purchase.
func TestEntitlementSourcesKeepEveryPurchaseLineage(t *testing.T) {
	cases := []lineageReferenceCase{
		{name: "app store original transaction", role: core.ReferenceLineage, kind: "original_transaction_id"},
		{name: "google play purchase token", role: core.ReferenceLineage, kind: "purchase_token", query: true},
		{name: "huawei purchase token", role: core.ReferenceQuery, kind: "purchase_token"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			database := openTestDatabase(t, postgres.LatestVersion)
			fixture := seedCatalog(t, database)
			mustExec(t, database, `UPDATE products SET kind = 'subscription' WHERE id = $1`, fixture.productID)
			store := openRepositoryStore(t, database.ctx)
			projectID := core.ProjectID(fixture.projectID)
			customerID := core.CustomerID(fixture.customerID)
			start := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
			evidenceID := saveLineageEvidence(t, database.ctx, store, fixture)

			saveLineageObservation(t, database.ctx, store, fixture, evidenceID, testCase, lineageSnapshot{
				id: "observation-lineage-a-active", lineage: "lineage-a", transaction: "transaction-a-1",
				access: core.AccessAllowed, startsAt: start, endsAt: start.AddDate(0, 1, 0), observedAt: start,
			})
			expired := lineageSnapshot{
				id: "observation-lineage-a-expired", lineage: "lineage-a", transaction: "transaction-a-1",
				access: core.AccessDenied, startsAt: start, endsAt: start.AddDate(0, 1, 0),
				observedAt: start.AddDate(0, 1, 1),
			}
			saveLineageObservation(t, database.ctx, store, fixture, evidenceID, testCase, expired)
			resubscribedAt := start.AddDate(0, 1, 2)
			saveLineageObservation(t, database.ctx, store, fixture, evidenceID, testCase, lineageSnapshot{
				id: "observation-lineage-b-active", lineage: "lineage-b", transaction: "transaction-b-1",
				access: core.AccessAllowed, startsAt: resubscribedAt, endsAt: resubscribedAt.AddDate(0, 1, 0),
				observedAt: resubscribedAt,
			})
			refreshLineageProjection(t, database.ctx, store, projectID, customerID, resubscribedAt)

			expired.observedAt = resubscribedAt.Add(24 * time.Hour)
			saveLineageObservation(t, database.ctx, store, fixture, evidenceID, testCase, expired)
			refreshLineageProjection(t, database.ctx, store, projectID, customerID, expired.observedAt)

			sources, entitlements := loadLineageState(t, database.ctx, store, projectID, customerID)
			if len(entitlements) != 1 || entitlements[0].Projection.Access != core.AccessAllowed ||
				entitlements[0].Projection.SourceObservationID != "observation-lineage-b-active" {
				t.Fatalf("entitlements = %#v, want access from the active lineage", entitlements)
			}
			if len(sources) != 2 {
				t.Fatalf("entitlement sources = %#v, want one latest snapshot per lineage", sources)
			}
			byObservation := make(map[core.ObservationID]persistence.EntitlementSource, len(sources))
			for _, source := range sources {
				byObservation[source.Projection.SourceObservationID] = source
			}
			expiredSource, expiredExists := byObservation["observation-lineage-a-expired"]
			activeSource, activeExists := byObservation["observation-lineage-b-active"]
			if !expiredExists || !activeExists ||
				!expiredSource.ObservedAt.Equal(expired.observedAt) ||
				expiredSource.SourceLineage == "" || activeSource.SourceLineage == "" ||
				expiredSource.SourceLineage == activeSource.SourceLineage {
				t.Fatalf("entitlement sources = %#v, want refreshed expired lineage and active lineage", sources)
			}
			if entitlements[0].SourceLineage != activeSource.SourceLineage {
				t.Fatalf("entitlement lineage = %q, want active source lineage %q",
					entitlements[0].SourceLineage, activeSource.SourceLineage)
			}
		})
	}
}

// TestEntitlementSourcesKeepEveryImportLineage verifies a newer denied import lineage cannot hide an allowed one.
func TestEntitlementSourcesKeepEveryImportLineage(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	projectID := core.ProjectID(fixture.projectID)
	customerID := core.CustomerID(fixture.customerID)
	observedAt := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	allowed := persistence.ImportGrant{
		ID: "imp-66666666666666666666666666666666", ProjectID: projectID, CustomerID: customerID,
		ApplicationID: core.ApplicationID(fixture.applicationID), ProductID: core.ProductID(fixture.productID),
		EntitlementID: core.EntitlementID(fixture.entitlementID),
		Origin:        persistence.ImportOriginRevenueCat, OriginStore: "app_store", Environment: core.EnvironmentSandbox,
		Access: core.AccessAllowed, AccessReason: core.AccessReasonPurchaseValid,
		EffectivePeriod: core.EffectivePeriod{StartsAt: observedAt.Add(-time.Hour)}, ObservedAt: observedAt,
		ExportSHA256: [32]byte{1}, LineageFingerprint: [32]byte{6},
	}
	denied := allowed
	denied.ID = "imp-77777777777777777777777777777777"
	denied.Access = core.AccessDenied
	denied.AccessReason = core.AccessReasonExpired
	denied.EffectivePeriod = core.EffectivePeriod{}
	denied.ObservedAt = observedAt.Add(time.Hour)
	denied.LineageFingerprint = [32]byte{7}
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		writer, ok := repository.(interface {
			UpsertImportGrant(context.Context, persistence.ImportGrant) (persistence.ImportGrantResult, error)
		})
		if !ok {
			return errors.New("import grant writer is unavailable")
		}
		for _, grant := range []persistence.ImportGrant{allowed, denied} {
			if _, err := writer.UpsertImportGrant(database.ctx, grant); err != nil {
				return err
			}
		}
		return verification.RefreshCustomerProjection(
			database.ctx, repository, verification.NewDefaultProjector(), projectID, customerID, denied.ObservedAt,
		)
	})
	if err != nil {
		t.Fatalf("import lineage projection error = %v", err)
	}

	sources, entitlements := loadLineageState(t, database.ctx, store, projectID, customerID)
	if len(entitlements) != 1 || entitlements[0].Projection.Access != core.AccessAllowed ||
		entitlements[0].Projection.SourceImportID != allowed.ID {
		t.Fatalf("import entitlements = %#v, want the allowed import lineage", entitlements)
	}
	if len(sources) != 2 || sources[0].SourceLineage == sources[1].SourceLineage {
		t.Fatalf("import sources = %#v, want one source per import lineage", sources)
	}
}

// saveLineageEvidence stores one client submission shared by every lineage snapshot in a test.
func saveLineageEvidence(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	fixture catalogFixture,
) int64 {
	t.Helper()
	evidence := newPurchaseWrites(t, fixture).evidence
	var evidenceID int64
	err := store.Transact(ctx, func(repository persistence.Transaction) error {
		var saveErr error
		evidenceID, saveErr = repository.SaveEvidence(ctx, evidence)
		return saveErr
	})
	if err != nil {
		t.Fatalf("SaveEvidence() error = %v", err)
	}
	return evidenceID
}

// saveLineageObservation stores one subscription snapshot with its lineage and transaction references.
func saveLineageObservation(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	fixture catalogFixture,
	evidenceID int64,
	lineage lineageReferenceCase,
	snapshot lineageSnapshot,
) {
	t.Helper()
	type referenceValue struct {
		role  core.ReferenceRole
		kind  string
		value string
	}
	values := []referenceValue{
		{role: core.ReferenceTransaction, kind: "transaction_id", value: snapshot.transaction},
		{role: lineage.role, kind: lineage.kind, value: snapshot.lineage},
	}
	if lineage.query {
		values = append(values, referenceValue{role: core.ReferenceQuery, kind: lineage.kind, value: snapshot.lineage})
	}
	references := make([]core.StoreReference, 0, len(values))
	protected := make([]persistence.ProtectedReference, 0, len(values))
	for _, value := range values {
		reference, err := core.NewStoreReference(value.role, value.kind, value.value)
		if err != nil {
			t.Fatalf("NewStoreReference() error = %v", err)
		}
		references = append(references, reference)
		protected = append(protected, persistence.ProtectedReference{
			Role: value.role, Kind: value.kind, Value: repositoryProtectedValue(value.value),
		})
	}
	state := core.LifecycleActive
	reason := core.AccessReasonPurchaseValid
	renewal := core.Renewal{Mode: core.RenewalAuto, Status: core.RenewalEnabled}
	if snapshot.access == core.AccessDenied {
		state = core.LifecycleExpired
		reason = core.AccessReasonExpired
		renewal.Status = core.RenewalDisabled
	}
	endsAt := snapshot.endsAt
	write := persistence.ObservationWrite{
		EvidenceID: evidenceID,
		ProjectID:  core.ProjectID(fixture.projectID),
		CustomerID: core.CustomerID(fixture.customerID),
		ProductID:  core.ProductID(fixture.productID),
		Observation: core.PurchaseObservation{
			ID:            snapshot.id,
			ApplicationID: core.ApplicationID(fixture.applicationID),
			Store: core.StoreApplication{
				Provider:    core.ProviderHuaweiAppGallery,
				Environment: core.EnvironmentSandbox,
				ID:          core.ProviderApplicationID(fixture.providerApplication),
			},
			ProductID:       core.ProviderProductID(fixture.providerProductID),
			ProductKind:     core.ProductKindSubscription,
			State:           state,
			ProviderState:   string(state),
			Access:          snapshot.access,
			AccessReason:    reason,
			Ownership:       core.OwnershipPurchased,
			Quantity:        1,
			OccurredAt:      snapshot.startsAt,
			ObservedAt:      snapshot.observedAt,
			EffectivePeriod: core.EffectivePeriod{StartsAt: snapshot.startsAt, EndsAt: &endsAt},
			Renewal:         renewal,
			References:      references,
		},
		References: protected,
	}
	err := store.Transact(ctx, func(repository persistence.Transaction) error {
		return repository.SaveObservation(ctx, write)
	})
	if err != nil {
		t.Fatalf("SaveObservation(%s) error = %v", snapshot.id, err)
	}
}

// refreshLineageProjection recomputes the customer projection with the default projector.
func refreshLineageProjection(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	projectID core.ProjectID,
	customerID core.CustomerID,
	occurredAt time.Time,
) {
	t.Helper()
	err := store.Transact(ctx, func(repository persistence.Transaction) error {
		return verification.RefreshCustomerProjection(
			ctx, repository, verification.NewDefaultProjector(), projectID, customerID, occurredAt,
		)
	})
	if err != nil {
		t.Fatalf("RefreshCustomerProjection() error = %v", err)
	}
}

// loadLineageState reads the source candidates and the current projection for one customer.
func loadLineageState(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	projectID core.ProjectID,
	customerID core.CustomerID,
) ([]persistence.EntitlementSource, []persistence.CustomerEntitlement) {
	t.Helper()
	var sources []persistence.EntitlementSource
	var entitlements []persistence.CustomerEntitlement
	err := store.Transact(ctx, func(repository persistence.Transaction) error {
		var loadErr error
		sources, loadErr = repository.CustomerEntitlementSources(ctx, projectID, customerID)
		if loadErr != nil {
			return loadErr
		}
		entitlements, loadErr = repository.CustomerEntitlements(ctx, projectID, customerID)
		return loadErr
	})
	if err != nil {
		t.Fatalf("load lineage state error = %v", err)
	}
	return sources, entitlements
}
