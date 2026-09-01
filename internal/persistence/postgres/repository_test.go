package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/protection"
)

const (
	// purchaseEvidenceKind identifies client purchase submissions in repository tests.
	purchaseEvidenceKind = "purchase_submission"
	// providerArtifactKind identifies authoritative provider responses in repository tests.
	providerArtifactKind = "provider_response"
	// repositoryContentType is the canonical JSON media type used by repository tests.
	repositoryContentType = "application/json"
	// repositoryEncryptionKeyID identifies the deterministic test protector key.
	repositoryEncryptionKeyID = "test-key"
	// repositoryCredentialKind identifies the logical application credential package.
	repositoryCredentialKind = "server_api"
	// repositoryCredentialSchemaVersion identifies the test provider credential format.
	repositoryCredentialSchemaVersion = 1
)

// purchaseWrites groups one coherent verification persistence graph for tests.
type purchaseWrites struct {
	evidence    persistence.EvidenceWrite
	artifact    persistence.ArtifactWrite
	observation persistence.ObservationWrite
	projection  persistence.EntitlementProjection
	outbox      persistence.OutboxEvent
}

// apiKeyRecord creates one deterministic valid verifier record for lifecycle tests.
func apiKeyRecord(
	id string,
	role persistence.APIKeyRole,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	createdAt time.Time,
) persistence.APIKeyRecord {
	var salt [16]byte
	var hash [32]byte
	salt[0] = 1
	hash[0] = 1
	return persistence.APIKeyRecord{
		ID: id, Role: role, ProjectID: projectID, ApplicationID: applicationID,
		SecretSalt: salt, SecretHash: hash, CreatedAt: createdAt,
	}
}

// TestCredentialRepositoryOptimisticRotation verifies scoped reads, retries, and stale conflicts.
func TestCredentialRepositoryOptimisticRotation(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	created := putCredential(t, database.ctx, store, credentialWrite(fixture, "credential-v1", 0))
	if created.Revision != 1 {
		t.Fatalf("created credential revision = %d, want 1", created.Revision)
	}

	retried := putCredential(t, database.ctx, store, credentialWrite(fixture, "credential-v1", 0))
	if retried.Revision != created.Revision {
		t.Fatalf("retried credential revision = %d, want %d", retried.Revision, created.Revision)
	}
	conflictingCreate := credentialWrite(fixture, "credential-conflict", 0)
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, err := repository.PutCredential(database.ctx, conflictingCreate)
		return err
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("conflicting PutCredential() error = %v, want ErrConflict", err)
	}

	rotatedWrite := credentialWrite(fixture, "credential-v2", created.Revision)
	rotated := putCredential(t, database.ctx, store, rotatedWrite)
	if rotated.Revision != 2 || !rotated.UpdatedAt.After(rotated.CreatedAt) {
		t.Fatalf("rotated credential = %#v, want revision 2 with a later update", rotated)
	}
	retryWrite := rotatedWrite
	retryWrite.Payload.Ciphertext = []byte("different-randomized-ciphertext")
	retriedRotation := putCredential(t, database.ctx, store, retryWrite)
	if retriedRotation.Revision != rotated.Revision ||
		string(retriedRotation.Payload.Ciphertext) == string(retryWrite.Payload.Ciphertext) {
		t.Fatalf("retried rotated credential = %#v, want existing logical revision", retriedRotation)
	}

	staleWrite := credentialWrite(fixture, "credential-v3", created.Revision)
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, err := repository.PutCredential(database.ctx, staleWrite)
		return err
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale PutCredential() error = %v, want ErrConflict", err)
	}

	loaded := loadCredential(t, database.ctx, store, rotated.CredentialKey)
	if loaded.Revision != rotated.Revision || loaded.Payload.Fingerprint != rotated.Payload.Fingerprint {
		t.Fatalf("Credential() = %#v, want rotated revision", loaded)
	}
	crossProjectKey := rotated.CredentialKey
	crossProjectKey.ProjectID = "other-project"
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, err := repository.Credential(database.ctx, crossProjectKey)
		return err
	})
	if !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("cross-project Credential() error = %v, want ErrNotFound", err)
	}
	assertTableCount(t, database, "application_credentials", 1)
}

