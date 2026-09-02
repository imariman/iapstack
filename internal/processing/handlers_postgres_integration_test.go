//go:build integration

package processing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/googleplay"
	"github.com/imariman/iapstack/internal/verification"
	"github.com/jackc/pgx/v5"
)

const (
	// processingIntegrationDatabaseURLKey names the opt-in PostgreSQL connection setting.
	processingIntegrationDatabaseURLKey = "IAPSTACK_TEST_DATABASE_URL"
	// processingIntegrationDatabaseTimeout bounds setup, execution, and cleanup.
	processingIntegrationDatabaseTimeout = 30 * time.Second
	// processingIntegrationDatabaseAdvisoryLock serializes schema-resetting test packages.
	processingIntegrationDatabaseAdvisoryLock int64 = 424090117
	// processingIntegrationProjectID identifies the durable test project.
	processingIntegrationProjectID = "processing-project-1"
	// processingIntegrationApplicationID identifies the Google Play test application.
	processingIntegrationApplicationID = "processing-application-1"
	// processingIntegrationCustomerID identifies the durable test customer.
	processingIntegrationCustomerID = "processing-customer-1"
	// processingIntegrationExternalCustomerID identifies the provider-bound customer.
	processingIntegrationExternalCustomerID = "processing-external-customer-1"
	// processingIntegrationProductID identifies the internal catalog product.
	processingIntegrationProductID = "processing-product-1"
	// processingIntegrationProviderProductID identifies the Google Play product.
	processingIntegrationProviderProductID = "premium_lifetime"
	// processingIntegrationEntitlementID identifies the projected access grant.
	processingIntegrationEntitlementID = "processing-entitlement-1"
	// processingIntegrationPurchaseToken identifies the protected lookup reference.
	processingIntegrationPurchaseToken = "processing-purchase-token-1"
)

type processingIntegrationDatabase struct {
	ctx      context.Context
	conn     *pgx.Conn
	migrator *postgres.Migrator
	store    *postgres.Store
}

type processingIntegrationClock struct {
	now time.Time
}

type processingIntegrationAdapter struct {
	verificationResult   stores.VerificationResult
	reconciliationResult stores.VerificationResult
	verifyCalls          int
	reconcileCalls       int
	postCommitCalls      int
}

type processingIntegrationProtection struct{}

var (
	_ stores.Adapter       = (*processingIntegrationAdapter)(nil)
	_ stores.PostCommitter = (*processingIntegrationAdapter)(nil)
	_ protection.Service   = processingIntegrationProtection{}
	_ verification.Clock   = processingIntegrationClock{}
)

