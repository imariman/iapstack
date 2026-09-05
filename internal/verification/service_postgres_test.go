package verification_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/verification"
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

// coordinatedVerificationAdapter releases one provider result only after every concurrent call is ready.
type coordinatedVerificationAdapter struct {
	result stores.VerificationResult
	ready  chan<- struct{}
	start  <-chan struct{}
}

// postgresVerificationScenario contains one provider result and its matching service inputs.
type postgresVerificationScenario struct {
	command verification.Command
	result  stores.VerificationResult
	clock   fakeClock
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

// TestServiceRebuildsIndependentSourcesWithPostgreSQL verifies a shadow grant survives revocation of the selected source.
func TestServiceRebuildsIndependentSourcesWithPostgreSQL(t *testing.T) {
	database := openVerificationTestDatabase(t)
	fixture := newVerificationFixture(t)
	seedVerificationCatalog(t, database, fixture)
	seedVerificationProduct(
		t,
		database,
		fixture,
		"product-2",
		"provider-product-2",
	)

	initial := postgresVerificationScenario{
		command: fixture.command,
		result:  fixture.result,
		clock:   fixture.clock,
	}
	verifyPostgresScenario(t, database, initial)

	secondAllowed := newPostgresVerificationScenario(
		t,
		fixture,
		"provider-product-2",
		"observation-product-2-allowed",
		fixture.result.VerifiedAt.Add(time.Minute),
		core.AccessAllowed,
	)
	verifyPostgresScenario(t, database, secondAllowed)

	firstDenied := newPostgresVerificationScenario(
		t,
		fixture,
		verificationProviderProductID,
		"observation-product-1-denied",
		fixture.result.VerifiedAt.Add(2*time.Minute),
		core.AccessDenied,
	)
	result := verifyPostgresScenario(t, database, firstDenied)
	assertVerificationEntitlement(
		t,
		result.Entitlements,
		core.AccessAllowed,
		"product-2",
		"observation-product-2-allowed",
	)
	assertVerificationTableCount(t, database, "purchase_observations", 3)
}

// TestServiceRejectsStaleSourceAndRefreshesReobservedStateWithPostgreSQL verifies durable source-time ordering.
func TestServiceRejectsStaleSourceAndRefreshesReobservedStateWithPostgreSQL(t *testing.T) {
	database := openVerificationTestDatabase(t)
	fixture := newVerificationFixture(t)
	seedVerificationCatalog(t, database, fixture)

	newerDenied := newPostgresVerificationScenario(
		t,
		fixture,
		verificationProviderProductID,
		"observation-newer-denied",
		fixture.result.VerifiedAt.Add(2*time.Minute),
		core.AccessDenied,
	)
	verifyPostgresScenario(t, database, newerDenied)

	olderAllowed := newPostgresVerificationScenario(
		t,
		fixture,
		verificationProviderProductID,
		"observation-older-allowed",
		fixture.result.VerifiedAt.Add(time.Minute),
		core.AccessAllowed,
	)
	staleResult := verifyPostgresScenario(t, database, olderAllowed)
	assertVerificationEntitlement(
		t,
		staleResult.Entitlements,
		core.AccessDenied,
		verificationProductID,
		"observation-newer-denied",
	)
	if staleResult.Entitlements[0].Version != 1 {
		t.Fatalf("stale projection version = %d, want unchanged version 1", staleResult.Entitlements[0].Version)
	}

	reobservedAllowed := olderAllowed
	reobservedAllowed.result.VerifiedAt = fixture.result.VerifiedAt.Add(3 * time.Minute)
	reobservedAllowed.result.Observations = append(
		[]core.PurchaseObservation(nil),
		reobservedAllowed.result.Observations...,
	)
	reobservedAllowed.result.Observations[0].ObservedAt = reobservedAllowed.result.VerifiedAt
	reobservedAllowed.clock.now = reobservedAllowed.result.VerifiedAt.Add(time.Second)
	freshResult := verifyPostgresScenario(t, database, reobservedAllowed)
	assertVerificationEntitlement(
		t,
		freshResult.Entitlements,
		core.AccessAllowed,
		verificationProductID,
		"observation-older-allowed",
	)
	if freshResult.Entitlements[0].Version != 2 {
		t.Fatalf("reobserved projection version = %d, want 2", freshResult.Entitlements[0].Version)
	}
}

// TestServiceSerializesConcurrentIndependentSourcesWithPostgreSQL verifies overlapping grant and revocation cannot lose access.
func TestServiceSerializesConcurrentIndependentSourcesWithPostgreSQL(t *testing.T) {
	database := openVerificationTestDatabase(t)
	fixture := newVerificationFixture(t)
	seedVerificationCatalog(t, database, fixture)
	seedVerificationProduct(t, database, fixture, "product-2", "provider-product-2")
	verifyPostgresScenario(t, database, postgresVerificationScenario{
		command: fixture.command,
		result:  fixture.result,
		clock:   fixture.clock,
	})

	firstDenied := newPostgresVerificationScenario(
		t,
		fixture,
		verificationProviderProductID,
		"observation-concurrent-product-1-denied",
		fixture.result.VerifiedAt.Add(2*time.Minute),
		core.AccessDenied,
	)
	secondAllowed := newPostgresVerificationScenario(
		t,
		fixture,
		"provider-product-2",
		"observation-concurrent-product-2-allowed",
		fixture.result.VerifiedAt.Add(time.Minute),
		core.AccessAllowed,
	)
	runConcurrentPostgresVerification(t, database, firstDenied, secondAllowed)
	assertVerificationEntitlement(
		t,
		loadVerificationEntitlements(t, database),
		core.AccessAllowed,
		"product-2",
		"observation-concurrent-product-2-allowed",
	)
}

// TestServiceSerializesConcurrentStaleSourceWithPostgreSQL verifies commit order cannot let an older snapshot win.
func TestServiceSerializesConcurrentStaleSourceWithPostgreSQL(t *testing.T) {
	database := openVerificationTestDatabase(t)
	fixture := newVerificationFixture(t)
	seedVerificationCatalog(t, database, fixture)

	olderAllowed := newPostgresVerificationScenario(
		t,
		fixture,
		verificationProviderProductID,
		"observation-concurrent-older-allowed",
		fixture.result.VerifiedAt.Add(time.Minute),
		core.AccessAllowed,
	)
	newerDenied := newPostgresVerificationScenario(
		t,
		fixture,
		verificationProviderProductID,
		"observation-concurrent-newer-denied",
		fixture.result.VerifiedAt.Add(2*time.Minute),
		core.AccessDenied,
	)
	runConcurrentPostgresVerification(t, database, olderAllowed, newerDenied)
	assertVerificationEntitlement(
		t,
		loadVerificationEntitlements(t, database),
		core.AccessDenied,
		verificationProductID,
		"observation-concurrent-newer-denied",
	)
}

// Provider identifies the provider implemented by the coordinated test adapter.
func (adapter *coordinatedVerificationAdapter) Provider() core.Provider {
	return core.ProviderHuaweiAppGallery
}

// Verify waits for the shared start signal before returning one authoritative result.
func (adapter *coordinatedVerificationAdapter) Verify(
	ctx context.Context,
	_ stores.VerificationRequest,
) (stores.VerificationResult, error) {
	select {
	case adapter.ready <- struct{}{}:
	case <-ctx.Done():
		return stores.VerificationResult{}, ctx.Err()
	}
	select {
	case <-adapter.start:
		return adapter.result, nil
	case <-ctx.Done():
		return stores.VerificationResult{}, ctx.Err()
	}
}

// Reconcile is unused by coordinated verification tests.
func (adapter *coordinatedVerificationAdapter) Reconcile(
	context.Context,
	stores.ReconciliationRequest,
) (stores.VerificationResult, error) {
	return stores.VerificationResult{}, errors.New("unexpected coordinated reconciliation")
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
	if err := postgres.RemoveRiver(ctx, databaseURL); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset verification River schema: %v", err)
	}
	if err := migrator.MigrateTo(ctx, 0); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset verification PostgreSQL: %v", err)
	}
	if err := migrator.Up(ctx); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("migrate verification PostgreSQL: %v", err)
	}
	if err := postgres.MigrateRiver(ctx, databaseURL); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("migrate verification River schema: %v", err)
	}
	database := &verificationTestDatabase{ctx: ctx, conn: connection, migrator: migrator}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), verificationDatabaseTimeout)
		defer cleanupCancel()
		if err := postgres.RemoveRiver(cleanupCtx, databaseURL); err != nil {
			t.Errorf("reset verification River schema during cleanup: %v", err)
		}
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