// TestAPIKeyLifecycleListsAndRevokesWithoutLosingFinalAdmin verifies secret-free metadata and lifecycle invariants.
func TestAPIKeyLifecycleListsAndRevokesWithoutLosingFinalAdmin(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	createdAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	records := []persistence.APIKeyRecord{
		apiKeyRecord("admin-key-1", persistence.APIKeyRoleAdmin, "", "", createdAt),
		apiKeyRecord("admin-key-2", persistence.APIKeyRoleAdmin, "", "", createdAt.Add(time.Minute)),
		apiKeyRecord("application-key-1", persistence.APIKeyRoleApplication,
			core.ProjectID(fixture.projectID), core.ApplicationID(fixture.applicationID), createdAt.Add(2*time.Minute)),
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		for _, record := range records {
			if err := repository.PutAPIKey(database.ctx, record); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("PutAPIKey() error = %v", err)
	}

	var listed []persistence.APIKeySummary
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var err error
		listed, err = repository.APIKeys(database.ctx)
		return err
	}); err != nil {
		t.Fatalf("APIKeys() error = %v", err)
	}
	if len(listed) != len(records) || listed[0].ID != "application-key-1" {
		t.Fatalf("APIKeys() = %#v, want three newest-first summaries", listed)
	}

	revokedAt := time.Now().UTC().Truncate(time.Microsecond)
	var revoked persistence.APIKeySummary
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var err error
		revoked, err = repository.RevokeAPIKey(database.ctx, "application-key-1", revokedAt)
		return err
	}); err != nil {
		t.Fatalf("RevokeAPIKey() application error = %v", err)
	}
	if revoked.RevokedAt == nil || !revoked.RevokedAt.Equal(revokedAt) {
		t.Fatalf("revoked application key = %#v, want revocation time %v", revoked, revokedAt)
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, err := repository.APIKey(database.ctx, "application-key-1")
		return err
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("revoked APIKey() error = %v, want ErrNotFound", err)
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		retried, err := repository.RevokeAPIKey(database.ctx, "application-key-1", revokedAt.Add(time.Hour))
		if err == nil && (retried.RevokedAt == nil || !retried.RevokedAt.Equal(revokedAt)) {
			t.Fatalf("retried RevokeAPIKey() = %#v, want original lifecycle", retried)
		}
		return err
	}); err != nil {
		t.Fatalf("retried RevokeAPIKey() error = %v", err)
	}

	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, err := repository.RevokeAPIKey(database.ctx, "admin-key-1", revokedAt)
		return err
	}); err != nil {
		t.Fatalf("RevokeAPIKey() first admin error = %v", err)
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, err := repository.RevokeAPIKey(database.ctx, "admin-key-2", revokedAt)
		return err
	}); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("RevokeAPIKey() final admin error = %v, want ErrConflict", err)
	}
}

// TestConcurrentAPIKeyRevocationPreservesOneAdmin verifies advisory locking across competing requests.
func TestConcurrentAPIKeyRevocationPreservesOneAdmin(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	store := openRepositoryStore(t, database.ctx)
	createdAt := time.Now().UTC().Add(-time.Hour)
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		for _, id := range []string{"admin-key-1", "admin-key-2"} {
			if err := repository.PutAPIKey(database.ctx, apiKeyRecord(
				id, persistence.APIKeyRoleAdmin, "", "", createdAt,
			)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("PutAPIKey() error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, id := range []string{"admin-key-1", "admin-key-2"} {
		workers.Add(1)
		go func(keyID string) {
			defer workers.Done()
			<-start
			results <- store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
				_, err := repository.RevokeAPIKey(database.ctx, keyID, time.Now().UTC())
				return err
			})
		}(id)
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, persistence.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent RevokeAPIKey() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent revocation outcomes = (%d success, %d conflict), want (1, 1)", successes, conflicts)
	}
	var activeAdministrators int
	if err := database.conn.QueryRow(database.ctx, `
		SELECT count(*) FROM api_keys WHERE role = 'admin' AND revoked_at IS NULL
	`).Scan(&activeAdministrators); err != nil {
		t.Fatalf("count active administrators: %v", err)
	}
	if activeAdministrators != 1 {
		t.Fatalf("active administrator count = %d, want 1", activeAdministrators)
	}
}

// TestOpenStoreValidatesURL verifies fail-fast pool configuration errors.
func TestOpenStoreValidatesURL(t *testing.T) {
	t.Parallel()

	if _, err := postgres.OpenStore(context.Background(), ""); !errors.Is(err, postgres.ErrDatabaseURLRequired) {
		t.Fatalf("OpenStore() error = %v, want ErrDatabaseURLRequired", err)
	}
	if _, err := postgres.OpenStore(context.Background(), "postgres://%zz"); !errors.Is(err, postgres.ErrInvalidDatabaseURL) {
		t.Fatalf("OpenStore() error = %v, want ErrInvalidDatabaseURL", err)
	}
}

// TestStorePingRejectsOutdatedSchema verifies readiness follows the exact embedded migration version.
func TestStorePingRejectsOutdatedSchema(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	store := openRepositoryStore(t, database.ctx)
	mustMigrateTo(t, database, postgres.LatestVersion-1)

	err := store.Ping(database.ctx)
	if !errors.Is(err, postgres.ErrSchemaVersionMismatch) {
		t.Fatalf("Ping() error = %v, want ErrSchemaVersionMismatch", err)
	}
}

