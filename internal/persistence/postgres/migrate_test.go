package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// testDatabaseURLKey names the opt-in PostgreSQL integration-test connection setting.
	testDatabaseURLKey = "IAPSTACK_TEST_DATABASE_URL"
	// testDatabaseTimeout bounds one integration-test database session.
	testDatabaseTimeout = 30 * time.Second
	// testDatabaseAdvisoryLock serializes schema-resetting integration test packages.
	testDatabaseAdvisoryLock int64 = 424090117
)

// testDatabase owns one connection and its embedded migration runner.
type testDatabase struct {
	ctx      context.Context
	conn     *pgx.Conn
	migrator *postgres.Migrator
}

// catalogFixture identifies one valid provider-neutral catalog graph.
type catalogFixture struct {
	projectID           string
	applicationID       string
	customerID          string
	productID           string
	entitlementID       string
	providerProductID   string
	providerApplication string
}

// purchaseFixture identifies persisted evidence, observation, and catalog records.
type purchaseFixture struct {
	catalog       catalogFixture
	evidenceID    int64
	observationID string
}

// TestOpenMigratorValidatesURL verifies fail-fast database configuration errors.
func TestOpenMigratorValidatesURL(t *testing.T) {
	t.Parallel()

	if _, err := postgres.OpenMigrator(context.Background(), ""); !errors.Is(err, postgres.ErrDatabaseURLRequired) {
		t.Fatalf("OpenMigrator() error = %v, want ErrDatabaseURLRequired", err)
	}
	if _, err := postgres.OpenMigrator(context.Background(), "postgres://%zz"); !errors.Is(err, postgres.ErrInvalidDatabaseURL) {
		t.Fatalf("OpenMigrator() error = %v, want ErrInvalidDatabaseURL", err)
	}
}

// TestMigrationLifecycle verifies every version can be applied, rolled back, and reapplied.
func TestMigrationLifecycle(t *testing.T) {
	database := openTestDatabase(t, 0)
	relations := []string{
		"projects",
		"purchase_observations",
		"customer_entitlements",
		"outbox_events",
	}

	for version := int32(1); version <= postgres.LatestVersion; version++ {
		mustMigrateTo(t, database, version)
		assertVersion(t, database, version)
		assertRelationExists(t, database, relations[version-1], true)
	}

	for version := postgres.LatestVersion; version > 0; version-- {
		mustMigrateTo(t, database, version-1)
		assertVersion(t, database, version-1)
		assertRelationExists(t, database, relations[version-1], false)
	}

	if err := database.migrator.Up(database.ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	assertVersion(t, database, postgres.LatestVersion)
}

// TestCatalogMigrationConstraints verifies catalog uniqueness and project isolation.
func TestCatalogMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, 1)
	fixture := seedCatalog(t, database)

	expectConstraint(t, database, "customers_external_id_unique", `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ('customer-duplicate', $1, 'customer-external')
	`, fixture.projectID)

	mustExec(t, database, `INSERT INTO projects (id) VALUES ('project-2')`)
	mustExec(t, database, `
		INSERT INTO products (id, project_id, kind)
		VALUES ('product-2', 'project-2', 'non_consumable')
	`)
	expectConstraint(t, database, "store_products_application_fk", `
		INSERT INTO store_products (project_id, application_id, product_id, provider_product_id)
		VALUES ('project-2', $1, 'product-2', 'cross-project-product')
	`, fixture.applicationID)

	expectConstraint(t, database, "applications_store_scope_unique", `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ('application-duplicate', 'project-2', 'huawei_appgallery', 'sandbox', $1)
	`, fixture.providerApplication)

	mustExec(t, database, `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ('application-future', 'project-2', 'future_store', 'test', 'future-app')
	`)
}