// TestGooglePlayInboxPersistsReconciliationWithPostgreSQL verifies the durable RTDN pipeline and replay safety.
func TestGooglePlayInboxPersistsReconciliationWithPostgreSQL(t *testing.T) {
	database := openProcessingIntegrationDatabase(t)
	now := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
	application := core.Application{
		ID: processingIntegrationApplicationID, ProjectID: processingIntegrationProjectID,
		Store: core.StoreApplication{
			Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest,
			ID: "com.example.iapstack",
		},
	}
	customer := core.Customer{
		ID: processingIntegrationCustomerID, ProjectID: processingIntegrationProjectID,
		ExternalID: processingIntegrationExternalCustomerID,
	}
	seedProcessingIntegrationCatalog(t, database, application, customer)

	queryReference := processingIntegrationReference(
		t,
		core.ReferenceQuery,
		"purchase_token",
		processingIntegrationPurchaseToken,
	)
	customerBinding := processingIntegrationReference(
		t,
		core.ReferenceCustomerBinding,
		"obfuscated_external_account_id",
		processingIntegrationExternalCustomerID,
	)
	transactionReference := processingIntegrationReference(
		t,
		core.ReferenceTransaction,
		"order_id",
		"GPA.1234-5678-9012-34567",
	)
	protector := processingIntegrationProtection{}
	adapter := &processingIntegrationAdapter{
		verificationResult: processingIntegrationResult(
			t,
			application,
			"processing-observation-initial",
			core.LifecycleActive,
			core.AccessAllowed,
			core.AccessReasonPurchaseValid,
			now,
			queryReference,
			customerBinding,
			transactionReference,
		),
		reconciliationResult: processingIntegrationResult(
			t,
			application,
			"processing-observation-refunded",
			core.LifecycleRefunded,
			core.AccessDenied,
			core.AccessReasonRefunded,
			now.Add(time.Minute),
			queryReference,
			customerBinding,
			transactionReference,
		),
	}
	registry, err := stores.NewRegistry(adapter)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	verificationService, err := verification.NewService(
		database.store,
		registry,
		protector,
		verification.NewDefaultProjector(),
		processingIntegrationClock{now: now.Add(2 * time.Minute)},
	)
	if err != nil {
		t.Fatalf("NewService() verification error = %v", err)
	}
	clientEvidence := processingIntegrationEvidence(
		t,
		"application/json",
		[]byte(`{"purchase_token":"processing-purchase-token-1"}`),
	)
	initial, err := verificationService.Verify(database.ctx, verification.Command{
		ProjectID: processingIntegrationProjectID, ApplicationID: processingIntegrationApplicationID,
		ExternalCustomerID: processingIntegrationExternalCustomerID,
		ClaimedProducts:    []core.ProviderProductID{processingIntegrationProviderProductID},
		ExpectedCustomerBindings: []core.StoreReference{
			customerBinding,
		},
		Evidence: clientEvidence,
	})
	if err != nil {
		t.Fatalf("Verify() bootstrap error = %v", err)
	}
	if len(initial.Entitlements) != 1 || initial.Entitlements[0].Projection.Access != core.AccessAllowed ||
		initial.Entitlements[0].Version != 1 {
		t.Fatalf("Verify() bootstrap entitlements = %#v", initial.Entitlements)
	}

	message := saveProcessingIntegrationInbox(t, database, protector, application, now.Add(3*time.Minute))
	processingService, err := New(database.store, protector, verificationService)
	if err != nil {
		t.Fatalf("New() processing error = %v", err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := processingService.HandleInbox(database.ctx, message); err != nil {
			t.Fatalf("HandleInbox() attempt %d error = %v", attempt, err)
		}
	}

	if adapter.verifyCalls != 1 || adapter.reconcileCalls != 2 || adapter.postCommitCalls != 3 {
		t.Fatalf(
			"adapter calls = verify %d, reconcile %d, post-commit %d",
			adapter.verifyCalls,
			adapter.reconcileCalls,
			adapter.postCommitCalls,
		)
	}
	assertProcessingIntegrationCount(t, database, "inbox_messages", 1)
	assertProcessingIntegrationCount(t, database, "purchase_evidence", 2)
	assertProcessingIntegrationCount(t, database, "purchase_observations", 2)
	assertProcessingIntegrationCount(t, database, "customer_entitlements", 1)
	assertProcessingIntegrationCount(t, database, "outbox_events", 2)
	var access core.AccessStatus
	var reason core.AccessReason
	var version int64
	if err := database.conn.QueryRow(database.ctx, `
		SELECT access_status, access_reason, version
		FROM customer_entitlements
		WHERE customer_id = $1 AND entitlement_id = $2
	`, processingIntegrationCustomerID, processingIntegrationEntitlementID).Scan(&access, &reason, &version); err != nil {
		t.Fatalf("load reconciled entitlement: %v", err)
	}
	if access != core.AccessDenied || reason != core.AccessReasonRefunded || version != 2 {
		t.Fatalf("reconciled entitlement = (%q, %q, %d)", access, reason, version)
	}
	var notificationEvidence int
	if err := database.conn.QueryRow(database.ctx, `
		SELECT count(*) FROM purchase_evidence
		WHERE kind = 'provider_notification_reconciliation'
	`).Scan(&notificationEvidence); err != nil {
		t.Fatalf("count notification evidence: %v", err)
	}
	if notificationEvidence != 1 {
		t.Fatalf("notification evidence count = %d, want 1", notificationEvidence)
	}
}

// Provider identifies the Google Play test adapter.
func (adapter *processingIntegrationAdapter) Provider() core.Provider {
	return core.ProviderGooglePlay
}

// Verify returns the configured initial authoritative purchase state.
func (adapter *processingIntegrationAdapter) Verify(
	_ context.Context,
	_ stores.VerificationRequest,
) (stores.VerificationResult, error) {
	adapter.verifyCalls++
	return adapter.verificationResult, nil
}

// Reconcile returns the configured lifecycle state after an RTDN signal.
func (adapter *processingIntegrationAdapter) Reconcile(
	_ context.Context,
	_ stores.ReconciliationRequest,
) (stores.VerificationResult, error) {
	adapter.reconcileCalls++
	return adapter.reconciliationResult, nil
}

// PostCommit records provider completion after durable persistence.
func (adapter *processingIntegrationAdapter) PostCommit(
	_ context.Context,
	_ stores.PostCommitRequest,
) error {
	adapter.postCommitCalls++
	return nil
}

// Now returns the deterministic integration-test timestamp.
func (clock processingIntegrationClock) Now() time.Time {
	return clock.now
}

// Protect creates deterministic scoped test ciphertext and fingerprints.
func (processingIntegrationProtection) Protect(
	_ context.Context,
	request protection.Request,
) (protection.Value, error) {
	if err := request.Validate(); err != nil {
		return protection.Value{}, err
	}
	payload := request.Bytes()
	return protection.Value{
		Ciphertext:  append([]byte(nil), payload...),
		Fingerprint: processingIntegrationFingerprint(request.Scope, payload),
		KeyID:       "integration-key",
	}, nil
}

// Open authenticates the deterministic scope before returning test plaintext.
func (processingIntegrationProtection) Open(
	_ context.Context,
	request protection.OpenRequest,
) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if request.Value.Fingerprint != processingIntegrationFingerprint(request.Scope, request.Value.Ciphertext) {
		return nil, protection.ErrOpenFailed
	}
	return append([]byte(nil), request.Value.Ciphertext...), nil
}