// TestCatalogRepositories verifies scoped application, customer, and product graph lookups.
func TestCatalogRepositories(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)

	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		application, err := repository.Application(
			database.ctx,
			core.ProjectID(fixture.projectID),
			core.ApplicationID(fixture.applicationID),
		)
		if err != nil {
			return err
		}
		if application.Store.Provider != core.ProviderHuaweiAppGallery {
			t.Fatalf("application provider = %q, want %q", application.Store.Provider, core.ProviderHuaweiAppGallery)
		}

		customer, err := repository.CustomerByExternalID(
			database.ctx,
			core.ProjectID(fixture.projectID),
			"customer-external",
		)
		if err != nil {
			return err
		}
		if customer.ID != core.CustomerID(fixture.customerID) {
			t.Fatalf("customer ID = %q, want %q", customer.ID, fixture.customerID)
		}

		products, err := repository.CatalogProducts(
			database.ctx,
			core.ProjectID(fixture.projectID),
			core.ApplicationID(fixture.applicationID),
			[]core.ProviderProductID{core.ProviderProductID(fixture.providerProductID)},
		)
		if err != nil {
			return err
		}
		if len(products) != 1 || len(products[0].Entitlements) != 1 {
			t.Fatalf("catalog products = %#v, want one product with one entitlement", products)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, err := repository.CatalogProducts(
			database.ctx,
			core.ProjectID(fixture.projectID),
			core.ApplicationID(fixture.applicationID),
			[]core.ProviderProductID{"unknown-product"},
		)
		return err
	})
	if !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("unknown CatalogProducts() error = %v, want ErrNotFound", err)
	}
}

// TestAdminQueryStoreReturnsBoundedSafeOverview verifies dashboard catalog and queue projections.
func TestAdminQueryStoreReturnsBoundedSafeOverview(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)

	projects, err := store.AdminProjects(database.ctx)
	if err != nil {
		t.Fatalf("AdminProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != core.ProjectID(fixture.projectID) {
		t.Fatalf("AdminProjects() = %#v, want seeded project", projects)
	}
	if projects[0].ApplicationCount != 1 || projects[0].CustomerCount != 1 || projects[0].ProductCount != 1 {
		t.Fatalf("AdminProjects() counts = %#v, want one application, customer, and product", projects[0])
	}

	overview, err := store.AdminProjectOverview(database.ctx, core.ProjectID(fixture.projectID))
	if err != nil {
		t.Fatalf("AdminProjectOverview() error = %v", err)
	}
	if len(overview.Applications) != 1 || overview.Applications[0].CredentialConfigured ||
		overview.Applications[0].CredentialRevision != 0 || overview.Applications[0].WebhookConfigured ||
		overview.Applications[0].WebhookRevision != 0 {
		t.Fatalf("overview applications = %#v, want one unconfigured application", overview.Applications)
	}
	if len(overview.Products) != 1 || len(overview.Products[0].EntitlementKeys) != 1 ||
		overview.Products[0].StoreMappingCount != 1 {
		t.Fatalf("overview products = %#v, want one fully mapped catalog product", overview.Products)
	}
	if len(overview.Customers) != 1 || overview.Customers[0].ExternalID != "customer-external" {
		t.Fatalf("overview customers = %#v, want seeded external customer", overview.Customers)
	}
	if len(overview.Queues) != 3 {
		t.Fatalf("overview queues = %#v, want three fixed queues", overview.Queues)
	}
	if len(overview.RecentTransactions) != 0 || len(overview.RecentWebhookEvents) != 0 {
		t.Fatalf("overview activity = (%#v, %#v), want empty activity", overview.RecentTransactions, overview.RecentWebhookEvents)
	}
	if overview.Analytics.WindowDays != 30 || len(overview.Analytics.DailyActivity) != 30 ||
		overview.Analytics.Revenue.Status != persistence.AdminRevenueSandboxOnly ||
		overview.Analytics.Revenue.RecognizedMinorUnits == nil ||
		*overview.Analytics.Revenue.RecognizedMinorUnits != 0 ||
		overview.Analytics.Revenue.ProductionApplicationCount != 0 ||
		overview.Analytics.Revenue.TestApplicationCount != 1 {
		t.Fatalf("overview analytics = %#v, want a zero-revenue 30-day sandbox window", overview.Analytics)
	}

	writes := newPurchaseWrites(t, fixture)
	observedAt := time.Now().UTC().Truncate(time.Second)
	writes.evidence.ReceivedAt = observedAt.Add(-2 * time.Minute)
	writes.observation.Observation.ObservedAt = observedAt
	writes.observation.Observation.OccurredAt = observedAt.Add(-time.Minute)
	writes.observation.Observation.EffectivePeriod = core.EffectivePeriod{StartsAt: observedAt.Add(-time.Minute)}
	writes.observation.Observation.Price = &core.PurchasePrice{Milliunits: 4990, Currency: "USD"}
	writes.projection.EffectivePeriod = writes.observation.Observation.EffectivePeriod
	writes.outbox.OccurredAt = observedAt
	writes.outbox.AvailableAt = observedAt
	persistPurchaseWrites(t, database.ctx, store, writes)

	active, err := store.AdminProjectOverview(database.ctx, core.ProjectID(fixture.projectID))
	if err != nil {
		t.Fatalf("active AdminProjectOverview() error = %v", err)
	}
	lastDay := active.Analytics.DailyActivity[len(active.Analytics.DailyActivity)-1]
	if active.Analytics.VerifiedCount != 1 || active.Analytics.AllowedCount != 1 ||
		active.Analytics.DeniedCount != 0 || active.Analytics.UnresolvedCount != 0 ||
		active.Analytics.ReversedCount != 0 || active.Analytics.ActiveEntitlementCount != 1 ||
		lastDay.Date != observedAt.Format(time.DateOnly) || lastDay.Verified != 1 || lastDay.Allowed != 1 {
		t.Fatalf("active analytics = %#v, last day = %#v, want one allowed verification", active.Analytics, lastDay)
	}
	if active.Analytics.SandboxValue.TransactionCount != 1 ||
		active.Analytics.SandboxValue.MissingPriceCount != 0 ||
		len(active.Analytics.SandboxValue.Amounts) != 1 ||
		active.Analytics.SandboxValue.Amounts[0].Milliunits != 4990 ||
		active.Analytics.SandboxValue.Amounts[0].Currency != "USD" {
		t.Fatalf("sandbox value = %#v, want one signed USD transaction", active.Analytics.SandboxValue)
	}
	if _, err := database.conn.Exec(database.ctx, `
		INSERT INTO application_credentials (
			project_id, application_id, kind, content_type, schema_version,
			payload_ciphertext, payload_fingerprint, encryption_key_id, revision
		) VALUES ($1, $2, 'huawei_server_api', 'application/json', 1,
			decode('01', 'hex'), decode(repeat('01', 32), 'hex'), 'test-key', 3)
	`, fixture.projectID, fixture.applicationID); err != nil {
		t.Fatalf("seed dashboard credential metadata: %v", err)
	}
	if _, err := database.conn.Exec(database.ctx, `
		INSERT INTO webhook_endpoints (
			project_id, application_id, url, secret_ciphertext,
			secret_fingerprint, encryption_key_id, revision
		) VALUES ($1, $2, 'https://example.com/iapstack', decode('01', 'hex'),
			decode(repeat('02', 32), 'hex'), 'test-key', 2)
	`, fixture.projectID, fixture.applicationID); err != nil {
		t.Fatalf("seed dashboard webhook metadata: %v", err)
	}
	configured, err := store.AdminProjectOverview(database.ctx, core.ProjectID(fixture.projectID))
	if err != nil {
		t.Fatalf("configured AdminProjectOverview() error = %v", err)
	}
	if !configured.Applications[0].CredentialConfigured || configured.Applications[0].CredentialRevision != 3 ||
		!configured.Applications[0].WebhookConfigured || configured.Applications[0].WebhookRevision != 2 {
		t.Fatalf("configured application = %#v, want credential revision 3 and webhook revision 2", configured.Applications[0])
	}
	if _, err := database.conn.Exec(database.ctx, `
		UPDATE applications SET environment = 'production' WHERE id = $1
	`, fixture.applicationID); err != nil {
		t.Fatalf("move dashboard application to production: %v", err)
	}
	production, err := store.AdminProjectOverview(database.ctx, core.ProjectID(fixture.projectID))
	if err != nil {
		t.Fatalf("production AdminProjectOverview() error = %v", err)
	}
	if production.Analytics.Revenue.Status != persistence.AdminRevenueStoreReportsRequired ||
		production.Analytics.Revenue.RecognizedMinorUnits != nil ||
		production.Analytics.Revenue.ProductionApplicationCount != 1 ||
		production.Analytics.Revenue.TestApplicationCount != 0 {
		t.Fatalf("production revenue = %#v, want store reports required without an estimate", production.Analytics.Revenue)
	}
	if production.Analytics.SandboxValue.TransactionCount != 0 ||
		len(production.Analytics.SandboxValue.Amounts) != 0 {
		t.Fatalf("production sandbox value = %#v, want no test transaction value", production.Analytics.SandboxValue)
	}

	_, err = store.AdminProjectOverview(database.ctx, "missing-project")
	if !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("missing AdminProjectOverview() error = %v, want ErrNotFound", err)
	}
}

