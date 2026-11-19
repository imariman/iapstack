//go:build integration

package reprojection

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/verification"
	"github.com/jackc/pgx/v5"
)

const (
	// reprojectionDatabaseURLKey names the opt-in PostgreSQL integration-test setting.
	reprojectionDatabaseURLKey = "IAPSTACK_TEST_DATABASE_URL"
	// reprojectionDatabaseAdvisoryLock serializes schema-resetting integration tests.
	reprojectionDatabaseAdvisoryLock int64 = 424090117
	// reprojectionProject is the fixture project.
	reprojectionProject = "project-rp"
	// staleCustomer holds a lapsed stored store row that hides a current import of another product.
	staleCustomer = "customer-stale"
	// currentCustomer already holds the projection the current projector selects.
	currentCustomer = "customer-current"
	// importOnlyCustomer holds only an import grant, so the default filter does not select it.
	importOnlyCustomer = "customer-import-only"
	// staleImportID is the current import grant of customer-stale.
	staleImportID = "imp-00000000000000000000000000000001"
)

// reprojectionDatabase is one migrated PostgreSQL fixture.
type reprojectionDatabase struct {
	ctx   context.Context
	conn  *pgx.Conn
	store *postgres.Store
}

// TestRunFixesAStaleStoreRowThatHidesACurrentImport verifies the dry run, the apply, the event,
// an already correct customer, the default filter, and rerun idempotency.
func TestRunFixesAStaleStoreRowThatHidesACurrentImport(t *testing.T) {
	database := openReprojectionDatabase(t)
	now := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	database.seed(t, now)
	options := Options{
		ProjectID: reprojectionProject,
		BatchSize: 1,
		Projector: verification.NewDefaultProjector(),
		Clock:     func() time.Time { return now },
	}
	if got := database.publicAccess(t, staleCustomer, now); got != core.AccessDenied {
		t.Fatalf("stale customer access before the run = %s, want denied", got)
	}
	currentVersion := database.version(t, currentCustomer)

	var dryReports []CustomerReport
	dry, err := Run(database.ctx, database.store, options, func(report CustomerReport) error {
		dryReports = append(dryReports, report)
		return nil
	})
	if err != nil {
		t.Fatalf("dry Run() error = %v", err)
	}
	if dry.Mode != ModeDryRun || dry.CustomersScanned != 2 || dry.CustomersSkipped != 0 ||
		dry.CustomersChanged != 1 || dry.ProjectionsChanged != 1 || dry.WebhooksEnqueued != 1 {
		t.Fatalf("dry summary = %#v", dry)
	}
	assertStaleReport(t, dryReports, ModeDryRun, now)
	if source := database.sourceKind(t, staleCustomer); source != persistence.SourceKindStoreObservation {
		t.Fatalf("stale source after dry run = %s, want the stored store row kept", source)
	}
	if database.version(t, staleCustomer) != 1 || database.outboxCount(t) != 0 {
		t.Fatalf("dry run wrote: version=%d outbox=%d", database.version(t, staleCustomer), database.outboxCount(t))
	}

	options.Apply = true
	var applyReports []CustomerReport
	applied, err := Run(database.ctx, database.store, options, func(report CustomerReport) error {
		applyReports = append(applyReports, report)
		return nil
	})
	if err != nil {
		t.Fatalf("apply Run() error = %v", err)
	}
	if applied.Mode != ModeApply || applied.CustomersScanned != 2 || applied.CustomersChanged != 1 ||
		applied.ProjectionsChanged != 1 || applied.WebhooksEnqueued != 1 {
		t.Fatalf("apply summary = %#v", applied)
	}
	assertStaleReport(t, applyReports, ModeApply, now)
	if got := database.publicAccess(t, staleCustomer, now); got != core.AccessAllowed {
		t.Fatalf("stale customer access after apply = %s, want allowed", got)
	}
	if source := database.sourceKind(t, staleCustomer); source != persistence.SourceKindImport {
		t.Fatalf("stale source after apply = %s, want import", source)
	}
	if database.version(t, staleCustomer) != 2 || database.outboxCount(t) != 1 {
		t.Fatalf("apply wrote: version=%d outbox=%d", database.version(t, staleCustomer), database.outboxCount(t))
	}
	payload := database.outboxPayload(t)
	if payload["customer_id"] != staleCustomer || payload["access"] != string(core.AccessAllowed) ||
		payload["source"] != string(persistence.SourceKindImport) || payload["source_import_id"] != staleImportID {
		t.Fatalf("outbox payload = %#v", payload)
	}

	options.ProjectID = ""
	rerun, err := Run(database.ctx, database.store, options, func(report CustomerReport) error {
		t.Fatalf("rerun reported a change: %#v", report)
		return nil
	})
	if err != nil {
		t.Fatalf("rerun Run() error = %v", err)
	}
	if len(rerun.Projects) != 1 || rerun.Projects[0] != reprojectionProject || rerun.CustomersScanned != 2 ||
		rerun.CustomersChanged != 0 || rerun.ProjectionsChanged != 0 || rerun.WebhooksEnqueued != 0 {
		t.Fatalf("rerun summary = %#v", rerun)
	}
	if database.version(t, staleCustomer) != 2 || database.outboxCount(t) != 1 {
		t.Fatalf("rerun wrote: version=%d outbox=%d", database.version(t, staleCustomer), database.outboxCount(t))
	}
	if database.version(t, currentCustomer) != currentVersion {
		t.Fatalf("current customer version = %d, want %d", database.version(t, currentCustomer), currentVersion)
	}

	options.AllCustomers = true
	everyone, err := Run(database.ctx, database.store, options, func(report CustomerReport) error {
		t.Fatalf("all-customer rerun reported a change: %#v", report)
		return nil
	})
	if err != nil {
		t.Fatalf("all-customer Run() error = %v", err)
	}
	if everyone.CustomersScanned != 3 || everyone.CustomersChanged != 0 || database.outboxCount(t) != 1 {
		t.Fatalf("all-customer summary = %#v outbox=%d", everyone, database.outboxCount(t))
	}
}