// TestPurchaseEvidenceMigrationConstraints verifies immutable purchase idempotency and reference scope.
func TestPurchaseEvidenceMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, 2)
	purchase := seedPurchase(t, database)
	fingerprint := bytes.Repeat([]byte{1}, 32)

	expectConstraint(t, database, "purchase_evidence_payload_unique", `
		INSERT INTO purchase_evidence (
			project_id, application_id, customer_id, kind, content_type,
			payload_ciphertext, payload_fingerprint, encryption_key_id, received_at
		) VALUES ($1, $2, $3, 'duplicate', 'application/json', 'ciphertext', $4, 'test-key', now())
	`, purchase.catalog.projectID, purchase.catalog.applicationID, purchase.catalog.customerID, fingerprint)

	expectConstraint(t, database, "purchase_observations_pkey", observationInsertSQL(),
		purchase.observationID,
		purchase.evidenceID,
		purchase.catalog.projectID,
		purchase.catalog.applicationID,
		purchase.catalog.customerID,
		purchase.catalog.productID,
		purchase.catalog.providerProductID,
	)

	expectConstraint(t, database, "provider_references_value_unique", `
		INSERT INTO provider_references (
			application_id, role, kind, value_ciphertext, value_fingerprint, encryption_key_id
		) VALUES ($1, 'transaction', 'order_id', 'ciphertext', $2, 'test-key')
	`, purchase.catalog.applicationID, bytes.Repeat([]byte{3}, 32))

	mustExec(t, database, `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ('application-2', $1, 'huawei_appgallery', 'test', 'huawei-app-2')
	`, purchase.catalog.projectID)

	var foreignReferenceID int64
	err := database.conn.QueryRow(database.ctx, `
		INSERT INTO provider_references (
			application_id, role, kind, value_ciphertext, value_fingerprint, encryption_key_id
		) VALUES ('application-2', 'transaction', 'order_id', 'ciphertext', $1, 'test-key')
		RETURNING id
	`, bytes.Repeat([]byte{4}, 32)).Scan(&foreignReferenceID)
	if err != nil {
		t.Fatalf("insert foreign provider reference: %v", err)
	}

	expectConstraint(t, database, "observation_references_reference_fk", `
		INSERT INTO observation_references (observation_id, application_id, reference_id)
		VALUES ($1, $2, $3)
	`, purchase.observationID, purchase.catalog.applicationID, foreignReferenceID)
}

// TestEntitlementProjectionMigrationConstraints verifies source and catalog integrity for current access.
func TestEntitlementProjectionMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, 3)
	purchase := seedPurchase(t, database)

	mustExec(t, database, entitlementInsertSQL(),
		purchase.catalog.projectID,
		purchase.catalog.customerID,
		purchase.catalog.entitlementID,
		purchase.observationID,
		purchase.catalog.productID,
	)

	expectConstraint(t, database, "customer_entitlements_pkey", entitlementInsertSQL(),
		purchase.catalog.projectID,
		purchase.catalog.customerID,
		purchase.catalog.entitlementID,
		purchase.observationID,
		purchase.catalog.productID,
	)

	mustExec(t, database, `
		INSERT INTO entitlements (id, project_id, key)
		VALUES ('entitlement-unmapped', $1, 'unmapped')
	`, purchase.catalog.projectID)
	expectConstraint(t, database, "customer_entitlements_mapping_fk", entitlementInsertSQL(),
		purchase.catalog.projectID,
		purchase.catalog.customerID,
		"entitlement-unmapped",
		purchase.observationID,
		purchase.catalog.productID,
	)

	mustExec(t, database, `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ('customer-2', $1, 'customer-external-2')
	`, purchase.catalog.projectID)
	expectConstraint(t, database, "customer_entitlements_source_fk", entitlementInsertSQL(),
		purchase.catalog.projectID,
		"customer-2",
		purchase.catalog.entitlementID,
		purchase.observationID,
		purchase.catalog.productID,
	)
}