// TestPutProductSerializesExactEntitlementSets verifies that concurrent writers cannot both commit divergent sets.
func TestPutProductSerializesExactEntitlementSets(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	additional := []core.Entitlement{
		{ID: "entitlement-2", ProjectID: core.ProjectID(fixture.projectID), Key: "second"},
		{ID: "entitlement-3", ProjectID: core.ProjectID(fixture.projectID), Key: "third"},
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		for _, entitlement := range additional {
			if err := repository.PutEntitlementDefinition(database.ctx, entitlement); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed additional entitlements: %v", err)
	}

	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, entitlementID := range []core.EntitlementID{additional[0].ID, additional[1].ID} {
		workers.Add(1)
		go func(entitlementID core.EntitlementID) {
			defer workers.Done()
			results <- store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
				ready <- struct{}{}
				<-start
				return repository.PutProduct(database.ctx, core.Product{
					ID: core.ProductID(fixture.productID), ProjectID: core.ProjectID(fixture.projectID),
					Kind: core.ProductKindNonConsumable,
					EntitlementIDs: []core.EntitlementID{
						core.EntitlementID(fixture.entitlementID), entitlementID,
					},
				})
			})
		}(entitlementID)
	}
	<-ready
	<-ready
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	conflicts := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, persistence.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent PutProduct() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent PutProduct() outcomes = (%d success, %d conflict), want (1, 1)", successes, conflicts)
	}
	var storedCount int
	if err := database.conn.QueryRow(database.ctx, `
		SELECT count(*) FROM product_entitlements WHERE project_id = $1 AND product_id = $2
	`, fixture.projectID, fixture.productID).Scan(&storedCount); err != nil {
		t.Fatalf("count product entitlements: %v", err)
	}
	if storedCount != 2 {
		t.Fatalf("stored entitlement count = %d, want exact winning set of 2", storedCount)
	}
}