// TestRunSkipsACustomerMergedAfterItWasListed verifies that a merged customer is never refreshed
// under its own id, and that the merged customer is not listed again.
func TestRunSkipsACustomerMergedAfterItWasListed(t *testing.T) {
	database := openReprojectionDatabase(t)
	now := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	database.seed(t, now)
	options := Options{
		ProjectID: reprojectionProject, BatchSize: 10, Apply: true,
		Projector: verification.NewDefaultProjector(), Clock: func() time.Time { return now },
	}
	database.exec(t, `UPDATE customers SET merged_into_customer_id = $1 WHERE id = $2`, importOnlyCustomer, currentCustomer)
	report, skipped, err := reprojectCustomer(database.ctx, database.store, options, reprojectionProject, currentCustomer)
	if err != nil || !skipped || report.ProjectionsChanged != 0 {
		t.Fatalf("reprojectCustomer(merged) = %#v, %t, %v; want skipped", report, skipped, err)
	}
	summary, err := Run(database.ctx, database.store, options, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if summary.CustomersScanned != 2 || summary.CustomersChanged != 2 {
		t.Fatalf("summary = %#v, want the stale customer and the new canonical customer", summary)
	}
}

// assertStaleReport checks the one customer report a run writes for customer-stale.
func assertStaleReport(t *testing.T, reports []CustomerReport, mode string, now time.Time) {
	t.Helper()
	if len(reports) != 1 {
		t.Fatalf("reports = %#v, want one", reports)
	}
	report := reports[0]
	if report.Mode != mode || report.ProjectID != reprojectionProject || report.CustomerID != staleCustomer ||
		!report.EvaluatedAt.Equal(now) || report.ProjectionsChanged != 1 || report.WebhooksEnqueued != 1 ||
		len(report.Changes) != 1 {
		t.Fatalf("report = %#v", report)
	}
	change := report.Changes[0]
	if change.EntitlementKey != "premium" || change.Before == nil {
		t.Fatalf("change = %#v", change)
	}
	before := *change.Before
	if before.SourceKind != persistence.SourceKindStoreObservation || before.SourceID != "observation-stale" ||
		before.SourceProductID != "product-store" || before.StoredAccess != core.AccessAllowed ||
		before.Access != core.AccessDenied || before.AccessReason != core.AccessReasonExpired ||
		before.EndsAt == nil || !before.EndsAt.Equal(time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)) ||
		before.Version != 1 {
		t.Fatalf("before = %#v", before)
	}
	after := change.After
	if after.SourceKind != persistence.SourceKindImport || after.SourceID != staleImportID ||
		after.SourceProductID != "product-import" || after.Access != core.AccessAllowed ||
		after.EndsAt == nil || !after.EndsAt.Equal(time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)) ||
		after.Version != 2 {
		t.Fatalf("after = %#v", after)
	}
	encoded, err := json.Marshal(report)
	if err != nil || !bytes.Contains(encoded, []byte(`"source_kind":"import"`)) {
		t.Fatalf("encoded report = %s, %v", encoded, err)
	}
}

