package postgres_test

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

const (
	// appleConsumptionSchemaVersion is the migration that adds consumption answers.
	appleConsumptionSchemaVersion int32 = 16
	// consumptionApplicationID identifies the App Store application in consumption repository tests.
	consumptionApplicationID = "apple-application-1"
)

// TestAppleConsumptionSettingsUseOptimisticRevisions verifies create, update, identical retry, and stale writes.
func TestAppleConsumptionSettingsUseOptimisticRevisions(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	seedConsumptionApplication(t, database, fixture)
	store := openRepositoryStore(t, database.ctx)
	projectID, applicationID := core.ProjectID(fixture.projectID), core.ApplicationID(consumptionApplicationID)

	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, loadErr := repository.AppleConsumptionSettings(database.ctx, projectID, applicationID)
		return loadErr
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("AppleConsumptionSettings(new) error = %v, want ErrNotFound", err)
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, putErr := repository.PutAppleConsumptionSettings(database.ctx, persistence.AppleConsumptionSettingsWrite{
			ProjectID: projectID, ApplicationID: applicationID, RefundPreference: "none", ExpectedRevision: 3,
		})
		return putErr
	}); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("update of missing settings error = %v, want ErrConflict", err)
	}
	write := persistence.AppleConsumptionSettingsWrite{
		ProjectID: projectID, ApplicationID: applicationID, Enabled: true,
		RefundPreference:      persistence.ConsumptionRefundPreferenceDecline,
		SampleContentProvided: true, ActorKeyID: "key-1",
	}
	created := putConsumptionSettings(t, database, store, write)
	if created.Revision != 1 || !created.Enabled || created.UpdatedByKeyID != "key-1" || created.RefundPreference != "decline" {
		t.Fatalf("created settings = %#v", created)
	}
	if replayed := putConsumptionSettings(t, database, store, write); replayed.Revision != 1 {
		t.Fatalf("identical create retry = %#v", replayed)
	}
	write.Enabled = false
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, putErr := repository.PutAppleConsumptionSettings(database.ctx, write)
		return putErr
	}); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("different create error = %v, want ErrConflict", err)
	}
	write.ExpectedRevision = 1
	updated := putConsumptionSettings(t, database, store, write)
	if updated.Revision != 2 || updated.Enabled {
		t.Fatalf("updated settings = %#v", updated)
	}
	if replayed := putConsumptionSettings(t, database, store, write); replayed.Revision != 2 {
		t.Fatalf("identical update retry = %#v", replayed)
	}
	write.RefundPreference = persistence.ConsumptionRefundPreferenceGrantFull
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, putErr := repository.PutAppleConsumptionSettings(database.ctx, write)
		return putErr
	}); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}
	expectConstraint(t, database, "apple_consumption_settings_refund_preference_valid", `
		UPDATE apple_consumption_settings SET refund_preference = 'always' WHERE application_id = $1
	`, consumptionApplicationID)
}

// TestAppleConsumptionConsentKeepsTheNewestDecision verifies consent upserts and customer scope.
func TestAppleConsumptionConsentKeepsTheNewestDecision(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	seedConsumptionApplication(t, database, fixture)
	store := openRepositoryStore(t, database.ctx)
	write := persistence.AppleConsumptionConsentWrite{
		ProjectID: core.ProjectID(fixture.projectID), ApplicationID: consumptionApplicationID,
		CustomerID: core.CustomerID(fixture.customerID), Consented: true, ActorKeyID: "application-key-1",
	}
	for _, consented := range []bool{true, false} {
		write.Consented = consented
		if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
			consent, putErr := repository.PutAppleConsumptionConsent(database.ctx, write)
			if putErr == nil && consent.Consented != consented {
				return errors.New("stored consent does not match the write")
			}
			return putErr
		}); err != nil {
			t.Fatalf("PutAppleConsumptionConsent(%t) error = %v", consented, err)
		}
	}
	var consent persistence.AppleConsumptionConsent
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		consent, loadErr = repository.AppleConsumptionConsent(database.ctx, write.ProjectID, write.ApplicationID, write.CustomerID)
		return loadErr
	}); err != nil || consent.Consented || consent.ActorKeyID != "application-key-1" {
		t.Fatalf("AppleConsumptionConsent() = (%#v, %v)", consent, err)
	}
	write.CustomerID = "customer-missing"
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, putErr := repository.PutAppleConsumptionConsent(database.ctx, write)
		return putErr
	}); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("consent for missing customer error = %v, want ErrConflict", err)
	}
}