// seedVerificationProduct adds one independently owned product that grants the fixture entitlement.
func seedVerificationProduct(
	t *testing.T,
	database *verificationTestDatabase,
	fixture verificationFixture,
	productID core.ProductID,
	providerProductID core.ProviderProductID,
) {
	t.Helper()
	mustVerificationExec(t, database, `
		INSERT INTO products (id, project_id, kind)
		VALUES ($1, $2, $3)
	`, productID, fixture.application.ProjectID, fixture.catalog.Product.Kind)
	for _, entitlement := range fixture.catalog.Entitlements {
		mustVerificationExec(t, database, `
			INSERT INTO product_entitlements (project_id, product_id, entitlement_id)
			VALUES ($1, $2, $3)
		`, entitlement.ProjectID, productID, entitlement.ID)
	}
	mustVerificationExec(t, database, `
		INSERT INTO store_products (project_id, application_id, product_id, provider_product_id)
		VALUES ($1, $2, $3, $4)
	`, fixture.application.ProjectID, fixture.application.ID, productID, providerProductID)
}

// newPostgresVerificationScenario builds one authoritative lifecycle snapshot for a chosen provider product.
func newPostgresVerificationScenario(
	t *testing.T,
	fixture verificationFixture,
	providerProductID core.ProviderProductID,
	observationID core.ObservationID,
	observedAt time.Time,
	access core.AccessStatus,
) postgresVerificationScenario {
	t.Helper()
	evidence, err := stores.NewEvidence(
		repositoryContentType(),
		[]byte(`{"observation":"`+string(observationID)+`"}`),
	)
	if err != nil {
		t.Fatalf("NewEvidence() PostgreSQL scenario error = %v", err)
	}
	observation := fixture.result.Observations[0]
	observation.ID = observationID
	observation.ProductID = providerProductID
	observation.ObservedAt = observedAt
	observation.Access = access
	switch access {
	case core.AccessAllowed:
		observation.State = core.LifecycleActive
		observation.ProviderState = "purchased"
		observation.AccessReason = core.AccessReasonPurchaseValid
	case core.AccessDenied:
		observation.State = core.LifecycleExpired
		observation.ProviderState = "expired"
		observation.AccessReason = core.AccessReasonExpired
	default:
		t.Fatalf("unsupported PostgreSQL scenario access %q", access)
	}
	command := fixture.command
	command.ClaimedProducts = []core.ProviderProductID{providerProductID}
	command.Evidence = evidence
	return postgresVerificationScenario{
		command: command,
		result: stores.VerificationResult{
			VerifiedAt: observedAt,
			Artifacts: []stores.VerifiedArtifact{{
				Kind: "provider_response", Evidence: evidence,
			}},
			Observations: []core.PurchaseObservation{observation},
		},
		clock: fakeClock{now: observedAt.Add(time.Second)},
	}
}