// openReprojectionDatabase resets and opens one migrated PostgreSQL fixture.
func openReprojectionDatabase(t *testing.T) *reprojectionDatabase {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(reprojectionDatabaseURLKey))
	if databaseURL == "" {
		t.Skipf("%s is not configured", reprojectionDatabaseURLKey)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, reprojectionDatabaseAdvisoryLock); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("lock: %v", err)
	}
	migrator, err := postgres.NewMigrator(ctx, connection)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("NewMigrator() error = %v", err)
	}
	if err := postgres.RemoveRiver(ctx, databaseURL); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("RemoveRiver() error = %v", err)
	}
	if err := postgres.ClearIdentityMigrationGuards(ctx, connection); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("clear guards: %v", err)
	}
	if err := migrator.MigrateTo(ctx, 0); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset schema: %v", err)
	}
	if err := migrator.Up(ctx); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("migrate: %v", err)
	}
	if err := postgres.MigrateRiver(ctx, databaseURL); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("MigrateRiver() error = %v", err)
	}
	store, err := postgres.OpenStore(ctx, databaseURL)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() {
		store.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := postgres.RemoveRiver(cleanupCtx, databaseURL); err != nil {
			t.Errorf("RemoveRiver() during cleanup: %v", err)
		}
		if err := postgres.ClearIdentityMigrationGuards(cleanupCtx, connection); err != nil {
			t.Errorf("clear guards during cleanup: %v", err)
		}
		if err := migrator.MigrateTo(cleanupCtx, 0); err != nil {
			t.Errorf("reset schema during cleanup: %v", err)
		}
		if _, err := connection.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1)`, reprojectionDatabaseAdvisoryLock); err != nil {
			t.Errorf("unlock: %v", err)
		}
		_ = connection.Close(cleanupCtx)
	})
	return &reprojectionDatabase{ctx: ctx, conn: connection, store: store}
}

// seed writes the catalog and three customers. customer-stale holds the projection the old
// projector order selected: its lapsed store row ranked above a current import of another product.
func (database *reprojectionDatabase) seed(t *testing.T, now time.Time) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO projects (id) VALUES ('project-rp')`,
		`INSERT INTO applications (id, project_id, provider, environment, provider_application_id) VALUES ('application-rp', 'project-rp', 'apple_app_store', 'production', 'apple-rp')`,
		`INSERT INTO entitlements (id, project_id, key) VALUES ('entitlement-premium', 'project-rp', 'premium')`,
		`INSERT INTO products (id, project_id, kind) VALUES ('product-store', 'project-rp', 'subscription'), ('product-import', 'project-rp', 'subscription')`,
		`INSERT INTO product_entitlements (project_id, product_id, entitlement_id) VALUES ('project-rp', 'product-store', 'entitlement-premium'), ('project-rp', 'product-import', 'entitlement-premium')`,
		`INSERT INTO store_products (project_id, application_id, product_id, provider_product_id) VALUES ('project-rp', 'application-rp', 'product-store', 'com.example.store')`,
		`INSERT INTO customers (id, project_id, external_id) VALUES ('customer-stale', 'project-rp', 'user-stale'), ('customer-current', 'project-rp', 'user-current'), ('customer-import-only', 'project-rp', 'user-import-only')`,
	} {
		database.exec(t, statement)
	}
	day := func(month time.Month, value int) time.Time {
		return time.Date(2026, month, value, 0, 0, 0, 0, time.UTC)
	}

	database.observation(t, staleCustomer, "observation-stale", 1, day(time.August, 1), day(time.September, 1))
	database.importGrant(t, staleCustomer, staleImportID, 1, day(time.July, 1), day(time.December, 1))
	database.exec(t, `
		INSERT INTO customer_entitlements (
			project_id, customer_id, entitlement_id, source_kind, source_observation_id,
			source_product_id, source_customer_id, access_status, access_reason,
			effective_starts_at, effective_ends_at
		) VALUES (
			'project-rp', 'customer-stale', 'entitlement-premium', 'store_observation', 'observation-stale',
			'product-store', 'customer-stale', 'allowed', 'purchase_valid', $1, $2
		)
	`, day(time.August, 1), day(time.September, 1))

	database.observation(t, currentCustomer, "observation-current", 2, day(time.September, 1), day(time.December, 1))
	database.importGrant(t, currentCustomer, "imp-00000000000000000000000000000002", 2, day(time.July, 1), day(time.November, 1))
	database.importGrant(t, importOnlyCustomer, "imp-00000000000000000000000000000003", 3, day(time.July, 1), day(time.November, 1))
	for _, customerID := range []core.CustomerID{currentCustomer, importOnlyCustomer} {
		err := database.store.Transact(database.ctx, func(repository persistence.Transaction) error {
			if err := repository.LockCustomerClosure(database.ctx, reprojectionProject, customerID); err != nil {
				return err
			}
			_, err := verification.RefreshCustomerProjectionWithOptions(
				database.ctx, repository, verification.NewDefaultProjector(),
				reprojectionProject, customerID, now, false,
			)
			return err
		})
		if err != nil {
			t.Fatalf("seed projection for %s: %v", customerID, err)
		}
	}
	if source := database.sourceKind(t, currentCustomer); source != persistence.SourceKindStoreObservation {
		t.Fatalf("current customer source = %s, want the longer store observation", source)
	}
	if database.outboxCount(t) != 0 {
		t.Fatal("seed enqueued events")
	}
}