// TestAppleConsumptionConsentFollowsMerges verifies a decision recorded before a merge still counts.
func TestAppleConsumptionConsentFollowsMerges(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	seedConsumptionApplication(t, database, fixture)
	store := openRepositoryStore(t, database.ctx)
	mustExec(t, database, `INSERT INTO customers (id, project_id, external_id) VALUES ('customer-source', $1, 'source-external')`,
		fixture.projectID)
	write := persistence.AppleConsumptionConsentWrite{
		ProjectID: core.ProjectID(fixture.projectID), ApplicationID: consumptionApplicationID,
		CustomerID: "customer-source", Consented: true, ActorKeyID: "application-key-1",
	}
	loadCanonical := func() persistence.AppleConsumptionConsent {
		t.Helper()
		var consent persistence.AppleConsumptionConsent
		if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
			var loadErr error
			consent, loadErr = repository.AppleConsumptionConsent(database.ctx, write.ProjectID, write.ApplicationID,
				core.CustomerID(fixture.customerID))
			return loadErr
		}); err != nil {
			t.Fatalf("AppleConsumptionConsent(canonical) error = %v", err)
		}
		return consent
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, putErr := repository.PutAppleConsumptionConsent(database.ctx, write)
		return putErr
	}); err != nil {
		t.Fatalf("PutAppleConsumptionConsent(source) error = %v", err)
	}
	mustExec(t, database, `UPDATE customers SET merged_into_customer_id = $1 WHERE id = 'customer-source'`, fixture.customerID)
	if consent := loadCanonical(); !consent.Consented || consent.CustomerID != "customer-source" {
		t.Fatalf("consent after merge = %#v", consent)
	}
	write.CustomerID, write.Consented = core.CustomerID(fixture.customerID), false
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, putErr := repository.PutAppleConsumptionConsent(database.ctx, write)
		return putErr
	}); err != nil {
		t.Fatalf("PutAppleConsumptionConsent(canonical) error = %v", err)
	}
	if consent := loadCanonical(); consent.Consented {
		t.Fatalf("newest canonical decision = %#v, want the withdrawal", consent)
	}
}

