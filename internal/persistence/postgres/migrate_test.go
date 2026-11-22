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

// TestMigrationLifecycle verifies the baseline schema can be applied, removed, and reapplied.
func TestMigrationLifecycle(t *testing.T) {
	database := openTestDatabase(t, 0)
	relations := []string{
		"projects",
		"purchase_observations",
		"purchase_observations_subscription_metrics_idx",
		"customer_entitlements",
		"outbox_events",
		"application_credentials",
		"api_keys",
		"admin_users",
		"customer_aliases",
		"financial_report_imports",
		"import_grants",
		"reconciliation_schedule_retries",
		"manual_grants",
		"apple_consumption_requests",
		"api_keys_project_idx",
	}
	assertRelations := func(exists bool) {
		t.Helper()
		for _, relation := range relations {
			assertRelationExists(t, database, relation, exists)
		}
	}

	assertVersion(t, database, 0)
	assertRelations(false)
	mustMigrateTo(t, database, postgres.LatestVersion)
	assertVersion(t, database, postgres.LatestVersion)
	assertRelations(true)
	mustMigrateTo(t, database, 0)
	assertVersion(t, database, 0)
	assertRelations(false)

	if err := database.migrator.Up(database.ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	assertVersion(t, database, postgres.LatestVersion)
	assertRelations(true)
}

// TestQueryIndexMigrationReplacesRedundantIndexes verifies the audited latest index set.
func TestQueryIndexMigrationReplacesRedundantIndexes(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	for _, index := range []string{
		"purchase_observations_evidence_scope_idx",
		"purchase_observations_internal_product_idx",
		"customer_entitlements_mapping_idx",
		"reconciliation_jobs_customer_idx",
	} {
		assertRelationExists(t, database, index, true)
	}
	for _, index := range []string{
		"provider_references_lookup_idx",
		"customers_project_idx",
		"entitlements_project_idx",
	} {
		assertRelationExists(t, database, index, false)
	}
}

// TestApplicationCredentialMigrationConstraints verifies protected values and project isolation.
func TestApplicationCredentialMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	fingerprint := bytes.Repeat([]byte{9}, 32)

	mustExec(t, database, credentialInsertSQL(),
		fixture.projectID,
		fixture.applicationID,
		"server_api",
		fingerprint,
	)
	expectConstraint(t, database, "application_credentials_pkey", credentialInsertSQL(),
		fixture.projectID,
		fixture.applicationID,
		"server_api",
		bytes.Repeat([]byte{10}, 32),
	)
	expectConstraint(t, database, "application_credentials_fingerprint_valid", credentialInsertSQL(),
		fixture.projectID,
		fixture.applicationID,
		"invalid_fingerprint",
		[]byte("short"),
	)

	mustExec(t, database, `INSERT INTO projects (id) VALUES ('project-2')`)
	expectConstraint(t, database, "application_credentials_application_fk", credentialInsertSQL(),
		"project-2",
		fixture.applicationID,
		"cross_project",
		bytes.Repeat([]byte{11}, 32),
	)
}

// TestCatalogMigrationConstraints verifies catalog uniqueness and project isolation.
func TestCatalogMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
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
	database := openTestDatabase(t, postgres.LatestVersion)
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

// TestPurchasePriceMigrationConstraints verifies signed transaction prices remain paired and canonical.
func TestPurchasePriceMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	purchase := seedPurchase(t, database)

	mustExec(t, database, `
		UPDATE purchase_observations
		SET price_milliunits = 4990, price_currency = 'USD'
		WHERE id = $1
	`, purchase.observationID)
	expectConstraint(t, database, "purchase_observations_price_pair_valid", `
		UPDATE purchase_observations
		SET price_milliunits = 4990, price_currency = NULL
		WHERE id = $1
	`, purchase.observationID)
	expectConstraint(t, database, "purchase_observations_price_amount_valid", `
		UPDATE purchase_observations
		SET price_milliunits = -1, price_currency = 'USD'
		WHERE id = $1
	`, purchase.observationID)
	expectConstraint(t, database, "purchase_observations_price_currency_valid", `
		UPDATE purchase_observations
		SET price_milliunits = 4990, price_currency = 'usd'
		WHERE id = $1
	`, purchase.observationID)
}

