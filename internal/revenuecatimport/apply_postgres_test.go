package revenuecatimport

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/verification"
	"github.com/jackc/pgx/v5"
)

const (
	// importDatabaseURLKey names the opt-in PostgreSQL integration-test setting.
	importDatabaseURLKey = "IAPSTACK_TEST_DATABASE_URL"
	// importDatabaseAdvisoryLock serializes schema-resetting integration tests.
	importDatabaseAdvisoryLock int64 = 424090117
)

// importAdapter returns one configured provider result.
type importAdapter struct {
	result stores.VerificationResult
}

// importProtector returns deterministic ciphertext for the verification fixture.
type importProtector struct{}

// importClock returns one fixed verification time.
type importClock struct {
	now time.Time
}

// TestApplyIsIdempotentAndLosesToAStoreObservation verifies grants, webhooks, and supersession.
func TestApplyIsIdempotentAndLosesToAStoreObservation(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv(importDatabaseURLKey))
	if databaseURL == "" {
		t.Skipf("%s is not configured", importDatabaseURLKey)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, importDatabaseAdvisoryLock); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("lock: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_, _ = connection.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1)`, importDatabaseAdvisoryLock)
		_ = connection.Close(cleanupCtx)
	})
	if err := postgres.RemoveRiver(ctx, databaseURL); err != nil {
		t.Fatalf("RemoveRiver() error = %v", err)
	}
	migrator, err := postgres.NewMigrator(ctx, connection)
	if err != nil {
		t.Fatalf("NewMigrator() error = %v", err)
	}
	if err := postgres.ClearIdentityMigrationGuards(ctx, connection); err != nil {
		t.Fatalf("clear guards: %v", err)
	}
	if err := migrator.MigrateTo(ctx, 0); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := migrator.Up(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := postgres.MigrateRiver(ctx, databaseURL); err != nil {
		t.Fatalf("MigrateRiver() error = %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		_ = postgres.RemoveRiver(cleanupCtx, databaseURL)
		_ = postgres.ClearIdentityMigrationGuards(cleanupCtx, connection)
		_ = migrator.MigrateTo(cleanupCtx, 0)
	})
	store, err := postgres.OpenStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(store.Close)
	seedImportCatalog(t, ctx, connection)

	now := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	input := importInput(now, importCSV("2026-10-15 00:00:00", "2026-10-20 00:00:00"))
	input.BatchID = "revenuecat-2026-10-01"
	first, err := Apply(ctx, store, input, false)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if first.Counts.CustomersCreated != 1 || first.Counts.GrantsUpserted != 1 || first.Counts.ProjectionsChanged != 1 || first.Counts.WebhooksEnqueued != 0 {
		t.Fatalf("first counts = %#v", first.Counts)
	}
	version := importProjectionVersion(t, ctx, connection)
	if version != 1 || importCount(t, ctx, connection, "purchase_observations") != 0 || importCount(t, ctx, connection, "import_grants") != 1 || importCount(t, ctx, connection, "outbox_events") != 0 {
		t.Fatalf("version=%d observations=%d grants=%d outbox=%d", version, importCount(t, ctx, connection, "purchase_observations"), importCount(t, ctx, connection, "import_grants"), importCount(t, ctx, connection, "outbox_events"))
	}
	if access := importAccess(t, ctx, connection); access != "allowed" {
		t.Fatalf("stored access = %s, want allowed until the read path", access)
	}
	second, err := Apply(ctx, store, input, false)
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if second.Counts.CustomersReused != 1 || second.Counts.GrantsUnchanged != 1 || second.Counts.ProjectionsChanged != 0 || second.Counts.WebhooksEnqueued != 0 || importProjectionVersion(t, ctx, connection) != version || importCount(t, ctx, connection, "outbox_events") != 0 {
		t.Fatalf("second counts = %#v version=%d", second.Counts, importProjectionVersion(t, ctx, connection))
	}

	moved := importInput(now, importCSV("2026-12-01 00:00:00", "2026-10-21 00:00:00"))
	moved.BatchID = "revenuecat-2026-10-02"
	third, err := Apply(ctx, store, moved, true)
	if err != nil {
		t.Fatalf("third Apply() error = %v", err)
	}
	if third.Counts.GrantsUpserted != 1 || third.Counts.ProjectionsChanged != 1 || third.Counts.WebhooksEnqueued != 1 || importProjectionVersion(t, ctx, connection) != 2 {
		t.Fatalf("third counts = %#v version=%d", third.Counts, importProjectionVersion(t, ctx, connection))
	}
	payload := importOutboxPayload(t, ctx, connection)
	if payload["source"] != "import" || payload["source_import_id"] == "" {
		t.Fatalf("outbox payload = %#v", payload)
	}
	if _, exists := payload["source_observation_id"]; exists {
		t.Fatalf("outbox payload included source_observation_id: %#v", payload)
	}
	if _, exists := payload["verified_at"]; exists {
		t.Fatalf("outbox payload included verified_at: %#v", payload)
	}

	verifyImportedDenial(t, ctx, store)
	if importProjectionVersion(t, ctx, connection) != 3 {
		t.Fatalf("version after observation = %d", importProjectionVersion(t, ctx, connection))
	}
	var sourceKind, observationID string
	var importID *string
	if err := connection.QueryRow(ctx, `
		SELECT source_kind, COALESCE(source_observation_id, ''), source_import_id
		FROM customer_entitlements WHERE project_id = 'project-rc'
	`).Scan(&sourceKind, &observationID, &importID); err != nil {
		t.Fatalf("read projection: %v", err)
	}
	if sourceKind != "store_observation" || observationID == "" || importID != nil {
		t.Fatalf("projection source = %s observation=%s import=%v", sourceKind, observationID, importID)
	}
	if importCount(t, ctx, connection, "import_grants") != 1 {
		t.Fatal("import grant was deleted")
	}
	overview, err := store.AdminProjectOverview(ctx, "project-rc", persistence.AdminRevenueQuery{})
	if err != nil {
		t.Fatalf("AdminProjectOverview() error = %v", err)
	}
	for _, transaction := range overview.RecentTransactions {
		if strings.HasPrefix(string(transaction.ID), "imp-") {
			t.Fatalf("recent transactions listed import grant %s", transaction.ID)
		}
	}
	if len(overview.RecentTransactions) != 1 {
		t.Fatalf("recent transactions = %#v", overview.RecentTransactions)
	}
}

