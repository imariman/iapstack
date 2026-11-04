//go:build integration

package processing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/verification"
)

// lineageIntegrationSnapshot describes one authoritative Google Play subscription snapshot.
type lineageIntegrationSnapshot struct {
	id         core.ObservationID
	token      string
	order      string
	access     core.AccessStatus
	startsAt   time.Time
	endsAt     time.Time
	observedAt time.Time
}

// retireFailingStore delegates storage but rejects every reconciliation completion.
type retireFailingStore struct {
	persistence.OperationsStore
}

// retireFailingTransaction rejects the completion that retires the next reconciliation generation.
type retireFailingTransaction struct {
	persistence.OperationsTransaction
}

// TestExpiredLineageReconciliationKeepsRepurchaseAndStops verifies an expired lineage neither revokes a repurchase nor reschedules.
func TestExpiredLineageReconciliationKeepsRepurchaseAndStops(t *testing.T) {
	database := openProcessingIntegrationDatabase(t)
	purchasedAt := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
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
	if _, err := database.conn.Exec(database.ctx, `
		UPDATE products SET kind = 'subscription' WHERE id = $1
	`, processingIntegrationProductID); err != nil {
		t.Fatalf("make integration product a subscription: %v", err)
	}

	expiredAt := purchasedAt.AddDate(0, 1, 0)
	activeA := lineageIntegrationSnapshot{
		id: "lineage-a-active", token: "lineage-token-a", order: "GPA.0000-0000-0000-00001",
		access: core.AccessAllowed, startsAt: purchasedAt, endsAt: expiredAt, observedAt: purchasedAt,
	}
	expiredA := activeA
	expiredA.id = "lineage-a-expired"
	expiredA.access = core.AccessDenied
	expiredA.observedAt = expiredAt.Add(time.Hour)
	repurchasedAt := expiredAt.Add(24 * time.Hour)
	activeB := lineageIntegrationSnapshot{
		id: "lineage-b-active", token: "lineage-token-b", order: "GPA.0000-0000-0000-00002",
		access: core.AccessAllowed, startsAt: repurchasedAt, endsAt: repurchasedAt.AddDate(0, 1, 0),
		observedAt: repurchasedAt,
	}
	reconciledAt := repurchasedAt.Add(24 * time.Hour)

	protector := processingIntegrationProtection{}
	adapter := &processingIntegrationAdapter{}
	registry, err := stores.NewRegistry(adapter)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	verificationService, err := verification.NewService(
		database.store, registry, protector, verification.NewDefaultProjector(),
		processingIntegrationClock{now: reconciledAt},
	)
	if err != nil {
		t.Fatalf("NewService() verification error = %v", err)
	}
	processingService, err := New(database.store, protector, verificationService)
	if err != nil {
		t.Fatalf("New() processing error = %v", err)
	}
	processingService.clock = func() time.Time { return reconciledAt }

	for _, snapshot := range []lineageIntegrationSnapshot{activeA, expiredA, activeB} {
		adapter.verificationResult = lineageIntegrationResult(t, application, snapshot)
		if _, err := processingService.verify(
			database.ctx, application.ProjectID, application.ID, lineageIntegrationPayload(snapshot.token),
		); err != nil {
			t.Fatalf("verify %s error = %v", snapshot.id, err)
		}
	}
	assertLineageIntegrationAccess(t, database, core.AccessAllowed, activeB.id)

	expiredA.observedAt = reconciledAt
	adapter.verificationResult = lineageIntegrationResult(t, application, expiredA)
	expiredJob := saveLineageIntegrationJob(t, database, processingService, application, activeA.token, repurchasedAt)
	if err := processingService.HandleReconciliation(database.ctx, expiredJob); err != nil {
		t.Fatalf("HandleReconciliation() expired lineage error = %v", err)
	}
	assertLineageIntegrationAccess(t, database, core.AccessAllowed, activeB.id)
	if completed := lineageIntegrationNextCompleted(t, database, activeA.token, reconciledAt); !completed {
		t.Fatal("expired lineage next reconciliation is still runnable, want it retired")
	}
	// A client resubmission of the same evidence in that window must reopen the retired generation.
	saveLineageIntegrationJob(t, database, processingService, application, activeA.token, reconciledAt)
	if completed := lineageIntegrationNextCompleted(t, database, activeA.token, reconciledAt); completed {
		t.Fatal("rescheduled expired lineage stayed retired, want its next reconciliation runnable")
	}

	activeB.observedAt = reconciledAt
	adapter.verificationResult = lineageIntegrationResult(t, application, activeB)
	activeJob := saveLineageIntegrationJob(t, database, processingService, application, activeB.token, repurchasedAt)
	if err := processingService.HandleReconciliation(database.ctx, activeJob); err != nil {
		t.Fatalf("HandleReconciliation() active lineage error = %v", err)
	}
	assertLineageIntegrationAccess(t, database, core.AccessAllowed, activeB.id)
	if completed := lineageIntegrationNextCompleted(t, database, activeB.token, reconciledAt); completed {
		t.Fatal("active lineage next reconciliation was retired, want it runnable")
	}
}

