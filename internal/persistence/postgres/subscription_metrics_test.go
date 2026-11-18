package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

// subscriptionSnapshotSpec describes one stored subscription observation for metrics tests.
type subscriptionSnapshotSpec struct {
	id          string
	application string
	provider    core.Provider
	environment core.Environment
	customer    string
	state       core.LifecycleState
	access      core.AccessStatus
	reason      core.AccessReason
	renewal     core.RenewalStatus
	ownership   core.Ownership
	startsAgo   time.Duration
	endsIn      time.Duration
	observedAgo time.Duration
	storedAgo   time.Duration
	phase       core.OfferPhase
	transaction string
	lineage     string
	lineageKind string
	linked      string
}

// TestAdminSubscriptionMetricsReadsStoredLineages verifies the SQL lineage evidence against the stated definitions.
func TestAdminSubscriptionMetricsReadsStoredLineages(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	store := openRepositoryStore(t, database.ctx)
	const projectID = "project-metrics"
	mustExec(t, database, `INSERT INTO projects (id) VALUES ($1)`, projectID)
	mustExec(t, database, `INSERT INTO products (id, project_id, kind) VALUES ('monthly', $1, 'subscription')`, projectID)
	for _, application := range []struct{ id, provider, environment string }{
		{"app-apple", "apple_app_store", "production"},
		{"app-google", "google_play", "production"},
		{"app-huawei-sandbox", "huawei_appgallery", "sandbox"},
	} {
		mustExec(t, database, `
			INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
			VALUES ($1, $2, $3, $4, $1)
		`, application.id, projectID, application.provider, application.environment)
		mustExec(t, database, `
			INSERT INTO store_products (project_id, application_id, product_id, provider_product_id)
			VALUES ($1, $2, 'monthly', 'store-monthly')
		`, projectID, application.id)
	}
	for _, customer := range []string{"customer-1", "customer-2", "customer-3", "customer-4", "customer-5", "customer-6"} {
		mustExec(t, database, `INSERT INTO customers (id, project_id, external_id) VALUES ($1, $2, $1)`, customer, projectID)
	}
	var base time.Time
	if err := database.conn.QueryRow(database.ctx, `SELECT now()`).Scan(&base); err != nil {
		t.Fatalf("read database time: %v", err)
	}
	save := func(spec subscriptionSnapshotSpec) {
		t.Helper()
		storeSubscriptionSnapshot(t, database, store, projectID, base, spec)
	}

	day := 24 * time.Hour
	apple := func(spec subscriptionSnapshotSpec) subscriptionSnapshotSpec {
		spec.application, spec.provider, spec.environment, spec.lineageKind = "app-apple", core.ProviderAppleAppStore, core.EnvironmentProduction, "original_transaction_id"
		return spec
	}
	google := func(spec subscriptionSnapshotSpec) subscriptionSnapshotSpec {
		spec.application, spec.provider, spec.environment, spec.lineageKind = "app-google", core.ProviderGooglePlay, core.EnvironmentProduction, "purchase_token"
		return spec
	}
	allowed := func(spec subscriptionSnapshotSpec) subscriptionSnapshotSpec {
		spec.state, spec.access, spec.reason, spec.renewal = core.LifecycleActive, core.AccessAllowed, core.AccessReasonPurchaseValid, core.RenewalEnabled
		return spec
	}

	// Lineage 1: a free trial stored first, then a converted paid period. The paid snapshot is saved
	// once without a phase and replayed with one, so the stored row is enriched.
	save(apple(allowed(subscriptionSnapshotSpec{
		id: "l1-trial-live", customer: "customer-1", startsAgo: 20 * day, endsIn: -13 * day, observedAgo: 19 * day,
		storedAgo: 20 * day, phase: core.OfferPhaseFreeTrial, transaction: "l1-t1", lineage: "l1",
	})))
	paid := apple(allowed(subscriptionSnapshotSpec{
		id: "l1-paid", customer: "customer-1", startsAgo: 13 * day, endsIn: 17 * day, observedAgo: day,
		storedAgo: 13 * day, transaction: "l1-t2", lineage: "l1",
	}))
	save(paid)
	paid.phase = core.OfferPhaseBase
	save(paid)

	// Lineage 2: active, then canceled with renewal off, then expired inside the window.
	save(apple(allowed(subscriptionSnapshotSpec{
		id: "l2-active", customer: "customer-2", startsAgo: 40 * day, endsIn: -10 * day, observedAgo: 40 * day,
		storedAgo: 40 * day, phase: core.OfferPhaseBase, transaction: "l2-t1", lineage: "l2",
	})))
	canceled := apple(allowed(subscriptionSnapshotSpec{
		id: "l2-canceled", customer: "customer-2", startsAgo: 40 * day, endsIn: -10 * day, observedAgo: 15 * day,
		storedAgo: 15 * day, phase: core.OfferPhaseBase, transaction: "l2-t1", lineage: "l2",
	}))
	canceled.state, canceled.reason, canceled.renewal = core.LifecycleCanceled, core.AccessReasonCanceledAtPeriodEnd, core.RenewalDisabled
	save(canceled)
	expired := canceled
	expired.id, expired.observedAgo, expired.storedAgo = "l2-expired", 9*day, 9*day
	expired.state, expired.access, expired.reason = core.LifecycleExpired, core.AccessDenied, core.AccessReasonExpired
	save(expired)

	// Lineage 3: a new purchase refunded two days ago. Access is counted until the refund was stored.
	save(apple(allowed(subscriptionSnapshotSpec{
		id: "l3-active", customer: "customer-3", startsAgo: 5 * day, endsIn: 25 * day, observedAgo: 5 * day,
		storedAgo: 5 * day, phase: core.OfferPhaseBase, transaction: "l3-t1", lineage: "l3",
	})))
	refunded := apple(subscriptionSnapshotSpec{
		id: "l3-refunded", customer: "customer-3", state: core.LifecycleRefunded, access: core.AccessDenied,
		reason: core.AccessReasonRefunded, renewal: core.RenewalDisabled, startsAgo: 5 * day, endsIn: 25 * day,
		observedAgo: 2 * day, storedAgo: 2 * day, phase: core.OfferPhaseBase, transaction: "l3-t1", lineage: "l3",
	})
	save(refunded)

	// Lineages 4 and 5: a Google Play plan change onto a new, linked purchase token.
	oldToken := google(allowed(subscriptionSnapshotSpec{
		id: "l4-active", customer: "customer-4", startsAgo: 60 * day, endsIn: -3 * day, observedAgo: 4 * day,
		storedAgo: 60 * day, phase: core.OfferPhaseBase, transaction: "l4-t1", lineage: "token-old",
	}))
	save(oldToken)
	save(google(allowed(subscriptionSnapshotSpec{
		id: "l5-active", customer: "customer-4", startsAgo: 3 * day, endsIn: 27 * day, observedAgo: day,
		storedAgo: 3 * day, phase: core.OfferPhaseBase, transaction: "l5-t1", lineage: "token-new", linked: "token-old",
	})))

	// Family-shared access and a sandbox application are outside the production subscription count.
	familyShared := apple(allowed(subscriptionSnapshotSpec{
		id: "family", customer: "customer-5", startsAgo: 2 * day, endsIn: 28 * day, observedAgo: day,
		storedAgo: 2 * day, phase: core.OfferPhaseBase, transaction: "family-t1", lineage: "family",
	}))
	familyShared.ownership = core.OwnershipFamilyShared
	save(familyShared)
	save(allowed(subscriptionSnapshotSpec{
		id: "sandbox", application: "app-huawei-sandbox", provider: core.ProviderHuaweiAppGallery,
		environment: core.EnvironmentSandbox, customer: "customer-6", startsAgo: 2 * day, endsIn: 28 * day,
		observedAgo: day, storedAgo: 2 * day, transaction: "sandbox-t1", lineage: "sandbox", lineageKind: "purchase_token",
	}))

	var phase *string
	if err := database.conn.QueryRow(database.ctx, `SELECT offer_phase FROM purchase_observations WHERE id = 'l1-paid'`).Scan(&phase); err != nil {
		t.Fatalf("read enriched offer phase: %v", err)
	}
	if phase == nil || *phase != string(core.OfferPhaseBase) {
		t.Fatalf("enriched offer phase = %v, want base", phase)
	}

	metrics, err := store.AdminSubscriptionMetrics(database.ctx, projectID, persistence.SubscriptionMetricsQuery{
		Environment: core.EnvironmentProduction, WindowDays: 30,
	})
	if err != nil {
		t.Fatalf("AdminSubscriptionMetrics() error = %v", err)
	}
	for name, got := range map[string][2]int64{
		"active at start":      {metrics.ActiveAtStart, 2},
		"active at end":        {metrics.ActiveAtEnd, 2},
		"active during window": {metrics.ActiveDuringWindow, 5},
		"new":                  {metrics.New, 2},
		"renewals":             {metrics.Renewals, 1},
		"churned":              {metrics.Churn.Churned, 2},
		"voluntary":            {metrics.Churn.Voluntary, 1},
		"reversed":             {metrics.Churn.Reversed, 1},
		"replaced":             {metrics.Churn.Replaced, 1},
	} {
		if got[0] != got[1] {
			t.Errorf("%s = %d, want %d", name, got[0], got[1])
		}
	}
	if len(metrics.Daily) != 30 || metrics.Window.MeasuredAt.After(metrics.Window.End) {
		t.Fatalf("daily series = %d points, window = %#v", len(metrics.Daily), metrics.Window)
	}
	if len(metrics.Providers) != 2 || metrics.Providers[0].Provider != core.ProviderAppleAppStore ||
		metrics.Providers[1].Provider != core.ProviderGooglePlay {
		t.Fatalf("providers = %#v, want the production providers only", metrics.Providers)
	}
	appleRow := metrics.Providers[0]
	if appleRow.TrialsStarted != 1 || appleRow.TrialsEnded != 1 || appleRow.TrialsConverted != 1 ||
		appleRow.ActiveDuringWindow != 3 || appleRow.WithOfferPhase != 3 {
		t.Fatalf("Apple trial metrics = %#v", appleRow)
	}

	sandbox, err := store.AdminSubscriptionMetrics(database.ctx, projectID, persistence.SubscriptionMetricsQuery{
		Environment: core.EnvironmentSandbox, WindowDays: 7,
	})
	if err != nil {
		t.Fatalf("AdminSubscriptionMetrics(sandbox) error = %v", err)
	}
	if sandbox.ActiveAtEnd != 1 || sandbox.New != 1 || len(sandbox.Daily) != 7 || len(sandbox.Providers) != 1 ||
		sandbox.Providers[0].WithOfferPhase != 0 {
		t.Fatalf("sandbox metrics = %#v, want the Huawei sandbox lineage without an offer phase", sandbox)
	}

	if _, err := store.AdminSubscriptionMetrics(database.ctx, "missing-project", persistence.SubscriptionMetricsQuery{
		Environment: core.EnvironmentProduction, WindowDays: 30,
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("AdminSubscriptionMetrics(missing) error = %v, want ErrNotFound", err)
	}
}

// storeSubscriptionSnapshot stores one subscription observation through SaveObservation and pins its storage time.
// Every time is relative to one shared base, so back-to-back periods of different lineages meet exactly.
func storeSubscriptionSnapshot(
	t *testing.T,
	database *testDatabase,
	store *postgres.Store,
	projectID string,
	now time.Time,
	spec subscriptionSnapshotSpec,
) {
	t.Helper()

	startsAt := now.Add(-spec.startsAgo).UTC().Truncate(time.Microsecond)
	endsAt := now.Add(spec.endsIn).UTC().Truncate(time.Microsecond)
	ownership := spec.ownership
	if ownership == "" {
		ownership = core.OwnershipPurchased
	}
	references := make([]core.StoreReference, 0, 3)
	for _, candidate := range []struct {
		role  core.ReferenceRole
		kind  string
		value string
	}{
		{role: core.ReferenceTransaction, kind: "transaction_id", value: spec.transaction},
		{role: core.ReferenceLineage, kind: spec.lineageKind, value: spec.lineage},
		{role: core.ReferenceLinkedLineage, kind: spec.lineageKind, value: spec.linked},
	} {
		if candidate.value == "" {
			continue
		}
		reference, err := core.NewStoreReference(candidate.role, candidate.kind, candidate.value)
		if err != nil {
			t.Fatalf("NewStoreReference() error = %v", err)
		}
		references = append(references, reference)
	}
	protected := make([]persistence.ProtectedReference, 0, len(references))
	for _, reference := range references {
		protected = append(protected, persistence.ProtectedReference{
			Role: reference.Role, Kind: reference.Kind, Value: repositoryProtectedValue(reference.Value()),
		})
	}
	observation := core.PurchaseObservation{
		ID: core.ObservationID(spec.id), ApplicationID: core.ApplicationID(spec.application),
		Store: core.StoreApplication{
			Provider: spec.provider, Environment: spec.environment, ID: core.ProviderApplicationID(spec.application),
		},
		ProductID: "store-monthly", ProductKind: core.ProductKindSubscription,
		State: spec.state, ProviderState: string(spec.state), Access: spec.access, AccessReason: spec.reason,
		Ownership: ownership, Quantity: 1, OfferPhase: spec.phase,
		OccurredAt: startsAt, ObservedAt: now.Add(-spec.observedAgo).UTC().Truncate(time.Microsecond),
		EffectivePeriod: core.EffectivePeriod{StartsAt: startsAt, EndsAt: &endsAt},
		Renewal:         core.Renewal{Mode: core.RenewalAuto, Status: spec.renewal},
		References:      references,
	}
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		evidenceID, err := repository.SaveEvidence(database.ctx, persistence.EvidenceWrite{
			ProjectID: core.ProjectID(projectID), ApplicationID: observation.ApplicationID,
			CustomerID: core.CustomerID(spec.customer), Kind: purchaseEvidenceKind, ContentType: repositoryContentType,
			Payload: repositoryProtectedValue("evidence-" + spec.id), ReceivedAt: observation.ObservedAt,
		})
		if err != nil {
			return err
		}
		return repository.SaveObservation(database.ctx, persistence.ObservationWrite{
			ProjectID: core.ProjectID(projectID), CustomerID: core.CustomerID(spec.customer),
			ProductID: "monthly", EvidenceID: evidenceID, Observation: observation, References: protected,
		})
	})
	if err != nil {
		t.Fatalf("SaveObservation(%s) error = %v", spec.id, err)
	}
	mustExec(t, database, `UPDATE purchase_observations SET created_at = $2 WHERE id = $1`,
		spec.id, now.Add(-spec.storedAgo).UTC().Truncate(time.Microsecond))
}