// importInput builds one apply request for the seeded catalog.
func importInput(now time.Time, export string) Input {
	return Input{
		ProjectID: "project-rc",
		Mapping: Mapping{
			ProjectID: "project-rc", CanonicalIDColumn: columnOriginalAppUserID,
			Entitlements: map[string]string{"premium": "premium"},
			Products: []ProductMapping{{
				Store: "app_store", Environment: "production", ProviderProductID: "com.example.annual",
				ApplicationID: "application-rc", ProductID: "product-rc",
			}},
		},
		Format: FormatTransactionsCSV,
		Export: []byte(export),
		Now:    now,
	}
}

// importCSV returns one subscription row with the supplied effective end and update time.
func importCSV(effectiveEnd, updated string) string {
	return "rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,start_time,effective_end_time,store,is_auto_renewable,is_sandbox,updated_at,original_store_transaction_id\n" +
		"user-rc,user-rc,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00," + effectiveEnd + ",app_store,true,false," + updated + ",original-rc\n"
}

// seedImportCatalog inserts the project records the fixture mapping names.
func seedImportCatalog(t *testing.T, ctx context.Context, connection *pgx.Conn) {
	t.Helper()
	statements := []string{
		`INSERT INTO projects (id) VALUES ('project-rc')`,
		`INSERT INTO applications (id, project_id, provider, environment, provider_application_id) VALUES ('application-rc', 'project-rc', 'apple_app_store', 'production', 'apple-rc')`,
		`INSERT INTO entitlements (id, project_id, key) VALUES ('entitlement-rc', 'project-rc', 'premium')`,
		`INSERT INTO products (id, project_id, kind) VALUES ('product-rc', 'project-rc', 'subscription')`,
		`INSERT INTO product_entitlements (project_id, product_id, entitlement_id) VALUES ('project-rc', 'product-rc', 'entitlement-rc')`,
		`INSERT INTO store_products (project_id, application_id, product_id, provider_product_id) VALUES ('project-rc', 'application-rc', 'product-rc', 'com.example.annual')`,
	}
	for _, statement := range statements {
		if _, err := connection.Exec(ctx, statement); err != nil {
			t.Fatalf("seed catalog: %v", err)
		}
	}
}