// TestTransactionalPurchasePersistence verifies atomic, idempotent, and isolated repository writes.
func TestTransactionalPurchasePersistence(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	writes := newPurchaseWrites(t, fixture)

	firstOutboxID, firstVersion, firstChanged := persistPurchaseWrites(t, database.ctx, store, writes)
	if firstOutboxID != writes.outbox.ID || firstVersion != 1 {
		t.Fatalf("first persistence result = (%q, %d), want (%q, 1)", firstOutboxID, firstVersion, writes.outbox.ID)
	}
	if !firstChanged {
		t.Fatal("first PutEntitlement() changed = false, want true")
	}

	writes.outbox.ID = "outbox-replayed-with-new-id"
	writes.evidence.ReceivedAt = writes.evidence.ReceivedAt.Add(time.Minute)
	writes.observation.Observation.ObservedAt = writes.observation.Observation.ObservedAt.Add(time.Minute)
	replayedOutboxID, replayedVersion, replayedChanged := persistPurchaseWrites(t, database.ctx, store, writes)
	if replayedOutboxID != firstOutboxID {
		t.Fatalf("replayed outbox ID = %q, want existing %q", replayedOutboxID, firstOutboxID)
	}
	if replayedVersion != firstVersion {
		t.Fatalf("replayed entitlement version = %d, want %d", replayedVersion, firstVersion)
	}
	if replayedChanged {
		t.Fatal("replayed PutEntitlement() changed = true, want false")
	}
	assertTableCount(t, database, "purchase_evidence", 1)
	assertTableCount(t, database, "purchase_observations", 1)
	assertTableCount(t, database, "provider_references", 1)
	assertTableCount(t, database, "customer_entitlements", 1)
	assertTableCount(t, database, "outbox_events", 1)

	pricedWrites := writes
	if err := database.conn.QueryRow(
		database.ctx,
		"SELECT id FROM purchase_evidence WHERE application_id = $1",
		fixture.applicationID,
	).Scan(&pricedWrites.observation.EvidenceID); err != nil {
		t.Fatalf("load persisted evidence ID for price enrichment: %v", err)
	}
	pricedWrites.observation.Observation.Price = &core.PurchasePrice{Milliunits: 4990, Currency: "USD"}
	if err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		return repository.SaveObservation(database.ctx, pricedWrites.observation)
	}); err != nil {
		t.Fatalf("enrich SaveObservation() error = %v", err)
	}
	var storedMilliunits int64
	var storedCurrency string
	if err := database.conn.QueryRow(database.ctx, `
		SELECT price_milliunits, price_currency
		FROM purchase_observations
		WHERE id = $1
	`, writes.observation.Observation.ID).Scan(&storedMilliunits, &storedCurrency); err != nil {
		t.Fatalf("load enriched observation price: %v", err)
	}
	if storedMilliunits != 4990 || storedCurrency != "USD" {
		t.Fatalf("enriched observation price = (%d, %q), want (4990, USD)", storedMilliunits, storedCurrency)
	}

	var purchaseContext persistence.PurchaseReferenceContext
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var lookupErr error
		reference := writes.observation.References[0]
		purchaseContext, lookupErr = repository.PurchaseContextByReference(
			database.ctx,
			persistence.ProviderReferenceLookup{
				ProjectID: core.ProjectID(fixture.projectID), ApplicationID: core.ApplicationID(fixture.applicationID),
				Role: reference.Role, Kind: reference.Kind, Fingerprint: reference.Value.Fingerprint,
			},
		)
		return lookupErr
	}); err != nil {
		t.Fatalf("PurchaseContextByReference() error = %v", err)
	}
	if purchaseContext.Customer.ID != core.CustomerID(fixture.customerID) ||
		purchaseContext.Customer.ExternalID != "customer-external" ||
		purchaseContext.ProviderProductID != core.ProviderProductID(fixture.providerProductID) ||
		purchaseContext.ProductKind != core.ProductKindNonConsumable {
		t.Fatalf("PurchaseContextByReference() = %#v", purchaseContext)
	}

	conflictingWrites := writes
	if err := database.conn.QueryRow(
		database.ctx,
		"SELECT id FROM purchase_evidence WHERE application_id = $1",
		fixture.applicationID,
	).Scan(&conflictingWrites.observation.EvidenceID); err != nil {
		t.Fatalf("load persisted evidence ID: %v", err)
	}
	conflictingWrites.observation.Observation.ProviderState = "different-state"
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		return repository.SaveObservation(database.ctx, conflictingWrites.observation)
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("conflicting SaveObservation() error = %v, want ErrConflict", err)
	}

	crossProjectEvidence := writes.evidence
	crossProjectEvidence.ProjectID = "other-project"
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, err := repository.SaveEvidence(database.ctx, crossProjectEvidence)
		return err
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("cross-project SaveEvidence() error = %v, want ErrConflict", err)
	}

	rollbackEvidence := writes.evidence
	rollbackEvidence.Payload = repositoryProtectedValue("rollback-payload")
	rollbackError := errors.New("force rollback")
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		if _, err := repository.SaveEvidence(database.ctx, rollbackEvidence); err != nil {
			return err
		}
		return rollbackError
	})
	if !errors.Is(err, rollbackError) {
		t.Fatalf("rollback Transact() error = %v, want sentinel", err)
	}
	assertTableCount(t, database, "purchase_evidence", 1)
}