// observation inserts one allowed subscription observation of product-store and its evidence.
func (database *reprojectionDatabase) observation(
	t *testing.T,
	customerID string,
	observationID string,
	fingerprint byte,
	startsAt time.Time,
	endsAt time.Time,
) {
	t.Helper()
	var evidenceID int64
	err := database.conn.QueryRow(database.ctx, `
		INSERT INTO purchase_evidence (
			project_id, application_id, customer_id, kind, content_type,
			payload_ciphertext, payload_fingerprint, encryption_key_id, received_at
		) VALUES ('project-rp', 'application-rp', $1, 'purchase_submission', 'application/json', 'ciphertext', $2, 'test-key', $3)
		RETURNING id
	`, customerID, bytes.Repeat([]byte{fingerprint}, 32), startsAt).Scan(&evidenceID)
	if err != nil {
		t.Fatalf("insert evidence: %v", err)
	}
	database.exec(t, `
		INSERT INTO purchase_observations (
			id, evidence_id, project_id, application_id, customer_id, product_id,
			provider_product_id, product_kind, lifecycle_state, provider_state,
			access_status, access_reason, ownership, quantity, occurred_at, observed_at,
			effective_starts_at, effective_ends_at, renewal_mode, renewal_status
		) VALUES (
			$1, $2, 'project-rp', 'application-rp', $3, 'product-store',
			'com.example.store', 'subscription', 'active', 'active',
			'allowed', 'purchase_valid', 'purchased', 1, $4, $4,
			$4, $5, 'auto', 'unknown'
		)
	`, observationID, evidenceID, customerID, startsAt, endsAt)
}

