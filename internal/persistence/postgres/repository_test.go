package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
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
