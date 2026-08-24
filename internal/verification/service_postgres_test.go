package verification_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/jackc/pgx/v5"
)

const (
	// verificationDatabaseURLKey names the opt-in PostgreSQL integration-test setting.
	verificationDatabaseURLKey = "IAPSTACK_TEST_DATABASE_URL"
	// verificationDatabaseTimeout bounds one orchestration integration-test database session.
	verificationDatabaseTimeout = 30 * time.Second
	// verificationDatabaseAdvisoryLock serializes schema-resetting integration test packages.
	verificationDatabaseAdvisoryLock int64 = 424090117
)

// verificationTestDatabase owns one migrated connection and one repository pool.
type verificationTestDatabase struct {
	ctx      context.Context
	conn     *pgx.Conn
	migrator *postgres.Migrator
	store    *postgres.Store
}

// failingOutboxStore decorates a durable store with an injected outbox failure.
type failingOutboxStore struct {
	store persistence.Store
}

// failingOutboxTransaction delegates every repository except outbox writes.
type failingOutboxTransaction struct {
	persistence.Transaction
}

// TestServicePersistsAndReplaysWithPostgreSQL verifies the complete use case against real repositories.
func TestServicePersistsAndReplaysWithPostgreSQL(t *testing.T) {
	database := openVerificationTestDatabase(t)
	fixture := newVerificationFixture(t)
	seedVerificationCatalog(t, database, fixture)
	service := newVerificationService(
		t,
		database.store,
		&fakeAdapter{result: fixture.result},
		&fakeProtector{},
		fixture.clock,
	)

	first, err := service.Verify(database.ctx, fixture.command)
	if err != nil {
		t.Fatalf("first Verify() error = %v", err)
	}
	second, err := service.Verify(database.ctx, fixture.command)
	if err != nil {
		t.Fatalf("second Verify() error = %v", err)
	}
	if len(first.Entitlements) != 1 || len(second.Entitlements) != 1 {
		t.Fatalf("entitlement snapshots = (%#v, %#v), want one entitlement", first, second)
	}
	if first.Entitlements[0].Version != 1 || second.Entitlements[0].Version != 1 {
		t.Fatalf(
			"entitlement versions = (%d, %d), want replayed version 1",
			first.Entitlements[0].Version,
			second.Entitlements[0].Version,
		)
	}
	assertVerificationTableCount(t, database, "purchase_evidence", 1)
	assertVerificationTableCount(t, database, "verified_artifacts", 1)
	assertVerificationTableCount(t, database, "purchase_observations", 1)
	assertVerificationTableCount(t, database, "provider_references", 1)
	assertVerificationTableCount(t, database, "customer_entitlements", 1)
	assertVerificationTableCount(t, database, "outbox_events", 1)
}

// TestServiceRollsBackPostgreSQLWhenOutboxFails verifies use-case atomicity through the real store.
func TestServiceRollsBackPostgreSQLWhenOutboxFails(t *testing.T) {
	database := openVerificationTestDatabase(t)
	fixture := newVerificationFixture(t)
	seedVerificationCatalog(t, database, fixture)
	service := newVerificationService(
		t,
		&failingOutboxStore{store: database.store},
		&fakeAdapter{result: fixture.result},
		&fakeProtector{},
		fixture.clock,
	)

	_, err := service.Verify(database.ctx, fixture.command)
	if !errors.Is(err, errFakeOutbox) {
		t.Fatalf("Verify() error = %v, want injected outbox failure", err)
	}
	assertVerificationTableCount(t, database, "purchase_evidence", 0)
	assertVerificationTableCount(t, database, "verified_artifacts", 0)
	assertVerificationTableCount(t, database, "purchase_observations", 0)
	assertVerificationTableCount(t, database, "provider_references", 0)
	assertVerificationTableCount(t, database, "customer_entitlements", 0)
	assertVerificationTableCount(t, database, "outbox_events", 0)
}

// Ping delegates availability checks to the wrapped durable store.
func (store *failingOutboxStore) Ping(ctx context.Context) error {
	return store.store.Ping(ctx)
}

// Transact injects an outbox-failing repository into one real durable transaction.
func (store *failingOutboxStore) Transact(ctx context.Context, operation persistence.TransactionFunc) error {
	return store.store.Transact(ctx, func(repository persistence.Transaction) error {
		return operation(&failingOutboxTransaction{Transaction: repository})
	})
}

// Close delegates resource release to the wrapped durable store.
func (store *failingOutboxStore) Close() {
	store.store.Close()
}

// SaveOutboxEvent returns the deterministic injected failure after earlier writes.
func (transaction *failingOutboxTransaction) SaveOutboxEvent(
	context.Context,
	persistence.OutboxEvent,
) (string, error) {
	return "", errFakeOutbox
}