// importCount returns one table's row count. The table name is a fixed literal from the test.
func importCount(t *testing.T, ctx context.Context, connection *pgx.Conn, table string) int {
	t.Helper()
	switch table {
	case "purchase_observations", "import_grants", "outbox_events":
	default:
		t.Fatalf("unexpected table %s", table)
	}
	var count int
	if err := connection.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

// importProjectionVersion reads the only current projection version.
func importProjectionVersion(t *testing.T, ctx context.Context, connection *pgx.Conn) int64 {
	t.Helper()
	var version int64
	if err := connection.QueryRow(ctx, `SELECT version FROM customer_entitlements WHERE project_id = 'project-rc'`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	return version
}

// importAccess reads the stored access status.
func importAccess(t *testing.T, ctx context.Context, connection *pgx.Conn) string {
	t.Helper()
	var access string
	if err := connection.QueryRow(ctx, `SELECT access_status FROM customer_entitlements WHERE project_id = 'project-rc'`).Scan(&access); err != nil {
		t.Fatalf("read access: %v", err)
	}
	return access
}

// importOutboxPayload reads the only webhook body.
func importOutboxPayload(t *testing.T, ctx context.Context, connection *pgx.Conn) map[string]any {
	t.Helper()
	var payload []byte
	if err := connection.QueryRow(ctx, `SELECT payload FROM outbox_events WHERE project_id = 'project-rc'`).Scan(&payload); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode outbox: %v", err)
	}
	return decoded
}

// verifyImportedDenial stores one denied observation through the verification use case.
func verifyImportedDenial(t *testing.T, ctx context.Context, store *postgres.Store) {
	t.Helper()
	evidence, err := stores.NewEvidence("application/json", []byte(`{"order":"import-observation"}`))
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	reference, err := core.NewStoreReference(core.ReferenceQuery, "original_transaction_id", "original-rc")
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	observedAt := time.Date(2026, time.October, 2, 0, 0, 0, 0, time.UTC)
	starts := observedAt.Add(-time.Hour)
	ends := observedAt
	registry, err := stores.NewRegistry(&importAdapter{result: stores.VerificationResult{
		VerifiedAt: observedAt,
		Artifacts:  []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: evidence}},
		Observations: []core.PurchaseObservation{{
			ID: "observation-rc", ApplicationID: "application-rc",
			Store:     core.StoreApplication{Provider: core.ProviderAppleAppStore, Environment: core.EnvironmentProduction, ID: "apple-rc"},
			ProductID: "com.example.annual", ProductKind: core.ProductKindSubscription,
			State: core.LifecycleExpired, ProviderState: "expired",
			Access: core.AccessDenied, AccessReason: core.AccessReasonExpired,
			Ownership: core.OwnershipPurchased, Quantity: 1,
			OccurredAt: starts, ObservedAt: observedAt,
			EffectivePeriod: core.EffectivePeriod{StartsAt: starts, EndsAt: &ends},
			Renewal:         core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable},
			References:      []core.StoreReference{reference},
		}},
	}})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	service, err := verification.NewService(store, registry, importProtector{}, verification.NewDefaultProjector(), importClock{now: observedAt})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := service.Verify(ctx, verification.Command{
		ProjectID: "project-rc", ApplicationID: "application-rc", ExternalCustomerID: "user-rc",
		ClaimedProducts: []core.ProviderProductID{"com.example.annual"}, Evidence: evidence,
	}); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

// Provider identifies the App Store adapter used by the fixture.
func (adapter *importAdapter) Provider() core.Provider { return core.ProviderAppleAppStore }

// Verify returns the configured denial.
func (adapter *importAdapter) Verify(context.Context, stores.VerificationRequest) (stores.VerificationResult, error) {
	return adapter.result, nil
}

// Reconcile is unused by this fixture.
func (adapter *importAdapter) Reconcile(context.Context, stores.ReconciliationRequest) (stores.VerificationResult, error) {
	return adapter.result, nil
}

// Protect returns ciphertext that does not retain the plaintext.
func (importProtector) Protect(_ context.Context, request protection.Request) (protection.Value, error) {
	return protection.Value{Ciphertext: []byte("ciphertext"), Fingerprint: sha256.Sum256(request.Bytes()), KeyID: "test-key"}, nil
}

// Now returns the fixture clock.
func (clock importClock) Now() time.Time { return clock.now }