// TestAppleConsumptionRequestLifecycle verifies job enqueue, audited attempts, terminal states, listing, and retention.
func TestAppleConsumptionRequestLifecycle(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	seedConsumptionApplication(t, database, fixture)
	store := openRepositoryStore(t, database.ctx)
	signedAt := time.Date(2026, time.November, 18, 8, 0, 0, 0, time.UTC)
	sent := newConsumptionRequest(fixture, "consumption-sent", signedAt, 1)
	rejected := newConsumptionRequest(fixture, "consumption-rejected", signedAt.Add(time.Minute), 2)
	exhausted := newConsumptionRequest(fixture, "consumption-exhausted", signedAt.Add(2*time.Minute), 3)
	exhausted.CustomerID = ""
	for _, request := range []persistence.AppleConsumptionRequest{sent, sent, rejected, exhausted} {
		if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
			_, saveErr := repository.SaveAppleConsumptionRequest(database.ctx, request)
			return saveErr
		}); err != nil {
			t.Fatalf("SaveAppleConsumptionRequest(%s) error = %v", request.ID, err)
		}
	}
	var jobCount, maxAttempts int
	var queue string
	var encodedArgs []byte
	if err := database.conn.QueryRow(database.ctx, `
		SELECT count(*) OVER (), job.queue, job.max_attempts, job.args
		FROM river_job AS job JOIN apple_consumption_requests AS request ON request.river_job_id = job.id
		WHERE request.id = $1
	`, sent.ID).Scan(&jobCount, &queue, &maxAttempts, &encodedArgs); err != nil {
		t.Fatalf("load consumption River job: %v", err)
	}
	var args jobs.AppleConsumptionArgs
	if err := json.Unmarshal(encodedArgs, &args); err != nil || args.RequestID != sent.ID || !args.DeadlineAt.Equal(sent.DeadlineAt) {
		t.Fatalf("River args = (%s, %v)", encodedArgs, err)
	}
	if queue != jobs.QueueAppleConsumption || maxAttempts != jobs.AppleConsumptionMaxAttempts {
		t.Fatalf("River job = (%q, %d)", queue, maxAttempts)
	}
	var totalJobs int
	if err := database.conn.QueryRow(database.ctx, `SELECT count(*) FROM river_job WHERE kind = $1`, jobs.KindAppleConsumption).Scan(&totalJobs); err != nil || totalJobs != 3 {
		t.Fatalf("consumption River jobs = (%d, %v), want 3", totalJobs, err)
	}

	attemptAt := signedAt.Add(2 * time.Minute)
	recordConsumptionAttempt(t, database, store, persistence.AppleConsumptionAttempt{
		RequestID: sent.ID, AttemptedAt: attemptAt, Outcome: persistence.ConsumptionAttemptTransientError,
		Reason: "provider_temporary", HTTPStatus: http.StatusServiceUnavailable,
		RequestBody: json.RawMessage(`{"customerConsented":true,"deliveryStatus":"DELIVERED","sampleContentProvided":false}`),
	}, true)
	recordConsumptionAttempt(t, database, store, persistence.AppleConsumptionAttempt{
		RequestID: sent.ID, AttemptedAt: attemptAt.Add(time.Minute), Outcome: persistence.ConsumptionAttemptSent,
		Reason: "accepted", HTTPStatus: http.StatusAccepted, Status: persistence.ConsumptionStatusSent,
		RequestBody: json.RawMessage(`{"customerConsented":true,"deliveryStatus":"DELIVERED","sampleContentProvided":false}`),
	}, true)
	recordConsumptionAttempt(t, database, store, persistence.AppleConsumptionAttempt{
		RequestID: sent.ID, AttemptedAt: attemptAt.Add(2 * time.Minute), Outcome: persistence.ConsumptionAttemptSkipped,
		Reason: "disabled", Status: persistence.ConsumptionStatusSkipped,
	}, false)
	completeOrFailQueue(t, database, store, persistence.QueueCompletion{
		Queue: persistence.QueueAppleConsumption, ID: sent.ID, CompletedAt: attemptAt.Add(time.Minute),
	}, nil)

	recordConsumptionAttempt(t, database, store, persistence.AppleConsumptionAttempt{
		RequestID: rejected.ID, AttemptedAt: attemptAt, Outcome: persistence.ConsumptionAttemptPermanentError,
		Reason: "apple_rejected", HTTPStatus: http.StatusBadRequest, AppleErrorCode: 4000006,
		Status:      persistence.ConsumptionStatusFailed,
		RequestBody: json.RawMessage(`{"customerConsented":true,"deliveryStatus":"DELIVERED","sampleContentProvided":false}`),
	}, true)
	completeOrFailQueue(t, database, store, persistence.QueueCompletion{}, &persistence.QueueFailure{
		Queue: persistence.QueueAppleConsumption, ID: rejected.ID, FailedAt: attemptAt, ErrorCode: "apple_rejected",
	})
	completeOrFailQueue(t, database, store, persistence.QueueCompletion{}, &persistence.QueueFailure{
		Queue: persistence.QueueAppleConsumption, ID: exhausted.ID, FailedAt: attemptAt, ErrorCode: "provider_temporary",
	})

	var message persistence.QueueMessage
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		message, loadErr = repository.QueueMessage(database.ctx, persistence.QueueAppleConsumption, sent.ID)
		return loadErr
	}); err != nil || !message.Completed || message.Failed || message.Provider != core.ProviderAppleAppStore ||
		message.CustomerID != sent.CustomerID {
		t.Fatalf("QueueMessage() = (%#v, %v)", message, err)
	}
	var summaries []persistence.AppleConsumptionRequestSummary
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var listErr error
		summaries, listErr = repository.RecentAppleConsumptionRequests(database.ctx, sent.ProjectID, sent.ApplicationID, 20)
		return listErr
	}); err != nil || len(summaries) != 3 {
		t.Fatalf("RecentAppleConsumptionRequests() = (%#v, %v)", summaries, err)
	}
	byID := map[string]persistence.AppleConsumptionRequestSummary{}
	for _, summary := range summaries {
		byID[summary.ID] = summary
	}
	if got := byID[sent.ID]; got.Status != "sent" || got.StatusReason != "accepted" || got.AttemptCount != 2 ||
		got.LastHTTPStatus != http.StatusAccepted || got.CompletedAt == nil || got.CustomerReason != "unintended_purchase" {
		t.Fatalf("sent summary = %#v", got)
	}
	if got := byID[rejected.ID]; got.Status != "failed" || got.StatusReason != "apple_rejected" ||
		got.LastHTTPStatus != http.StatusBadRequest || got.LastAppleErrorCode != 4000006 {
		t.Fatalf("rejected summary = %#v", got)
	}
	if got := byID[exhausted.ID]; got.Status != "failed" || got.StatusReason != "provider_temporary" ||
		got.AttemptCount != 0 || got.CustomerID != "" {
		t.Fatalf("exhausted summary = %#v", got)
	}
	if summaries[0].ID != exhausted.ID {
		t.Fatalf("newest summary = %q, want %q", summaries[0].ID, exhausted.ID)
	}
	var attempts int
	if err := database.conn.QueryRow(database.ctx, `SELECT count(*) FROM apple_consumption_attempts`).Scan(&attempts); err != nil || attempts != 3 {
		t.Fatalf("attempt rows = (%d, %v), want 3", attempts, err)
	}

	mustExec(t, database, `DELETE FROM river_job WHERE kind = $1`, jobs.KindAppleConsumption)
	var deleted int64
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var purgeErr error
		deleted, purgeErr = repository.PurgeTerminalQueueRecords(database.ctx, attemptAt.Add(time.Hour), 100)
		return purgeErr
	}); err != nil || deleted != 3 {
		t.Fatalf("PurgeTerminalQueueRecords() = (%d, %v), want 3", deleted, err)
	}
	if err := database.conn.QueryRow(database.ctx, `SELECT count(*) FROM apple_consumption_attempts`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("attempt rows after purge = (%d, %v), want 0", attempts, err)
	}
}