// verifyPostgresScenario persists one scenario through the complete verification service.
func verifyPostgresScenario(
	t *testing.T,
	database *verificationTestDatabase,
	scenario postgresVerificationScenario,
) verification.Result {
	t.Helper()
	service := newVerificationService(
		t,
		database.store,
		&fakeAdapter{result: scenario.result},
		&fakeProtector{},
		scenario.clock,
	)
	result, err := service.Verify(database.ctx, scenario.command)
	if err != nil {
		t.Fatalf("Verify() PostgreSQL scenario error = %v", err)
	}
	return result
}

// runConcurrentPostgresVerification starts two provider results together and waits for durable completion.
func runConcurrentPostgresVerification(
	t *testing.T,
	database *verificationTestDatabase,
	scenarios ...postgresVerificationScenario,
) {
	t.Helper()
	ready := make(chan struct{}, len(scenarios))
	start := make(chan struct{})
	errorsChannel := make(chan error, len(scenarios))
	var workers sync.WaitGroup
	for _, scenario := range scenarios {
		scenario := scenario
		service := newVerificationService(
			t,
			database.store,
			&coordinatedVerificationAdapter{result: scenario.result, ready: ready, start: start},
			&fakeProtector{},
			scenario.clock,
		)
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := service.Verify(database.ctx, scenario.command)
			errorsChannel <- err
		}()
	}
	for range scenarios {
		select {
		case <-ready:
		case <-database.ctx.Done():
			close(start)
			workers.Wait()
			t.Fatalf("wait for concurrent provider calls: %v", database.ctx.Err())
		}
	}
	close(start)
	workers.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent Verify() error = %v", err)
		}
	}
}

// loadVerificationEntitlements returns the committed projection snapshot for the fixture customer.
func loadVerificationEntitlements(
	t *testing.T,
	database *verificationTestDatabase,
) []persistence.CustomerEntitlement {
	t.Helper()
	var entitlements []persistence.CustomerEntitlement
	err := database.store.Transact(database.ctx, func(repository persistence.Transaction) error {
		var loadErr error
		entitlements, loadErr = repository.CustomerEntitlements(
			database.ctx,
			verificationProjectID,
			verificationCustomerID,
		)
		return loadErr
	})
	if err != nil {
		t.Fatalf("CustomerEntitlements() PostgreSQL error = %v", err)
	}
	return entitlements
}

// assertVerificationEntitlement checks one final access decision and its durable source.
func assertVerificationEntitlement(
	t *testing.T,
	entitlements []persistence.CustomerEntitlement,
	wantAccess core.AccessStatus,
	wantProduct core.ProductID,
	wantObservation core.ObservationID,
) {
	t.Helper()
	if len(entitlements) != 1 ||
		entitlements[0].Projection.Access != wantAccess ||
		entitlements[0].Projection.SourceProductID != wantProduct ||
		entitlements[0].Projection.SourceObservationID != wantObservation {
		t.Fatalf(
			"entitlements = %#v, want access %q from product %q observation %q",
			entitlements,
			wantAccess,
			wantProduct,
			wantObservation,
		)
	}
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