// TestExpiredLineageRetireFailureKeepsCommittedRefresh verifies a failed retire neither fails nor repeats the refresh.
func TestExpiredLineageRetireFailureKeepsCommittedRefresh(t *testing.T) {
	database := openProcessingIntegrationDatabase(t)
	purchasedAt := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
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
	if _, err := database.conn.Exec(database.ctx, `
		UPDATE products SET kind = 'subscription' WHERE id = $1
	`, processingIntegrationProductID); err != nil {
		t.Fatalf("make integration product a subscription: %v", err)
	}
	expiredAt := purchasedAt.AddDate(0, 1, 0)
	reconciledAt := expiredAt.Add(48 * time.Hour)
	expired := lineageIntegrationSnapshot{
		id: "lineage-retire-expired", token: "lineage-token-retire", order: "GPA.0000-0000-0000-00003",
		access: core.AccessDenied, startsAt: purchasedAt, endsAt: expiredAt, observedAt: reconciledAt,
	}

	protector := processingIntegrationProtection{}
	adapter := &processingIntegrationAdapter{verificationResult: lineageIntegrationResult(t, application, expired)}
	registry, err := stores.NewRegistry(adapter)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	verificationService, err := verification.NewService(
		database.store, registry, protector, verification.NewDefaultProjector(),
		processingIntegrationClock{now: reconciledAt},
	)
	if err != nil {
		t.Fatalf("NewService() verification error = %v", err)
	}
	processingService, err := New(retireFailingStore{database.store}, protector, verificationService)
	if err != nil {
		t.Fatalf("New() processing error = %v", err)
	}
	processingService.clock = func() time.Time { return reconciledAt }

	job := saveLineageIntegrationJob(t, database, processingService, application, expired.token, expiredAt)
	if err := processingService.HandleReconciliation(database.ctx, job); err != nil {
		t.Fatalf("HandleReconciliation() error = %v, want nil after a committed refresh", err)
	}
	if completed := lineageIntegrationNextCompleted(t, database, expired.token, reconciledAt); completed {
		t.Fatal("next reconciliation completed despite the rejected retire")
	}
	var observations int
	if err := database.conn.QueryRow(database.ctx, `
		SELECT count(*) FROM purchase_observations WHERE id = $1
	`, expired.id).Scan(&observations); err != nil {
		t.Fatalf("count refreshed observations: %v", err)
	}
	if observations != 1 {
		t.Fatalf("refreshed observations = %d, want 1", observations)
	}
}

// Operate wraps each operations transaction so its reconciliation completions fail.
func (store retireFailingStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	return store.OperationsStore.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		return operation(retireFailingTransaction{repository})
	})
}

// CompleteQueue rejects reconciliation completions and delegates every other queue.
func (transaction retireFailingTransaction) CompleteQueue(
	ctx context.Context,
	completion persistence.QueueCompletion,
) error {
	if completion.Queue == persistence.QueueReconciliation {
		return errors.Join(persistence.ErrUnavailable, errors.New("reject reconciliation completion"))
	}
	return transaction.OperationsTransaction.CompleteQueue(ctx, completion)
}

// lineageIntegrationResult builds one valid Google Play subscription response for a lineage snapshot.
func lineageIntegrationResult(
	t *testing.T,
	application core.Application,
	snapshot lineageIntegrationSnapshot,
) stores.VerificationResult {
	t.Helper()
	state := core.LifecycleActive
	reason := core.AccessReasonPurchaseValid
	renewal := core.Renewal{Mode: core.RenewalAuto, Status: core.RenewalEnabled}
	if snapshot.access == core.AccessDenied {
		state = core.LifecycleExpired
		reason = core.AccessReasonExpired
		renewal.Status = core.RenewalDisabled
	}
	endsAt := snapshot.endsAt
	observation := core.PurchaseObservation{
		ID: snapshot.id, ApplicationID: application.ID, Store: application.Store,
		ProductID: processingIntegrationProviderProductID, ProductKind: core.ProductKindSubscription,
		State: state, ProviderState: string(state), Access: snapshot.access, AccessReason: reason,
		Ownership: core.OwnershipPurchased, Quantity: 1,
		OccurredAt: snapshot.startsAt, ObservedAt: snapshot.observedAt,
		EffectivePeriod: core.EffectivePeriod{StartsAt: snapshot.startsAt, EndsAt: &endsAt},
		Renewal:         renewal,
		References: []core.StoreReference{
			processingIntegrationReference(t, core.ReferenceTransaction, "order_id", snapshot.order),
			processingIntegrationReference(t, core.ReferenceLineage, "purchase_token", snapshot.token),
			processingIntegrationReference(t, core.ReferenceQuery, "purchase_token", snapshot.token),
			processingIntegrationReference(
				t, core.ReferenceCustomerBinding, "obfuscated_external_account_id",
				processingIntegrationExternalCustomerID,
			),
		},
	}
	return stores.VerificationResult{
		VerifiedAt: snapshot.observedAt,
		Artifacts: []stores.VerifiedArtifact{{
			Kind: "provider_response",
			Evidence: processingIntegrationEvidence(
				t, "application/json", []byte(`{"observation_id":"`+string(snapshot.id)+`"}`),
			),
		}},
		Observations: []core.PurchaseObservation{observation},
	}
}