// TestSerializableTransactionRetriesConcurrentIdempotentWrites verifies transparent fresh-snapshot retries.
func TestSerializableTransactionRetriesConcurrentIdempotentWrites(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	write := newPurchaseWrites(t, fixture).evidence
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	errorsChannel := make(chan error, 2)
	identities := make(chan int64, 2)
	var attempts atomic.Int32
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			firstAttempt := true
			var evidenceID int64
			err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
				attempts.Add(1)
				if _, loadErr := repository.Application(database.ctx,
					core.ProjectID(fixture.projectID), core.ApplicationID(fixture.applicationID)); loadErr != nil {
					return loadErr
				}
				if firstAttempt {
					firstAttempt = false
					ready <- struct{}{}
					<-start
				}
				var saveErr error
				evidenceID, saveErr = repository.SaveEvidence(database.ctx, write)
				return saveErr
			})
			errorsChannel <- err
			identities <- evidenceID
		}()
	}
	<-ready
	<-ready
	close(start)
	workers.Wait()
	close(errorsChannel)
	close(identities)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent Transact() error = %v", err)
		}
	}
	var expectedID int64
	for identity := range identities {
		if expectedID == 0 {
			expectedID = identity
			continue
		}
		if identity != expectedID {
			t.Fatalf("concurrent evidence IDs = (%d, %d), want one identity", expectedID, identity)
		}
	}
	if attempts.Load() < 3 {
		t.Fatalf("transaction attempts = %d, want at least one retry", attempts.Load())
	}
	assertTableCount(t, database, "purchase_evidence", 1)
}

// TestOperationalQueueEnqueueAndCompletion verifies atomic River insertion and idempotent audit outcomes.
func TestOperationalQueueEnqueueAndCompletion(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	writes := newPurchaseWrites(t, fixture)
	var firstID, secondID string
	if err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		var saveErr error
		firstID, saveErr = repository.SaveOutboxEvent(database.ctx, writes.outbox)
		if saveErr != nil {
			return saveErr
		}
		secondID, saveErr = repository.SaveOutboxEvent(database.ctx, writes.outbox)
		return saveErr
	}); err != nil {
		t.Fatalf("SaveOutboxEvent() error = %v", err)
	}
	if firstID != writes.outbox.ID || secondID != firstID {
		t.Fatalf("outbox IDs = (%q, %q), want %q", firstID, secondID, writes.outbox.ID)
	}
	var riverJobID int64
	if err := database.conn.QueryRow(database.ctx,
		`SELECT river_job_id FROM outbox_events WHERE id = $1`, firstID).Scan(&riverJobID); err != nil {
		t.Fatalf("load linked River job: %v", err)
	}
	var kind, queue string
	if err := database.conn.QueryRow(database.ctx,
		`SELECT kind, queue FROM river_job WHERE id = $1`, riverJobID).Scan(&kind, &queue); err != nil {
		t.Fatalf("load River job: %v", err)
	}
	if kind != "iapstack_outbox" || queue != "iapstack_outbox" {
		t.Fatalf("River job = (%q, %q), want iapstack_outbox", kind, queue)
	}
	assertTableCount(t, database, "river_job", 1)

	completedAt := writes.outbox.AvailableAt.Add(time.Second)
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		message, loadErr := repository.QueueMessage(database.ctx, persistence.QueueOutbox, firstID)
		if loadErr != nil {
			return loadErr
		}
		if message.Completed || message.Failed || !json.Valid(message.JSONPayload) {
			return errors.New("loaded outbox audit record is invalid")
		}
		return repository.CompleteQueue(database.ctx, persistence.QueueCompletion{
			Queue: persistence.QueueOutbox, ID: firstID, CompletedAt: completedAt,
		})
	}); err != nil {
		t.Fatalf("complete outbox audit record: %v", err)
	}
	var deliveredAt time.Time
	if err := database.conn.QueryRow(database.ctx,
		`SELECT delivered_at FROM outbox_events WHERE id = $1`, firstID).Scan(&deliveredAt); err != nil {
		t.Fatalf("load outbox completion: %v", err)
	}
	if !deliveredAt.Equal(completedAt) {
		t.Fatalf("delivered_at = %v, want %v", deliveredAt, completedAt)
	}
}