// TestInboxOutboxMigrationConstraints verifies queue deduplication and state consistency.
func TestInboxOutboxMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, 4)
	fixture := seedCatalog(t, database)
	inboxFingerprint := bytes.Repeat([]byte{5}, 32)
	outboxFingerprint := bytes.Repeat([]byte{6}, 32)

	mustExec(t, database, inboxInsertSQL(),
		"inbox-1",
		fixture.projectID,
		fixture.applicationID,
		inboxFingerprint,
	)
	expectConstraint(t, database, "inbox_messages_payload_unique", inboxInsertSQL(),
		"inbox-duplicate",
		fixture.projectID,
		fixture.applicationID,
		inboxFingerprint,
	)
	expectConstraint(t, database, "inbox_messages_state_fields_valid", `
		INSERT INTO inbox_messages (
			id, project_id, application_id, provider, kind, content_type,
			payload_ciphertext, payload_fingerprint, encryption_key_id,
			state, received_at, available_at
		) VALUES (
			'inbox-invalid', $1, $2, 'huawei_appgallery', 'subscription_event', 'application/json',
			'ciphertext', $3, 'test-key', 'processing', now(), now()
		)
	`, fixture.projectID, fixture.applicationID, bytes.Repeat([]byte{7}, 32))

	mustExec(t, database, outboxInsertSQL(),
		"outbox-1",
		fixture.projectID,
		fixture.applicationID,
		outboxFingerprint,
	)
	expectConstraint(t, database, "outbox_events_payload_unique", outboxInsertSQL(),
		"outbox-duplicate",
		fixture.projectID,
		fixture.applicationID,
		outboxFingerprint,
	)
	expectConstraint(t, database, "outbox_events_state_fields_valid", `
		INSERT INTO outbox_events (
			id, project_id, application_id, event_type, aggregate_type, aggregate_id,
			payload, payload_fingerprint, state, occurred_at, available_at
		) VALUES (
			'outbox-invalid', $1, $2, 'entitlement.changed', 'customer', 'customer-1',
			'{"entitlement":"pro"}'::jsonb, $3, 'delivered', now(), now()
		)
	`, fixture.projectID, fixture.applicationID, bytes.Repeat([]byte{8}, 32))
}