// lineageIntegrationPayload builds the protected replay payload for one purchase token.
func lineageIntegrationPayload(token string) jobs.VerificationPayload {
	return jobs.VerificationPayload{
		ExternalCustomerID: processingIntegrationExternalCustomerID,
		ClaimedProducts:    []core.ProviderProductID{processingIntegrationProviderProductID},
		Evidence: json.RawMessage(
			`{"product_kind":"subscription","purchase_token":"` + token + `"}`,
		),
	}
}

// saveLineageIntegrationJob schedules and loads the reconciliation generation for one purchase token.
func saveLineageIntegrationJob(
	t *testing.T,
	database *processingIntegrationDatabase,
	service *Service,
	application core.Application,
	token string,
	scheduledAt time.Time,
) persistence.QueueMessage {
	t.Helper()
	clock := service.clock
	service.clock = func() time.Time { return scheduledAt }
	defer func() { service.clock = clock }()
	if err := service.schedule(
		database.ctx, application.ProjectID, application.ID, processingIntegrationCustomerID,
		lineageIntegrationPayload(token),
	); err != nil {
		t.Fatalf("schedule %s error = %v", token, err)
	}
	id := lineageIntegrationJobID(t, database, token, nextReconciliationTime(scheduledAt))
	var message persistence.QueueMessage
	err := database.store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		message, loadErr = repository.QueueMessage(database.ctx, persistence.QueueReconciliation, id)
		return loadErr
	})
	if err != nil {
		t.Fatalf("load reconciliation job %s: %v", token, err)
	}
	return message
}

// lineageIntegrationJobID finds the reconciliation generation scheduled for one token and time.
func lineageIntegrationJobID(
	t *testing.T,
	database *processingIntegrationDatabase,
	token string,
	scheduledFor time.Time,
) string {
	t.Helper()
	payload, err := json.Marshal(jobs.ReconciliationPayload{
		Verification: lineageIntegrationPayload(token),
		ScheduledFor: scheduledFor,
	})
	if err != nil {
		t.Fatalf("Marshal() reconciliation payload error = %v", err)
	}
	var id string
	if err := database.conn.QueryRow(database.ctx, `
		SELECT id FROM reconciliation_jobs WHERE payload_ciphertext = $1
	`, payload).Scan(&id); err != nil {
		t.Fatalf("find reconciliation job for %s at %s: %v", token, scheduledFor, err)
	}
	return id
}

// lineageIntegrationNextCompleted reports whether the worker would skip the generation after one run.
func lineageIntegrationNextCompleted(
	t *testing.T,
	database *processingIntegrationDatabase,
	token string,
	ranAt time.Time,
) bool {
	t.Helper()
	id := lineageIntegrationJobID(t, database, token, nextReconciliationTime(ranAt))
	var message persistence.QueueMessage
	err := database.store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		message, loadErr = repository.QueueMessage(database.ctx, persistence.QueueReconciliation, id)
		return loadErr
	})
	if err != nil {
		t.Fatalf("load next reconciliation job for %s: %v", token, err)
	}
	if message.Failed {
		t.Fatalf("next reconciliation job for %s failed, want runnable or completed", token)
	}
	return message.Completed
}

// assertLineageIntegrationAccess verifies the durable projection access and source observation.
func assertLineageIntegrationAccess(
	t *testing.T,
	database *processingIntegrationDatabase,
	access core.AccessStatus,
	observationID core.ObservationID,
) {
	t.Helper()
	var gotAccess core.AccessStatus
	var gotObservation core.ObservationID
	if err := database.conn.QueryRow(database.ctx, `
		SELECT access_status, source_observation_id
		FROM customer_entitlements
		WHERE customer_id = $1 AND entitlement_id = $2
	`, processingIntegrationCustomerID, processingIntegrationEntitlementID).Scan(&gotAccess, &gotObservation); err != nil {
		t.Fatalf("load lineage entitlement: %v", err)
	}
	if gotAccess != access || gotObservation != observationID {
		t.Fatalf("entitlement = (%q, %q), want (%q, %q)", gotAccess, gotObservation, access, observationID)
	}
}