// TestSubscriptionOfferPhaseMigrationConstraints verifies the offer phase vocabulary and subscription scope.
func TestSubscriptionOfferPhaseMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	purchase := seedPurchase(t, database)

	expectConstraint(t, database, "purchase_observations_offer_phase_kind_valid", `
		UPDATE purchase_observations SET offer_phase = 'free_trial' WHERE id = $1
	`, purchase.observationID)

	catalog := purchase.catalog
	mustExec(t, database, `INSERT INTO products (id, project_id, kind) VALUES ('product-sub', $1, 'subscription')`, catalog.projectID)
	mustExec(t, database, `
		INSERT INTO store_products (project_id, application_id, product_id, provider_product_id)
		VALUES ($1, $2, 'product-sub', 'provider-sub')
	`, catalog.projectID, catalog.applicationID)
	insertSubscription := `
		INSERT INTO purchase_observations (
			id, evidence_id, project_id, application_id, customer_id, product_id,
			provider_product_id, product_kind, lifecycle_state, provider_state,
			access_status, access_reason, ownership, quantity, occurred_at, observed_at,
			effective_starts_at, effective_ends_at, renewal_mode, renewal_status, offer_phase
		) VALUES (
			$1, $2, $3, $4, $5, 'product-sub',
			'provider-sub', 'subscription', 'active', 'active',
			'allowed', 'purchase_valid', 'purchased', 1, now() - interval '1 hour', now(),
			now() - interval '1 hour', now() + interval '1 day', 'auto', 'enabled', $6
		)
	`
	mustExec(t, database, insertSubscription, "observation-trial", purchase.evidenceID, catalog.projectID,
		catalog.applicationID, catalog.customerID, "free_trial")
	expectConstraint(t, database, "purchase_observations_offer_phase_valid", insertSubscription,
		"observation-intro", purchase.evidenceID, catalog.projectID, catalog.applicationID, catalog.customerID, "intro")
	mustExec(t, database, `
		UPDATE purchase_observations
		SET transaction_reason = 'renewal', lineage_started_at = now() - interval '1 year',
			cancellation_reason = 'replacement'
		WHERE id = 'observation-trial'
	`)
	expectConstraint(t, database, "purchase_observations_transaction_reason_valid", `
		UPDATE purchase_observations SET transaction_reason = 'upgrade' WHERE id = 'observation-trial'
	`)
	expectConstraint(t, database, "purchase_observations_cancellation_reason_valid", `
		UPDATE purchase_observations SET cancellation_reason = 'refund' WHERE id = 'observation-trial'
	`)
	expectConstraint(t, database, "purchase_observations_offer_phase_kind_valid", `
		UPDATE purchase_observations SET lineage_started_at = now() WHERE id = $1
	`, purchase.observationID)
}