// openVerificationTestDatabase resets, migrates, and opens the configured PostgreSQL test database.
func openVerificationTestDatabase(t *testing.T) *verificationTestDatabase {
	t.Helper()

	databaseURL := strings.TrimSpace(os.Getenv(verificationDatabaseURLKey))
	if databaseURL == "" {
		t.Skipf("%s is not configured", verificationDatabaseURLKey)
	}
	ctx, cancel := context.WithTimeout(context.Background(), verificationDatabaseTimeout)
	t.Cleanup(cancel)

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to verification PostgreSQL: %v", err)
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, verificationDatabaseAdvisoryLock); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("lock verification PostgreSQL: %v", err)
	}
	migrator, err := postgres.NewMigrator(ctx, connection)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("NewMigrator() error = %v", err)
	}
	if err := migrator.MigrateTo(ctx, 0); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset verification PostgreSQL: %v", err)
	}
	if err := migrator.Up(ctx); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("migrate verification PostgreSQL: %v", err)
	}
	database := &verificationTestDatabase{ctx: ctx, conn: connection, migrator: migrator}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), verificationDatabaseTimeout)
		defer cleanupCancel()
		if err := migrator.MigrateTo(cleanupCtx, 0); err != nil {
			t.Errorf("reset verification PostgreSQL during cleanup: %v", err)
		}
		if _, err := connection.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1)`, verificationDatabaseAdvisoryLock); err != nil {
			t.Errorf("unlock verification PostgreSQL: %v", err)
		}
		if err := connection.Close(cleanupCtx); err != nil {
			t.Errorf("close verification PostgreSQL: %v", err)
		}
	})

	database.store, err = postgres.OpenStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(database.store.Close)
	return database
}

// seedVerificationCatalog inserts the project-scoped catalog required by one fixture.
func seedVerificationCatalog(
	t *testing.T,
	database *verificationTestDatabase,
	fixture verificationFixture,
) {
	t.Helper()

	mustVerificationExec(t, database, `INSERT INTO projects (id) VALUES ($1)`, fixture.application.ProjectID)
	mustVerificationExec(t, database, `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ($1, $2, $3, $4, $5)
	`,
		fixture.application.ID,
		fixture.application.ProjectID,
		fixture.application.Store.Provider,
		fixture.application.Store.Environment,
		fixture.application.Store.ID,
	)
	mustVerificationExec(t, database, `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ($1, $2, $3)
	`, fixture.customer.ID, fixture.customer.ProjectID, fixture.customer.ExternalID)
	mustVerificationExec(t, database, `
		INSERT INTO products (id, project_id, kind)
		VALUES ($1, $2, $3)
	`, fixture.catalog.Product.ID, fixture.catalog.Product.ProjectID, fixture.catalog.Product.Kind)
	for _, entitlement := range fixture.catalog.Entitlements {
		mustVerificationExec(t, database, `
			INSERT INTO entitlements (id, project_id, key)
			VALUES ($1, $2, $3)
		`, entitlement.ID, entitlement.ProjectID, entitlement.Key)
		mustVerificationExec(t, database, `
			INSERT INTO product_entitlements (project_id, product_id, entitlement_id)
			VALUES ($1, $2, $3)
		`, entitlement.ProjectID, fixture.catalog.Product.ID, entitlement.ID)
	}
	mustVerificationExec(t, database, `
		INSERT INTO store_products (project_id, application_id, product_id, provider_product_id)
		VALUES ($1, $2, $3, $4)
	`,
		fixture.catalog.Product.ProjectID,
		fixture.catalog.Mapping.ApplicationID,
		fixture.catalog.Mapping.ProductID,
		fixture.catalog.Mapping.ProviderID,
	)
}

// mustVerificationExec executes one PostgreSQL fixture statement or fails the test.
func mustVerificationExec(
	t *testing.T,
	database *verificationTestDatabase,
	query string,
	arguments ...any,
) {
	t.Helper()
	if _, err := database.conn.Exec(database.ctx, query, arguments...); err != nil {
		t.Fatalf("execute verification PostgreSQL fixture: %v", err)
	}
}

// assertVerificationTableCount verifies committed rows in one fixed orchestration test table.
func assertVerificationTableCount(
	t *testing.T,
	database *verificationTestDatabase,
	table string,
	expected int,
) {
	t.Helper()

	allowedTables := map[string]struct{}{
		"customer_entitlements": {},
		"outbox_events":         {},
		"provider_references":   {},
		"purchase_evidence":     {},
		"purchase_observations": {},
		"verified_artifacts":    {},
	}
	if _, allowed := allowedTables[table]; !allowed {
		t.Fatalf("table %q is not allowed by verification test helper", table)
	}
	var actual int
	if err := database.conn.QueryRow(database.ctx, "SELECT count(*) FROM "+table).Scan(&actual); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if actual != expected {
		t.Fatalf("%s count = %d, want %d", table, actual, expected)
	}
}