// TestOperationalQueueRetentionPreservesRunnableJobs verifies cleanup cannot remove work River may still execute.
func TestOperationalQueueRetentionPreservesRunnableJobs(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	event := newPurchaseWrites(t, fixture).outbox
	if err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, saveErr := repository.SaveOutboxEvent(database.ctx, event)
		return saveErr
	}); err != nil {
		t.Fatalf("SaveOutboxEvent() error = %v", err)
	}

	cutoff := event.AvailableAt.Add(48 * time.Hour)
	completedAt := cutoff.Add(-time.Hour)
	if _, err := database.conn.Exec(database.ctx,
		`UPDATE outbox_events SET delivered_at = $1 WHERE id = $2`, completedAt, event.ID); err != nil {
		t.Fatalf("mark outbox audit record terminal: %v", err)
	}
	var deleted int64
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var purgeErr error
		deleted, purgeErr = repository.PurgeTerminalQueueRecords(database.ctx, cutoff, 100)
		return purgeErr
	}); err != nil {
		t.Fatalf("PurgeTerminalQueueRecords() with runnable job error = %v", err)
	}
	if deleted != 0 {
		t.Fatalf("PurgeTerminalQueueRecords() deleted = %d, want 0 while River job is runnable", deleted)
	}
	assertTableCount(t, database, "outbox_events", 1)

	if _, err := database.conn.Exec(database.ctx,
		`DELETE FROM river_job WHERE id = (SELECT river_job_id FROM outbox_events WHERE id = $1)`, event.ID); err != nil {
		t.Fatalf("simulate River terminal cleanup: %v", err)
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var purgeErr error
		deleted, purgeErr = repository.PurgeTerminalQueueRecords(database.ctx, cutoff, 100)
		return purgeErr
	}); err != nil {
		t.Fatalf("PurgeTerminalQueueRecords() after River cleanup error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("PurgeTerminalQueueRecords() deleted = %d, want 1 after River cleanup", deleted)
	}
	assertTableCount(t, database, "outbox_events", 0)
}

// TestOperationalQueueEnqueueRollsBackAtomically verifies River and audit records share one commit boundary.
func TestOperationalQueueEnqueueRollsBackAtomically(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	event := newPurchaseWrites(t, fixture).outbox
	expected := errors.New("force rollback")
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		if _, saveErr := repository.SaveOutboxEvent(database.ctx, event); saveErr != nil {
			return saveErr
		}
		return expected
	})
	if !errors.Is(err, expected) {
		t.Fatalf("Transact() error = %v, want forced rollback", err)
	}
	assertTableCount(t, database, "outbox_events", 0)
	assertTableCount(t, database, "river_job", 0)
}

// newPurchaseWrites builds one valid provider-neutral purchase persistence graph.
func newPurchaseWrites(t *testing.T, fixture catalogFixture) purchaseWrites {
	t.Helper()

	observedAt := time.Date(2026, time.August, 24, 1, 0, 0, 123000, time.UTC)
	transactionReference, err := core.NewStoreReference(
		core.ReferenceTransaction,
		"order_id",
		"sensitive-order-id",
	)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	observation := core.PurchaseObservation{
		ID:            "observation-repository-1",
		ApplicationID: core.ApplicationID(fixture.applicationID),
		Store: core.StoreApplication{
			Provider:    core.ProviderHuaweiAppGallery,
			Environment: core.EnvironmentSandbox,
			ID:          core.ProviderApplicationID(fixture.providerApplication),
		},
		ProductID:       core.ProviderProductID(fixture.providerProductID),
		ProductKind:     core.ProductKindNonConsumable,
		State:           core.LifecycleActive,
		ProviderState:   "purchased",
		Access:          core.AccessAllowed,
		AccessReason:    core.AccessReasonPurchaseValid,
		Ownership:       core.OwnershipPurchased,
		Quantity:        1,
		OccurredAt:      observedAt.Add(-time.Minute),
		ObservedAt:      observedAt,
		EffectivePeriod: core.EffectivePeriod{StartsAt: observedAt.Add(-time.Minute)},
		Renewal:         core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable},
		References:      []core.StoreReference{transactionReference},
	}
	payload := json.RawMessage(`{"customer_id":"customer-1","entitlement":"pro"}`)
	return purchaseWrites{
		evidence: persistence.EvidenceWrite{
			ProjectID:     core.ProjectID(fixture.projectID),
			ApplicationID: core.ApplicationID(fixture.applicationID),
			CustomerID:    core.CustomerID(fixture.customerID),
			Kind:          purchaseEvidenceKind,
			ContentType:   repositoryContentType,
			Payload:       repositoryProtectedValue("client-evidence"),
			ReceivedAt:    observedAt.Add(-2 * time.Minute),
		},
		artifact: persistence.ArtifactWrite{
			Kind:        providerArtifactKind,
			ContentType: repositoryContentType,
			Payload:     repositoryProtectedValue("provider-artifact"),
		},
		observation: persistence.ObservationWrite{
			ProjectID:   core.ProjectID(fixture.projectID),
			CustomerID:  core.CustomerID(fixture.customerID),
			ProductID:   core.ProductID(fixture.productID),
			Observation: observation,
			References: []persistence.ProtectedReference{
				{
					Role:  transactionReference.Role,
					Kind:  transactionReference.Kind,
					Value: repositoryProtectedValue(transactionReference.Value()),
				},
			},
		},
		projection: persistence.EntitlementProjection{
			ProjectID:           core.ProjectID(fixture.projectID),
			CustomerID:          core.CustomerID(fixture.customerID),
			EntitlementID:       core.EntitlementID(fixture.entitlementID),
			SourceObservationID: observation.ID,
			SourceProductID:     core.ProductID(fixture.productID),
			Access:              observation.Access,
			AccessReason:        observation.AccessReason,
			EffectivePeriod:     observation.EffectivePeriod,
		},
		outbox: persistence.OutboxEvent{
			ID:                 "outbox-repository-1",
			ProjectID:          core.ProjectID(fixture.projectID),
			ApplicationID:      core.ApplicationID(fixture.applicationID),
			EventType:          "entitlement.changed",
			AggregateType:      "customer",
			AggregateID:        fixture.customerID,
			Payload:            payload,
			PayloadFingerprint: sha256.Sum256(payload),
			OccurredAt:         observedAt,
			AvailableAt:        observedAt,
		},
	}
}