// TestFinancialReportMigrationConstraints verifies accepted amounts, one current period, and no raw payload.
func TestFinancialReportMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	mustExec(t, database, `INSERT INTO projects (id) VALUES ('project-constraint')`)
	mustExec(t, database, `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ('app-constraint', 'project-constraint', 'apple_app_store', 'production', '1000000001')
	`)
	mustExec(t, database, `
		INSERT INTO financial_report_imports (
			id, project_id, provider, report_kind, period_start, period_end, source,
			received_sha256, byte_size, content_type, parser_version, status, reason
		) VALUES (
			'import-accepted', 'project-constraint', 'apple_app_store', 'apple_financial_consolidated',
			DATE '2026-09-28', DATE '2026-11-01', 'upload',
			decode(repeat('ab', 32), 'hex'), 12, 'text/plain', 'apple-financial-consolidated-1', 'accepted', ''
		)
	`)
	mustExec(t, database, `
		INSERT INTO financial_report_amounts (
			import_id, currency, amount_decimal, amount_scale, minor_units,
			commission_state, tax_state, covered_application_id
		) VALUES ('import-accepted', 'EUR', -1.25, 2, -125, 'included', 'not_itemized', 'app-constraint')
	`)
	expectConstraint(t, database, "financial_report_imports_one_accepted_period_idx", `
		INSERT INTO financial_report_imports (
			id, project_id, provider, report_kind, period_start, period_end, source,
			received_sha256, byte_size, content_type, parser_version, status, reason
		) VALUES (
			'import-second', 'project-constraint', 'apple_app_store', 'apple_financial_consolidated',
			DATE '2026-09-28', DATE '2026-11-01', 'upload',
			decode(repeat('cd', 32), 'hex'), 12, 'text/plain', 'apple-financial-consolidated-1', 'accepted', ''
		)
	`)
	mustExec(t, database, `
		INSERT INTO financial_report_imports (
			id, project_id, provider, report_kind, period_start, period_end, source,
			received_sha256, byte_size, content_type, parser_version, status, reason
		) VALUES (
			'import-unrecognized', 'project-constraint', 'google_play', 'google_play_earnings',
			DATE '2026-10-01', DATE '2026-10-31', 'upload',
			decode(repeat('ef', 32), 'hex'), 8, 'text/csv', 'fixture-required', 'unrecognized', 'fixture_required'
		)
	`)
	expectConstraint(t, database, "financial_report_amounts_accepted_valid", `
		INSERT INTO financial_report_amounts (
			import_id, currency, amount_decimal, amount_scale, minor_units,
			commission_state, tax_state, covered_application_id
		) VALUES ('import-unrecognized', 'USD', 1.00, 2, 100, 'included', 'not_itemized', 'app-constraint')
	`)
	expectConstraint(t, database, "financial_report_amounts_currency_valid", `
		INSERT INTO financial_report_amounts (
			import_id, currency, amount_decimal, amount_scale,
			commission_state, tax_state, covered_application_id
		) VALUES ('import-accepted', 'usd', 1.00, 2, 'included', 'not_itemized', 'app-constraint')
	`)
}

// TestEntitlementProjectionMigrationConstraints verifies source and catalog integrity for current access.
func TestEntitlementProjectionMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
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