// TestDeliveredPurchaseCustomersRequiresAllowedObservation verifies the delivery evidence lookup.
func TestDeliveredPurchaseCustomersRequiresAllowedObservation(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	writes := newPurchaseWrites(t, fixture)
	persistPurchaseWrites(t, database.ctx, store, writes)
	lookup := persistence.ProviderReferenceLookup{
		ProjectID: core.ProjectID(fixture.projectID), ApplicationID: core.ApplicationID(fixture.applicationID),
		Role: core.ReferenceTransaction, Kind: "order_id", Fingerprint: sha256.Sum256([]byte("sensitive-order-id")),
	}
	owners := deliveredCustomers(t, database, store, lookup)
	if len(owners) != 1 || owners[0] != core.CustomerID(fixture.customerID) {
		t.Fatalf("DeliveredPurchaseCustomers() = %#v", owners)
	}
	lookup.Fingerprint = sha256.Sum256([]byte("another-order"))
	if owners := deliveredCustomers(t, database, store, lookup); len(owners) != 0 {
		t.Fatalf("DeliveredPurchaseCustomers(unknown) = %#v", owners)
	}
	lookup.Fingerprint = sha256.Sum256([]byte("sensitive-order-id"))
	mustExec(t, database, `
		UPDATE purchase_observations SET access_status = 'unresolved', access_reason = 'pending_payment', lifecycle_state = 'pending'
	`)
	if owners := deliveredCustomers(t, database, store, lookup); len(owners) != 0 {
		t.Fatalf("DeliveredPurchaseCustomers(unresolved) = %#v", owners)
	}
}

// TestAppleConsumptionMigrationRefusesDirtyDown verifies a pending request blocks the down migration.
func TestAppleConsumptionMigrationRefusesDirtyDown(t *testing.T) {
	database := openTestDatabase(t, appleConsumptionSchemaVersion)
	fixture := seedCatalog(t, database)
	seedConsumptionApplication(t, database, fixture)
	request := newConsumptionRequest(fixture, "consumption-pending", time.Date(2026, time.November, 18, 8, 0, 0, 0, time.UTC), 4)
	mustExec(t, database, `
		INSERT INTO apple_consumption_requests (
			id, project_id, application_id, provider_product_id, product_type,
			payload_ciphertext, payload_fingerprint, encryption_key_id,
			signed_at, received_at, deadline_at, available_at
		) VALUES ($1, $2, $3, 'iapstack.pro', 'consumable', 'ciphertext', $4, 'test-key', $5, $5, $6, $5)
	`, request.ID, fixture.projectID, consumptionApplicationID, request.Payload.Fingerprint[:], request.SignedAt, request.DeadlineAt)
	if err := database.migrator.MigrateTo(database.ctx, appleConsumptionSchemaVersion-1); err == nil {
		t.Fatal("MigrateTo(previous) error = nil, want refusal while a request is pending")
	}
	assertVersion(t, database, appleConsumptionSchemaVersion)
	expectConstraint(t, database, "apple_consumption_requests_terminal_valid", `
		UPDATE apple_consumption_requests SET status = 'sent' WHERE id = $1
	`, request.ID)
	expectConstraint(t, database, "apple_consumption_requests_outcome_valid", `
		UPDATE apple_consumption_requests SET processed_at = now() WHERE id = $1
	`, request.ID)
	mustExec(t, database, `
		UPDATE apple_consumption_requests SET status = 'skipped', status_reason = 'disabled', completed_at = now() WHERE id = $1
	`, request.ID)
	mustMigrateTo(t, database, appleConsumptionSchemaVersion-1)
	mustMigrateTo(t, database, appleConsumptionSchemaVersion)
}