// persistPurchaseWrites saves one complete graph and returns its outbox ID and entitlement version.
func persistPurchaseWrites(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	writes purchaseWrites,
) (string, int64, bool) {
	t.Helper()

	var outboxID string
	var entitlementVersion int64
	var entitlementChanged bool
	err := store.Transact(ctx, func(repository persistence.Transaction) error {
		evidenceID, err := repository.SaveEvidence(ctx, writes.evidence)
		if err != nil {
			return err
		}
		writes.artifact.EvidenceID = evidenceID
		if err := repository.SaveArtifact(ctx, writes.artifact); err != nil {
			return err
		}
		writes.observation.EvidenceID = evidenceID
		if err := repository.SaveObservation(ctx, writes.observation); err != nil {
			return err
		}
		result, err := repository.PutEntitlement(ctx, writes.projection)
		if err != nil {
			return err
		}
		entitlementVersion = result.Entitlement.Version
		entitlementChanged = result.Changed
		outboxID, err = repository.SaveOutboxEvent(ctx, writes.outbox)
		return err
	})
	if err != nil {
		t.Fatalf("persist purchase transaction: %v", err)
	}
	return outboxID, entitlementVersion, entitlementChanged
}

// repositoryProtectedValue produces deterministic protected metadata for repository tests.
func repositoryProtectedValue(plaintext string) protection.Value {
	return protection.Value{
		Ciphertext:  []byte("ciphertext:" + plaintext),
		Fingerprint: sha256.Sum256([]byte(plaintext)),
		KeyID:       repositoryEncryptionKeyID,
	}
}

// credentialWrite builds one protected optimistic credential mutation.
func credentialWrite(
	fixture catalogFixture,
	plaintext string,
	expectedRevision int64,
) persistence.CredentialWrite {
	return persistence.CredentialWrite{
		CredentialKey: persistence.CredentialKey{
			ProjectID:     core.ProjectID(fixture.projectID),
			ApplicationID: core.ApplicationID(fixture.applicationID),
			Kind:          repositoryCredentialKind,
		},
		ContentType:      repositoryContentType,
		SchemaVersion:    repositoryCredentialSchemaVersion,
		Payload:          repositoryProtectedValue(plaintext),
		ExpectedRevision: expectedRevision,
	}
}

// putCredential persists one credential mutation or fails the current test.
func putCredential(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	write persistence.CredentialWrite,
) persistence.CredentialRecord {
	t.Helper()
	var record persistence.CredentialRecord
	err := store.Transact(ctx, func(repository persistence.Transaction) error {
		var err error
		record, err = repository.PutCredential(ctx, write)
		return err
	})
	if err != nil {
		t.Fatalf("PutCredential() error = %v", err)
	}
	return record
}

// loadCredential resolves one credential record or fails the current test.
func loadCredential(
	t *testing.T,
	ctx context.Context,
	store *postgres.Store,
	key persistence.CredentialKey,
) persistence.CredentialRecord {
	t.Helper()
	var record persistence.CredentialRecord
	err := store.Transact(ctx, func(repository persistence.Transaction) error {
		var err error
		record, err = repository.Credential(ctx, key)
		return err
	})
	if err != nil {
		t.Fatalf("Credential() error = %v", err)
	}
	return record
}

// openRepositoryStore opens a pool against the configured integration database.
func openRepositoryStore(t *testing.T, ctx context.Context) *postgres.Store {
	t.Helper()

	store, err := postgres.OpenStore(ctx, os.Getenv(testDatabaseURLKey))
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

// assertTableCount verifies the committed row count of one fixed test table.
func assertTableCount(t *testing.T, database *testDatabase, table string, expected int) {
	t.Helper()

	allowedTables := map[string]struct{}{
		"application_credentials": {},
		"customer_entitlements":   {},
		"outbox_events":           {},
		"provider_references":     {},
		"purchase_evidence":       {},
		"purchase_observations":   {},
		"river_job":               {},
	}
	if _, allowed := allowedTables[table]; !allowed {
		t.Fatalf("table %q is not allowed by test helper", table)
	}
	var actual int
	if err := database.conn.QueryRow(database.ctx, "SELECT count(*) FROM "+table).Scan(&actual); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if actual != expected {
		t.Fatalf("%s count = %d, want %d", table, actual, expected)
	}
}