// importGrant inserts one allowed import grant of product-import.
func (database *reprojectionDatabase) importGrant(
	t *testing.T,
	customerID string,
	grantID string,
	fingerprint byte,
	startsAt time.Time,
	endsAt time.Time,
) {
	t.Helper()
	database.exec(t, `
		INSERT INTO import_grants (
			id, project_id, customer_id, application_id, product_id, entitlement_id,
			origin, origin_store, environment, access_status, access_reason,
			effective_starts_at, effective_ends_at, observed_at, export_sha256, lineage_fingerprint
		) VALUES (
			$1, 'project-rp', $2, 'application-rp', 'product-import', 'entitlement-premium',
			'revenuecat', 'app_store', 'production', 'allowed', 'purchase_valid',
			$3, $4, $3, $5, $5
		)
	`, grantID, customerID, startsAt, endsAt, bytes.Repeat([]byte{fingerprint}, 32))
}

// exec runs one fixture statement.
func (database *reprojectionDatabase) exec(t *testing.T, statement string, arguments ...any) {
	t.Helper()
	if _, err := database.conn.Exec(database.ctx, statement, arguments...); err != nil {
		t.Fatalf("exec fixture: %v", err)
	}
}

// publicAccess reads one customer's premium access as the public read path evaluates it.
func (database *reprojectionDatabase) publicAccess(t *testing.T, customerID string, at time.Time) core.AccessStatus {
	t.Helper()
	var entitlements []persistence.CustomerEntitlement
	err := database.store.Transact(database.ctx, func(repository persistence.Transaction) error {
		var readErr error
		entitlements, readErr = repository.CustomerEntitlements(database.ctx, reprojectionProject, core.CustomerID(customerID))
		return readErr
	})
	if err != nil || len(entitlements) != 1 {
		t.Fatalf("read entitlements = %#v, %v", entitlements, err)
	}
	projection := entitlements[0].Projection
	access, _ := core.AccessAt(projection.Access, projection.AccessReason, projection.EffectivePeriod.EndsAt, at)
	return access
}

// sourceKind reads the stored source kind of one customer's premium projection.
func (database *reprojectionDatabase) sourceKind(t *testing.T, customerID string) persistence.SourceKind {
	t.Helper()
	var kind persistence.SourceKind
	if err := database.conn.QueryRow(database.ctx, `
		SELECT source_kind FROM customer_entitlements WHERE customer_id = $1
	`, customerID).Scan(&kind); err != nil {
		t.Fatalf("read source kind: %v", err)
	}
	return kind
}

// version reads the stored version of one customer's premium projection.
func (database *reprojectionDatabase) version(t *testing.T, customerID string) int64 {
	t.Helper()
	var version int64
	if err := database.conn.QueryRow(database.ctx, `
		SELECT version FROM customer_entitlements WHERE customer_id = $1
	`, customerID).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	return version
}

// outboxCount returns the number of stored entitlement events.
func (database *reprojectionDatabase) outboxCount(t *testing.T) int {
	t.Helper()
	var count int
	if err := database.conn.QueryRow(database.ctx, `SELECT count(*) FROM outbox_events`).Scan(&count); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return count
}

// outboxPayload decodes the only stored entitlement event.
func (database *reprojectionDatabase) outboxPayload(t *testing.T) map[string]any {
	t.Helper()
	var payload []byte
	if err := database.conn.QueryRow(database.ctx, `SELECT payload FROM outbox_events`).Scan(&payload); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode outbox: %v", err)
	}
	return decoded
}