// processingIntegrationFingerprint derives one deterministic scope-bound digest.
func processingIntegrationFingerprint(scope protection.Scope, payload []byte) [sha256.Size]byte {
	scoped := []byte(fmt.Sprintf(
		"%s\x00%s\x00%s\x00",
		scope.ProjectID,
		scope.ApplicationID,
		scope.Purpose,
	))
	return sha256.Sum256(append(scoped, payload...))
}

// processingIntegrationResult builds one valid authoritative provider response.
func processingIntegrationResult(
	t *testing.T,
	application core.Application,
	observationID core.ObservationID,
	state core.LifecycleState,
	access core.AccessStatus,
	reason core.AccessReason,
	observedAt time.Time,
	references ...core.StoreReference,
) stores.VerificationResult {
	t.Helper()
	artifact := processingIntegrationEvidence(
		t,
		"application/json",
		[]byte(fmt.Sprintf(`{"observation_id":%q}`, observationID)),
	)
	observation := core.PurchaseObservation{
		ID: observationID, ApplicationID: application.ID, Store: application.Store,
		ProductID: processingIntegrationProviderProductID, ProductKind: core.ProductKindNonConsumable,
		State: state, ProviderState: string(state), Access: access, AccessReason: reason,
		Ownership: core.OwnershipPurchased, Quantity: 1,
		OccurredAt: observedAt.Add(-time.Minute), ObservedAt: observedAt,
		EffectivePeriod: core.EffectivePeriod{StartsAt: observedAt.Add(-time.Minute)},
		Renewal:         core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable},
		References:      append([]core.StoreReference(nil), references...),
	}
	queryReferences := observation.ReferencesFor(core.ReferenceQuery)
	return stores.VerificationResult{
		VerifiedAt: observedAt,
		Artifacts:  []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: artifact}},
		Observations: []core.PurchaseObservation{
			observation,
		},
		PostCommitActions: []stores.PostCommitAction{{
			Kind: "acknowledge_purchase", ProductID: processingIntegrationProviderProductID,
			ProductKind: core.ProductKindNonConsumable, QueryReferences: queryReferences,
		}},
	}
}

// processingIntegrationReference constructs one valid opaque provider reference.
func processingIntegrationReference(
	t *testing.T,
	role core.ReferenceRole,
	kind string,
	value string,
) core.StoreReference {
	t.Helper()
	reference, err := core.NewStoreReference(role, kind, value)
	if err != nil {
		t.Fatalf("NewStoreReference(%q, %q) error = %v", role, kind, err)
	}
	return reference
}

// processingIntegrationEvidence constructs one validated provider evidence value.
func processingIntegrationEvidence(t *testing.T, contentType string, payload []byte) stores.Evidence {
	t.Helper()
	evidence, err := stores.NewEvidence(contentType, payload)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	return evidence
}

// seedProcessingIntegrationCatalog creates the durable scope needed by the pipeline.
func seedProcessingIntegrationCatalog(
	t *testing.T,
	database *processingIntegrationDatabase,
	application core.Application,
	customer core.Customer,
) {
	t.Helper()
	err := database.store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		if err := repository.PutProject(database.ctx, processingIntegrationProjectID); err != nil {
			return err
		}
		if err := repository.PutApplication(database.ctx, application); err != nil {
			return err
		}
		if _, err := repository.PutCustomer(database.ctx, customer); err != nil {
			return err
		}
		if err := repository.PutEntitlementDefinition(database.ctx, core.Entitlement{
			ID: processingIntegrationEntitlementID, ProjectID: processingIntegrationProjectID, Key: "premium",
		}); err != nil {
			return err
		}
		if err := repository.PutProduct(database.ctx, core.Product{
			ID: processingIntegrationProductID, ProjectID: processingIntegrationProjectID,
			Kind: core.ProductKindNonConsumable,
			EntitlementIDs: []core.EntitlementID{
				processingIntegrationEntitlementID,
			},
		}); err != nil {
			return err
		}
		return repository.PutStoreProduct(database.ctx, processingIntegrationProjectID, core.StoreProduct{
			ApplicationID: processingIntegrationApplicationID,
			ProductID:     processingIntegrationProductID,
			ProviderID:    processingIntegrationProviderProductID,
		})
	})
	if err != nil {
		t.Fatalf("seed processing integration catalog: %v", err)
	}
}