// seedConsumptionApplication adds one App Store application to the shared catalog fixture.
func seedConsumptionApplication(t *testing.T, database *testDatabase, fixture catalogFixture) {
	t.Helper()
	mustExec(t, database, `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ($1, $2, 'apple_app_store', 'sandbox', 'com.example.iapstack')
	`, consumptionApplicationID, fixture.projectID)
}

// newConsumptionRequest builds one valid pending request for repository tests.
func newConsumptionRequest(
	fixture catalogFixture,
	id string,
	signedAt time.Time,
	mark byte,
) persistence.AppleConsumptionRequest {
	payload := repositoryProtectedValue("consumption-payload-" + string('a'+rune(mark)))
	return persistence.AppleConsumptionRequest{
		ID: id, ProjectID: core.ProjectID(fixture.projectID), ApplicationID: consumptionApplicationID,
		CustomerID: core.CustomerID(fixture.customerID), ProviderProductID: "iapstack.pro",
		ProductType: "auto_renewable_subscription", CustomerReason: "unintended_purchase", Payload: payload,
		SignedAt: signedAt, ReceivedAt: signedAt, DeadlineAt: signedAt.Add(jobs.AppleConsumptionResponseWindow),
		AvailableAt: signedAt.Add(time.Minute),
	}
}

// putConsumptionSettings saves settings or fails the test.
func putConsumptionSettings(
	t *testing.T,
	database *testDatabase,
	store *postgres.Store,
	write persistence.AppleConsumptionSettingsWrite,
) persistence.AppleConsumptionSettings {
	t.Helper()
	var settings persistence.AppleConsumptionSettings
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var putErr error
		settings, putErr = repository.PutAppleConsumptionSettings(database.ctx, write)
		return putErr
	}); err != nil {
		t.Fatalf("PutAppleConsumptionSettings() error = %v", err)
	}
	return settings
}

// recordConsumptionAttempt records one attempt and checks whether the request accepted it.
func recordConsumptionAttempt(
	t *testing.T,
	database *testDatabase,
	store *postgres.Store,
	attempt persistence.AppleConsumptionAttempt,
	want bool,
) {
	t.Helper()
	var recorded bool
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var recordErr error
		recorded, recordErr = repository.RecordAppleConsumptionAttempt(database.ctx, attempt)
		return recordErr
	}); err != nil || recorded != want {
		t.Fatalf("RecordAppleConsumptionAttempt(%s) = (%t, %v), want %t", attempt.Outcome, recorded, err, want)
	}
}

// completeOrFailQueue applies one worker completion or failure to a consumption request.
func completeOrFailQueue(
	t *testing.T,
	database *testDatabase,
	store *postgres.Store,
	completion persistence.QueueCompletion,
	failure *persistence.QueueFailure,
) {
	t.Helper()
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		if failure != nil {
			return repository.FailQueue(database.ctx, *failure)
		}
		return repository.CompleteQueue(database.ctx, completion)
	}); err != nil {
		t.Fatalf("queue outcome error = %v", err)
	}
}

// deliveredCustomers runs one delivery evidence lookup or fails the test.
func deliveredCustomers(
	t *testing.T,
	database *testDatabase,
	store *postgres.Store,
	lookup persistence.ProviderReferenceLookup,
) []core.CustomerID {
	t.Helper()
	var owners []core.CustomerID
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var lookupErr error
		owners, lookupErr = repository.DeliveredPurchaseCustomers(database.ctx, lookup)
		return lookupErr
	}); err != nil {
		t.Fatalf("DeliveredPurchaseCustomers() error = %v", err)
	}
	return owners
}