// TestInboxOutboxMigrationConstraints verifies audit deduplication and terminal outcome consistency.
func TestInboxOutboxMigrationConstraints(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
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
	expectConstraint(t, database, "inbox_messages_outcome_valid", `
		INSERT INTO inbox_messages (
			id, project_id, application_id, provider, kind, content_type,
			payload_ciphertext, payload_fingerprint, encryption_key_id,
			received_at, available_at, processed_at, failed_at
		) VALUES (
			'inbox-invalid', $1, $2, 'huawei_appgallery', 'subscription_event', 'application/json',
			'ciphertext', $3, 'test-key', now(), now(), now(), now()
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
	expectConstraint(t, database, "outbox_events_outcome_valid", `
		INSERT INTO outbox_events (
			id, project_id, application_id, event_type, aggregate_type, aggregate_id,
			payload, payload_fingerprint, occurred_at, available_at, delivered_at, failed_at
		) VALUES (
			'outbox-invalid', $1, $2, 'entitlement.changed', 'customer', 'customer-1',
			'{"entitlement":"pro"}'::jsonb, $3, now(), now(), now(), now()
		)
	`, fixture.projectID, fixture.applicationID, bytes.Repeat([]byte{8}, 32))
}

// TestReconciliationScheduleRetryConstraints verifies a retry cannot be both scheduled and dead.
func TestReconciliationScheduleRetryConstraints(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	fingerprint := bytes.Repeat([]byte{11}, 32)
	mustExec(t, database, `
		INSERT INTO reconciliation_schedule_retries (
			id, project_id, application_id, customer_id,
			payload_ciphertext, payload_fingerprint, encryption_key_id
		) VALUES (
			'retry-pending', $1, $2, $3,
			'ciphertext', $4, 'test-key'
		)
	`, fixture.projectID, fixture.applicationID, fixture.customerID, fingerprint)
	mustExec(t, database, `UPDATE reconciliation_schedule_retries SET dead_at = now() WHERE id = 'retry-pending'`)
	mustExec(t, database, `DELETE FROM reconciliation_schedule_retries`)
	expectConstraint(t, database, "reconciliation_schedule_retries_outcome_exclusive", `
		INSERT INTO reconciliation_schedule_retries (
			id, project_id, application_id, customer_id,
			payload_ciphertext, payload_fingerprint, encryption_key_id, scheduled_at, dead_at
		) VALUES (
			'retry-both', $1, $2, $3,
			'ciphertext', $4, 'test-key', now(), now()
		)
	`, fixture.projectID, fixture.applicationID, fixture.customerID, fingerprint)
}

// TestManualGrantConstraints verifies the schema keeps manual sources exclusive and stored.
func TestManualGrantConstraints(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	purchase := seedPurchase(t, database)
	fixture := purchase.catalog
	grantInsert := `
		INSERT INTO manual_grants (
			id, project_id, customer_id, application_id, product_id, entitlement_id,
			reason, actor_key_id, idempotency_key, effective_starts_at, effective_ends_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'admin-key', $8, now(), now() + interval '30 days')
	`
	expectConstraint(t, database, "manual_grants_id_valid", grantInsert,
		"imp-0123456789abcdef0123456789abcdef", fixture.projectID, fixture.customerID, fixture.applicationID,
		fixture.productID, fixture.entitlementID, "Support", "grant-id")
	expectConstraint(t, database, "manual_grants_reason_valid", grantInsert,
		"mgr-0123456789abcdef0123456789abcdef", fixture.projectID, fixture.customerID, fixture.applicationID,
		fixture.productID, fixture.entitlementID, "line one\nline two", "grant-reason")
	mustExec(t, database, grantInsert,
		"mgr-0123456789abcdef0123456789abcdef", fixture.projectID, fixture.customerID, fixture.applicationID,
		fixture.productID, fixture.entitlementID, "Support ticket 1", "grant-1")
	expectConstraint(t, database, "manual_grants_idempotency_unique", grantInsert,
		"mgr-1123456789abcdef0123456789abcdef", fixture.projectID, fixture.customerID, fixture.applicationID,
		fixture.productID, fixture.entitlementID, "Support ticket 1", "grant-1")
	expectConstraint(t, database, "manual_grants_revocation_valid", `
		UPDATE manual_grants SET revoked_at = now() WHERE id = 'mgr-0123456789abcdef0123456789abcdef'
	`)
	expectConstraint(t, database, "customer_entitlements_source_exclusive", `
		INSERT INTO customer_entitlements (
			project_id, customer_id, entitlement_id, source_kind, source_observation_id, source_manual_grant_id,
			source_product_id, source_customer_id, access_status, access_reason, effective_starts_at
		) VALUES ($1, $2, $3, 'manual', $4, 'mgr-0123456789abcdef0123456789abcdef', $5, $2, 'allowed', 'manual_grant', now())
	`, fixture.projectID, fixture.customerID, fixture.entitlementID, purchase.observationID, fixture.productID)
	mustExec(t, database, `
		INSERT INTO customer_entitlements (
			project_id, customer_id, entitlement_id, source_kind, source_manual_grant_id,
			source_product_id, source_customer_id, access_status, access_reason, effective_starts_at
		) VALUES ($1, $2, $3, 'manual', 'mgr-0123456789abcdef0123456789abcdef', $4, $2, 'allowed', 'manual_grant', now())
	`, fixture.projectID, fixture.customerID, fixture.entitlementID, fixture.productID)
	expectConstraint(t, database, "customer_entitlements_manual_reason_valid", `
		UPDATE customer_entitlements SET access_reason = 'purchase_valid' WHERE customer_id = $1
	`, fixture.customerID)
	expectConstraint(t, database, "customer_entitlements_manual_reason_valid", `
		INSERT INTO customer_entitlements (
			project_id, customer_id, entitlement_id, source_observation_id,
			source_product_id, source_customer_id, access_status, access_reason, effective_starts_at
		) VALUES ($1, 'customer-store', $2, $3, $4, 'customer-store', 'allowed', 'manual_grant', now())
	`, fixture.projectID, fixture.entitlementID, purchase.observationID, fixture.productID)
	expectConstraint(t, database, "purchase_observations_reason_valid", `
		UPDATE purchase_observations SET access_reason = 'manual_grant' WHERE id = $1
	`, purchase.observationID)
	expectConstraint(t, database, "customer_entitlements_source_manual_fk", `
		UPDATE customer_entitlements SET source_manual_grant_id = 'mgr-ffffffffffffffffffffffffffffffff'
		WHERE customer_id = $1
	`, fixture.customerID)
}

// TestAgentAPIKeyConstraints verifies agent key scope rules, the write flag, and the project reference.
func TestAgentAPIKeyConstraints(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	salt := bytes.Repeat([]byte{1}, 16)
	hash := bytes.Repeat([]byte{2}, 32)
	keyInsert := `
		INSERT INTO api_keys (id, role, project_id, application_id, allow_writes, secret_salt, secret_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	mustExec(t, database, keyInsert, "admin-key", "admin", nil, nil, false, salt, hash)
	mustExec(t, database, keyInsert, "application-key", "application", fixture.projectID, fixture.applicationID, false, salt, hash)
	mustExec(t, database, keyInsert, "agent-key", "agent", fixture.projectID, nil, false, salt, hash)
	mustExec(t, database, keyInsert, "agent-writer-key", "agent", fixture.projectID, nil, true, salt, hash)
	expectConstraint(t, database, "api_keys_scope_valid", keyInsert,
		"agent-without-project", "agent", nil, nil, false, salt, hash)
	expectConstraint(t, database, "api_keys_scope_valid", keyInsert,
		"agent-with-application", "agent", fixture.projectID, fixture.applicationID, false, salt, hash)
	expectConstraint(t, database, "api_keys_allow_writes_valid", keyInsert,
		"admin-writer", "admin", nil, nil, true, salt, hash)
	expectConstraint(t, database, "api_keys_allow_writes_valid", keyInsert,
		"application-writer", "application", fixture.projectID, fixture.applicationID, true, salt, hash)
	expectConstraint(t, database, "api_keys_role_valid", keyInsert,
		"operator-key", "operator", fixture.projectID, nil, false, salt, hash)
	expectConstraint(t, database, "api_keys_project_fk", keyInsert,
		"agent-missing-project", "agent", "missing-project", nil, false, salt, hash)

	var indexDefinition string
	if err := database.conn.QueryRow(database.ctx,
		`SELECT indexdef FROM pg_indexes WHERE indexname = 'api_keys_project_idx'`,
	).Scan(&indexDefinition); err != nil {
		t.Fatalf("load api_keys_project_idx: %v", err)
	}
	if strings.Contains(indexDefinition, "WHERE") {
		t.Fatalf("api_keys_project_idx = %s, want a full index for the cascading project foreign key", indexDefinition)
	}
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
	if err := postgres.RemoveRiver(ctx, databaseURL); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset River integration schema: %v", err)
	}
	if err := migrator.MigrateTo(ctx, 0); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset integration PostgreSQL: %v", err)
	}
	if err := migrator.MigrateTo(ctx, targetVersion); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("migrate integration PostgreSQL to %d: %v", targetVersion, err)
	}
	if targetVersion == postgres.LatestVersion {
		if err := postgres.MigrateRiver(ctx, databaseURL); err != nil {
			_ = connection.Close(ctx)
			t.Fatalf("migrate River integration schema: %v", err)
		}
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), testDatabaseTimeout)
		defer cleanupCancel()
		if err := postgres.RemoveRiver(cleanupCtx, databaseURL); err != nil {
			t.Errorf("reset River PostgreSQL during cleanup: %v", err)
		}
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
			source_product_id, source_customer_id, access_status, access_reason, effective_starts_at
		) VALUES ($1, $2, $3, $4, $5, $2, 'allowed', 'purchase_valid', now() - interval '1 hour')
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

// credentialInsertSQL returns one valid protected application credential insert.
func credentialInsertSQL() string {
	return `
		INSERT INTO application_credentials (
			project_id, application_id, kind, content_type, schema_version,
			payload_ciphertext, payload_fingerprint, encryption_key_id
		) VALUES (
			$1, $2, $3, 'application/json', 1,
			'ciphertext', $4, 'test-key'
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