// saveProcessingIntegrationInbox persists and reloads one protected RTDN job.
func saveProcessingIntegrationInbox(
	t *testing.T,
	database *processingIntegrationDatabase,
	protector protection.Service,
	application core.Application,
	receivedAt time.Time,
) persistence.QueueMessage {
	t.Helper()
	payload, err := json.Marshal(googleplay.NotificationEnvelope{
		MessageID: "pubsub-message-processing-1", Kind: googleplay.NotificationKindOneTimeProduct,
		NotificationType: 1, EventTime: receivedAt,
		PurchaseToken: processingIntegrationPurchaseToken,
		ProductKind:   core.ProductKindNonConsumable, ProviderProductID: processingIntegrationProviderProductID,
	})
	if err != nil {
		t.Fatalf("Marshal() notification error = %v", err)
	}
	protected, err := protection.Protect(database.ctx, protector, protection.Scope{
		ProjectID: application.ProjectID, ApplicationID: application.ID, Purpose: inboxProtectionPurpose,
	}, payload)
	if err != nil {
		t.Fatalf("Protect() inbox error = %v", err)
	}
	var message persistence.QueueMessage
	err = database.store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		id, saveErr := repository.SaveInboxMessage(database.ctx, persistence.InboxMessage{
			ID: "processing-inbox-1", ProjectID: application.ProjectID, ApplicationID: application.ID,
			Provider: application.Store.Provider, Kind: "google_play_rtdn",
			ContentType: googleplay.NotificationContentType, Payload: protected,
			ReceivedAt: receivedAt, AvailableAt: receivedAt,
		})
		if saveErr != nil {
			return saveErr
		}
		message, saveErr = repository.QueueMessage(database.ctx, persistence.QueueInbox, id)
		return saveErr
	})
	if err != nil {
		t.Fatalf("save and load integration inbox: %v", err)
	}
	return message
}

// openProcessingIntegrationDatabase resets and opens one migrated PostgreSQL fixture.
func openProcessingIntegrationDatabase(t *testing.T) *processingIntegrationDatabase {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(processingIntegrationDatabaseURLKey))
	if databaseURL == "" {
		t.Skipf("%s is not configured", processingIntegrationDatabaseURLKey)
	}
	ctx, cancel := context.WithTimeout(context.Background(), processingIntegrationDatabaseTimeout)
	t.Cleanup(cancel)
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to processing integration PostgreSQL: %v", err)
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, processingIntegrationDatabaseAdvisoryLock); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("lock processing integration PostgreSQL: %v", err)
	}
	migrator, err := postgres.NewMigrator(ctx, connection)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("NewMigrator() error = %v", err)
	}
	if err := postgres.RemoveRiver(ctx, databaseURL); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset processing integration River schema: %v", err)
	}
	if err := migrator.MigrateTo(ctx, 0); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset processing integration PostgreSQL: %v", err)
	}
	if err := migrator.Up(ctx); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("migrate processing integration PostgreSQL: %v", err)
	}
	if err := postgres.MigrateRiver(ctx, databaseURL); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("migrate processing integration River schema: %v", err)
	}
	database := &processingIntegrationDatabase{ctx: ctx, conn: connection, migrator: migrator}
	database.store, err = postgres.OpenStore(ctx, databaseURL)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() {
		database.store.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), processingIntegrationDatabaseTimeout)
		defer cleanupCancel()
		if err := postgres.RemoveRiver(cleanupCtx, databaseURL); err != nil {
			t.Errorf("reset processing integration River schema during cleanup: %v", err)
		}
		if err := migrator.MigrateTo(cleanupCtx, 0); err != nil {
			t.Errorf("reset processing integration PostgreSQL during cleanup: %v", err)
		}
		if _, err := connection.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1)`, processingIntegrationDatabaseAdvisoryLock); err != nil {
			t.Errorf("unlock processing integration PostgreSQL: %v", err)
		}
		if err := connection.Close(cleanupCtx); err != nil {
			t.Errorf("close processing integration PostgreSQL: %v", err)
		}
	})
	return database
}

// assertProcessingIntegrationCount verifies one fixed pipeline table cardinality.
func assertProcessingIntegrationCount(
	t *testing.T,
	database *processingIntegrationDatabase,
	table string,
	want int,
) {
	t.Helper()
	allowed := map[string]struct{}{
		"customer_entitlements": {},
		"inbox_messages":        {},
		"outbox_events":         {},
		"purchase_evidence":     {},
		"purchase_observations": {},
	}
	if _, ok := allowed[table]; !ok {
		t.Fatalf("table %q is not allowed by processing integration helper", table)
	}
	var got int
	if err := database.conn.QueryRow(database.ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