// openTestDatabase opens the configured integration database at one schema version.
func openTestDatabase(t *testing.T, targetVersion int32) *testDatabase {
	t.Helper()

	databaseURL := strings.TrimSpace(os.Getenv(testDatabaseURLKey))
	if databaseURL == "" {
		t.Skipf("%s is not configured", testDatabaseURLKey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testDatabaseTimeout)
	t.Cleanup(cancel)

	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to integration PostgreSQL: %v", err)
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, testDatabaseAdvisoryLock); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("lock integration PostgreSQL: %v", err)
	}

	migrator, err := postgres.NewMigrator(ctx, connection)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("NewMigrator() error = %v", err)
	}

	database := &testDatabase{ctx: ctx, conn: connection, migrator: migrator}
	if err := migrator.MigrateTo(ctx, 0); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset integration PostgreSQL: %v", err)
	}
	if err := migrator.MigrateTo(ctx, targetVersion); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("migrate integration PostgreSQL to %d: %v", targetVersion, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), testDatabaseTimeout)
		defer cleanupCancel()
		if err := migrator.MigrateTo(cleanupCtx, 0); err != nil {
			t.Errorf("reset integration PostgreSQL during cleanup: %v", err)
		}
		if _, err := connection.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1)`, testDatabaseAdvisoryLock); err != nil {
			t.Errorf("unlock integration PostgreSQL: %v", err)
		}
		if err := connection.Close(cleanupCtx); err != nil {
			t.Errorf("close integration PostgreSQL: %v", err)
		}
	})

	return database
}

// seedCatalog inserts one coherent project, application, customer, product, and entitlement graph.
func seedCatalog(t *testing.T, database *testDatabase) catalogFixture {
	t.Helper()

	fixture := catalogFixture{
		projectID:           "project-1",
		applicationID:       "application-1",
		customerID:          "customer-1",
		productID:           "product-1",
		entitlementID:       "entitlement-1",
		providerProductID:   "provider-product-1",
		providerApplication: "huawei-app-1",
	}

	mustExec(t, database, `INSERT INTO projects (id) VALUES ($1)`, fixture.projectID)
	mustExec(t, database, `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ($1, $2, 'huawei_appgallery', 'sandbox', $3)
	`, fixture.applicationID, fixture.projectID, fixture.providerApplication)
	mustExec(t, database, `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ($1, $2, 'customer-external')
	`, fixture.customerID, fixture.projectID)
	mustExec(t, database, `
		INSERT INTO products (id, project_id, kind)
		VALUES ($1, $2, 'non_consumable')
	`, fixture.productID, fixture.projectID)
	mustExec(t, database, `
		INSERT INTO entitlements (id, project_id, key)
		VALUES ($1, $2, 'pro')
	`, fixture.entitlementID, fixture.projectID)
	mustExec(t, database, `
		INSERT INTO product_entitlements (project_id, product_id, entitlement_id)
		VALUES ($1, $2, $3)
	`, fixture.projectID, fixture.productID, fixture.entitlementID)
	mustExec(t, database, `
		INSERT INTO store_products (project_id, application_id, product_id, provider_product_id)
		VALUES ($1, $2, $3, $4)
	`, fixture.projectID, fixture.applicationID, fixture.productID, fixture.providerProductID)

	return fixture
}

// seedPurchase inserts encrypted evidence, one normalized observation, and its transaction reference.
func seedPurchase(t *testing.T, database *testDatabase) purchaseFixture {
	t.Helper()

	fixture := seedCatalog(t, database)
	fingerprint := bytes.Repeat([]byte{1}, 32)

	var evidenceID int64
	err := database.conn.QueryRow(database.ctx, `
		INSERT INTO purchase_evidence (
			project_id, application_id, customer_id, kind, content_type,
			payload_ciphertext, payload_fingerprint, encryption_key_id, received_at
		) VALUES ($1, $2, $3, 'purchase_submission', 'application/json', 'ciphertext', $4, 'test-key', now())
		RETURNING id
	`, fixture.projectID, fixture.applicationID, fixture.customerID, fingerprint).Scan(&evidenceID)
	if err != nil {
		t.Fatalf("insert purchase evidence: %v", err)
	}

	mustExec(t, database, `
		INSERT INTO verified_artifacts (
			evidence_id, kind, content_type, payload_ciphertext, payload_fingerprint, encryption_key_id
		) VALUES ($1, 'provider_response', 'application/json', 'ciphertext', $2, 'test-key')
	`, evidenceID, bytes.Repeat([]byte{2}, 32))

	observationID := "observation-1"
	mustExec(t, database, observationInsertSQL(),
		observationID,
		evidenceID,
		fixture.projectID,
		fixture.applicationID,
		fixture.customerID,
		fixture.productID,
		fixture.providerProductID,
	)

	var referenceID int64
	err = database.conn.QueryRow(database.ctx, `
		INSERT INTO provider_references (
			application_id, role, kind, value_ciphertext, value_fingerprint, encryption_key_id
		) VALUES ($1, 'transaction', 'order_id', 'ciphertext', $2, 'test-key')
		RETURNING id
	`, fixture.applicationID, bytes.Repeat([]byte{3}, 32)).Scan(&referenceID)
	if err != nil {
		t.Fatalf("insert provider reference: %v", err)
	}
	mustExec(t, database, `
		INSERT INTO observation_references (observation_id, application_id, reference_id)
		VALUES ($1, $2, $3)
	`, observationID, fixture.applicationID, referenceID)

	return purchaseFixture{catalog: fixture, evidenceID: evidenceID, observationID: observationID}
}

// observationInsertSQL returns the canonical valid non-consumable observation insert used by tests.
func observationInsertSQL() string {
	return `
		INSERT INTO purchase_observations (
			id, evidence_id, project_id, application_id, customer_id, product_id,
			provider_product_id, product_kind, lifecycle_state, provider_state,
			access_status, access_reason, ownership, quantity, occurred_at, observed_at,
			effective_starts_at, renewal_mode, renewal_status
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, 'non_consumable', 'active', 'purchased',
			'allowed', 'purchase_valid', 'purchased', 1, now() - interval '1 hour', now(),
			now() - interval '1 hour', 'none', 'not_applicable'
		)
	`
}

// entitlementInsertSQL returns the canonical current-entitlement projection insert used by tests.
func entitlementInsertSQL() string {
	return `
		INSERT INTO customer_entitlements (
			project_id, customer_id, entitlement_id, source_observation_id,
			source_product_id, access_status, access_reason, effective_starts_at
		) VALUES ($1, $2, $3, $4, $5, 'allowed', 'purchase_valid', now() - interval '1 hour')
	`
}

// inboxInsertSQL returns the canonical pending inbox insert used by tests.
func inboxInsertSQL() string {
	return `
		INSERT INTO inbox_messages (
			id, project_id, application_id, provider, kind, content_type,
			payload_ciphertext, payload_fingerprint, encryption_key_id, received_at, available_at
		) VALUES (
			$1, $2, $3, 'huawei_appgallery', 'subscription_event', 'application/json',
			'ciphertext', $4, 'test-key', now(), now()
		)
	`
}

// outboxInsertSQL returns the canonical pending webhook outbox insert used by tests.
func outboxInsertSQL() string {
	return `
		INSERT INTO outbox_events (
			id, project_id, application_id, event_type, aggregate_type, aggregate_id,
			payload, payload_fingerprint, occurred_at, available_at
		) VALUES (
			$1, $2, $3, 'entitlement.changed', 'customer', 'customer-1',
			'{"entitlement":"pro"}'::jsonb, $4, now(), now()
		)
	`
}

// mustMigrateTo moves the integration database to one version or fails the test.
func mustMigrateTo(t *testing.T, database *testDatabase, version int32) {
	t.Helper()

	if err := database.migrator.MigrateTo(database.ctx, version); err != nil {
		t.Fatalf("MigrateTo(%d) error = %v", version, err)
	}
}

// mustExec executes a PostgreSQL statement or fails the test.
func mustExec(t *testing.T, database *testDatabase, query string, arguments ...any) {
	t.Helper()

	if _, err := database.conn.Exec(database.ctx, query, arguments...); err != nil {
		t.Fatalf("execute PostgreSQL fixture statement: %v", err)
	}
}

// expectConstraint verifies that PostgreSQL rejects a statement with the named constraint.
func expectConstraint(
	t *testing.T,
	database *testDatabase,
	constraintName string,
	query string,
	arguments ...any,
) {
	t.Helper()

	_, err := database.conn.Exec(database.ctx, query, arguments...)
	if err == nil {
		t.Fatalf("statement succeeded, want constraint %s", constraintName)
	}

	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		t.Fatalf("statement error = %v, want PostgreSQL constraint %s", err, constraintName)
	}
	if postgresError.ConstraintName != constraintName {
		t.Fatalf("constraint = %q, want %q: %v", postgresError.ConstraintName, constraintName, err)
	}
}

// assertVersion verifies the migration runner's current version.
func assertVersion(t *testing.T, database *testDatabase, expected int32) {
	t.Helper()

	actual, err := database.migrator.Version(database.ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if actual != expected {
		t.Fatalf("Version() = %d, want %d", actual, expected)
	}
}

// assertRelationExists verifies whether a public PostgreSQL relation is present.
func assertRelationExists(t *testing.T, database *testDatabase, relation string, expected bool) {
	t.Helper()

	var qualifiedName *string
	if err := database.conn.QueryRow(database.ctx, `SELECT to_regclass('public.' || $1)::text`, relation).Scan(&qualifiedName); err != nil {
		t.Fatalf("inspect relation %s: %v", relation, err)
	}
	if actual := qualifiedName != nil; actual != expected {
		t.Fatalf("relation %s exists = %t, want %t", relation, actual, expected)
	}
}
