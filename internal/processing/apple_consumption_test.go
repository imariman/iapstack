package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/apple"
)

const (
	// consumptionTestTransactionID is the protected App Store transaction a test request names.
	consumptionTestTransactionID = "2000000923456790"
	// consumptionTestAccountToken is the app account token and external customer id in tests.
	consumptionTestAccountToken = "4f753b4a-32c1-4fe4-a8e6-06efb0b35db7"
	// consumptionTestNotificationUUID identifies one CONSUMPTION_REQUEST delivery in tests.
	consumptionTestNotificationUUID = "018f59d0-a200-7000-8000-000000000099"
)

// consumptionStore holds the deterministic state one consumption handler test reads and writes.
type consumptionStore struct {
	application    core.Application
	settings       *persistence.AppleConsumptionSettings
	customer       *core.Customer
	consent        *persistence.AppleConsumptionConsent
	owners         []core.CustomerID
	closure        map[core.CustomerID]bool
	record         persistence.AppleConsumptionRequestRecord
	saved          []persistence.AppleConsumptionRequest
	attempts       []persistence.AppleConsumptionAttempt
	lookups        []persistence.ProviderReferenceLookup
	recordAttempts error
}

// consumptionTransaction implements the consumption repository test port.
type consumptionTransaction struct {
	persistence.OperationsTransaction
	store *consumptionStore
}

// consumptionProtection protects values as plaintext with purpose-scoped fingerprints.
type consumptionProtection struct{}

// fakeConsumptionSender records consumption bodies instead of calling Apple.
type fakeConsumptionSender struct {
	response      apple.ConsumptionResponse
	err           error
	transactionID string
	requests      []apple.ConsumptionRequest
}

// TestHandleAppleConsumptionSendsWhenConsentAndDeliveryAreEstablished verifies the success path and its audit row.
func TestHandleAppleConsumptionSendsWhenConsentAndDeliveryAreEstablished(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 9, 0, 0, 0, time.UTC)
	store := newConsumptionStore(t, now, false)
	store.consent = &persistence.AppleConsumptionConsent{Consented: true}
	store.settings.RefundPreference = persistence.ConsumptionRefundPreferenceGrantProrated
	store.settings.SampleContentProvided = true
	sender := &fakeConsumptionSender{response: apple.ConsumptionResponse{HTTPStatus: http.StatusAccepted}}
	service := newConsumptionService(store, sender, now)

	if err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store)); err != nil {
		t.Fatalf("HandleAppleConsumption() error = %v", err)
	}
	if sender.transactionID != consumptionTestTransactionID || len(sender.requests) != 1 {
		t.Fatalf("sender = %#v", sender)
	}
	if got := sender.requests[0]; got != (apple.ConsumptionRequest{
		CustomerConsented: true, DeliveryStatus: apple.DeliveryStatusDelivered,
		RefundPreference: apple.RefundPreferenceGrantProrated, SampleContentProvided: true,
	}) {
		t.Fatalf("request = %#v", got)
	}
	attempt := store.onlyAttempt(t)
	if attempt.Outcome != persistence.ConsumptionAttemptSent || attempt.Status != persistence.ConsumptionStatusSent ||
		attempt.Reason != "accepted" || attempt.HTTPStatus != http.StatusAccepted || !attempt.AttemptedAt.Equal(now) {
		t.Fatalf("attempt = %#v", attempt)
	}
	if !bytes.Equal(attempt.RequestBody, []byte(`{"customerConsented":true,"deliveryStatus":"DELIVERED","refundPreference":"GRANT_PRORATED","sampleContentProvided":true}`)) {
		t.Fatalf("audited body = %s", attempt.RequestBody)
	}
	if bytes.Contains(attempt.RequestBody, []byte(consumptionTestTransactionID)) || bytes.Contains(attempt.RequestBody, []byte(consumptionTestAccountToken)) {
		t.Fatal("audited body contains an identifier")
	}
	if len(store.lookups) != 1 || store.lookups[0].Role != core.ReferenceTransaction || store.lookups[0].Kind != "transaction_id" ||
		store.lookups[0].Fingerprint != consumptionFingerprint("provider_reference:transaction:transaction_id", []byte(consumptionTestTransactionID)) {
		t.Fatalf("delivery lookup = %#v", store.lookups)
	}
}

// TestHandleAppleConsumptionSkipsWithoutEstablishedFacts verifies every skip reason and that Apple is never called.
func TestHandleAppleConsumptionSkipsWithoutEstablishedFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*consumptionStore)
		reason string
	}{
		{name: "never configured", reason: "disabled", mutate: func(store *consumptionStore) { store.settings = nil }},
		{name: "disabled", reason: "disabled", mutate: func(store *consumptionStore) { store.settings.Enabled = false }},
		{name: "disabled never opens the payload", reason: "disabled", mutate: func(store *consumptionStore) {
			store.settings.Enabled = false
			store.record.Payload.Fingerprint = [32]byte{1}
		}},
		{name: "no consent record", reason: "no_consent", mutate: func(*consumptionStore) {}},
		{name: "unknown customer", reason: "no_consent", mutate: func(store *consumptionStore) { store.customer = nil }},
		{name: "recorded no", reason: "consent_withdrawn", mutate: func(store *consumptionStore) {
			store.consent = &persistence.AppleConsumptionConsent{Consented: false}
		}},
		{name: "no delivery evidence", reason: "no_delivery_evidence", mutate: func(store *consumptionStore) {
			store.consent = &persistence.AppleConsumptionConsent{Consented: true}
			store.owners = nil
		}},
		{name: "another customer's purchase", reason: "no_delivery_evidence", mutate: func(store *consumptionStore) {
			store.consent = &persistence.AppleConsumptionConsent{Consented: true}
			store.owners = []core.CustomerID{"customer-other"}
		}},
		{name: "family shared", reason: "family_shared", mutate: func(store *consumptionStore) {
			store.setPayload(t, apple.ConsumptionOwnershipFamilyShared)
		}},
		{name: "assigned seat", reason: "unsupported_ownership", mutate: func(store *consumptionStore) {
			store.setPayload(t, apple.ConsumptionOwnershipOther)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := newConsumptionStore(t, now, false)
			test.mutate(store)
			sender := &fakeConsumptionSender{response: apple.ConsumptionResponse{HTTPStatus: http.StatusAccepted}}
			// Two hours after receipt, missing delivery evidence is no longer waited for.
			service := newConsumptionService(store, sender, now.Add(2*time.Hour))
			if err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store)); err != nil {
				t.Fatalf("HandleAppleConsumption() error = %v", err)
			}
			if len(sender.requests) != 0 {
				t.Fatalf("Apple received %d requests, want 0", len(sender.requests))
			}
			attempt := store.onlyAttempt(t)
			if attempt.Outcome != persistence.ConsumptionAttemptSkipped || attempt.Status != persistence.ConsumptionStatusSkipped ||
				attempt.Reason != test.reason || attempt.RequestBody != nil {
				t.Fatalf("attempt = %#v, want skipped %s", attempt, test.reason)
			}
		})
	}
}

// TestHandleAppleConsumptionWaitsForLateDeliveryEvidence verifies missing evidence is retried for one hour.
func TestHandleAppleConsumptionWaitsForLateDeliveryEvidence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 10, 15, 0, 0, time.UTC)
	store := newConsumptionStore(t, now, true)
	store.owners = nil
	sender := &fakeConsumptionSender{response: apple.ConsumptionResponse{HTTPStatus: http.StatusAccepted}}
	service := newConsumptionService(store, sender, now.Add(30*time.Minute))
	err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store))
	var failure *consumptionFailure
	if !errors.As(err, &failure) || !failure.Retryable() || failure.CodeValue() != "awaiting_delivery_evidence" {
		t.Fatalf("HandleAppleConsumption() error = %v, want a retryable wait", err)
	}
	attempt := store.onlyAttempt(t)
	if attempt.Outcome != persistence.ConsumptionAttemptTransientError || attempt.Status != "" || len(sender.requests) != 0 {
		t.Fatalf("attempt = %#v, requests = %d", attempt, len(sender.requests))
	}

	store.attempts = nil
	store.owners = []core.CustomerID{"customer-1"}
	if err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store)); err != nil {
		t.Fatalf("HandleAppleConsumption(evidence arrived) error = %v", err)
	}
	if store.onlyAttempt(t).Status != persistence.ConsumptionStatusSent || len(sender.requests) != 1 {
		t.Fatalf("attempts after evidence = %#v", store.attempts)
	}
}

// TestHandleAppleConsumptionAcceptsMergedSourcePurchase verifies delivery evidence follows a customer merge.
func TestHandleAppleConsumptionAcceptsMergedSourcePurchase(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 10, 30, 0, 0, time.UTC)
	store := newConsumptionStore(t, now, true)
	store.owners = []core.CustomerID{"customer-source"}
	store.closure = map[core.CustomerID]bool{"customer-source": true}
	sender := &fakeConsumptionSender{response: apple.ConsumptionResponse{HTTPStatus: http.StatusAccepted}}
	service := newConsumptionService(store, sender, now)
	if err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store)); err != nil {
		t.Fatalf("HandleAppleConsumption() error = %v", err)
	}
	if store.onlyAttempt(t).Status != persistence.ConsumptionStatusSent || len(sender.requests) != 1 {
		t.Fatalf("attempts = %#v", store.attempts)
	}
}

// TestHandleAppleConsumptionExpiresAtDeadline verifies a run at the deadline neither sends nor retries.
func TestHandleAppleConsumptionExpiresAtDeadline(t *testing.T) {
	t.Parallel()

	signedAt := time.Date(2026, time.November, 18, 0, 0, 0, 0, time.UTC)
	store := newConsumptionStore(t, signedAt, true)
	sender := &fakeConsumptionSender{response: apple.ConsumptionResponse{HTTPStatus: http.StatusAccepted}}
	service := newConsumptionService(store, sender, signedAt.Add(jobs.AppleConsumptionResponseWindow))
	if err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store)); err != nil {
		t.Fatalf("HandleAppleConsumption() error = %v", err)
	}
	attempt := store.onlyAttempt(t)
	if attempt.Outcome != persistence.ConsumptionAttemptExpired || attempt.Status != persistence.ConsumptionStatusExpired ||
		attempt.Reason != "deadline_passed" || len(sender.requests) != 0 {
		t.Fatalf("attempt = %#v, requests = %d", attempt, len(sender.requests))
	}

	service = newConsumptionService(store, sender, signedAt.Add(jobs.AppleConsumptionResponseWindow-time.Second))
	store.attempts = nil
	if err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store)); err != nil {
		t.Fatalf("HandleAppleConsumption(before deadline) error = %v", err)
	}
	if store.onlyAttempt(t).Status != persistence.ConsumptionStatusSent {
		t.Fatalf("attempt before deadline = %#v", store.attempts)
	}
}

// TestHandleAppleConsumptionRetriesTransientAndFailsPermanentErrors verifies retry classification and audit rows.
func TestHandleAppleConsumptionRetriesTransientAndFailsPermanentErrors(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 11, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		response  apple.ConsumptionResponse
		err       error
		reason    string
		retryable bool
	}{
		{name: "rate limited", response: apple.ConsumptionResponse{HTTPStatus: http.StatusTooManyRequests}, err: stores.NewFailure(core.ProviderAppleAppStore, "consumption", stores.FailureRateLimited, time.Second, nil), reason: "provider_rate_limited", retryable: true},
		{name: "server error", response: apple.ConsumptionResponse{HTTPStatus: http.StatusInternalServerError}, err: stores.NewFailure(core.ProviderAppleAppStore, "consumption", stores.FailureTemporary, 0, nil), reason: "provider_temporary", retryable: true},
		{name: "network", err: stores.NewFailure(core.ProviderAppleAppStore, "http", stores.FailureTemporary, 0, nil), reason: "provider_temporary", retryable: true},
		{name: "rejected", response: apple.ConsumptionResponse{HTTPStatus: http.StatusBadRequest, AppleErrorCode: 4000006}, err: stores.NewFailure(core.ProviderAppleAppStore, "consumption", stores.FailurePermanent, 0, nil), reason: "apple_rejected"},
		{name: "unauthorized", response: apple.ConsumptionResponse{HTTPStatus: http.StatusUnauthorized}, err: stores.NewFailure(core.ProviderAppleAppStore, "consumption", stores.FailureUnauthorized, 0, nil), reason: "provider_unauthorized"},
		{name: "not found", response: apple.ConsumptionResponse{HTTPStatus: http.StatusNotFound}, err: stores.NewFailure(core.ProviderAppleAppStore, "consumption", stores.FailureNotFound, 0, nil), reason: "transaction_not_found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := newConsumptionStore(t, now, true)
			sender := &fakeConsumptionSender{response: test.response, err: test.err}
			service := newConsumptionService(store, sender, now)
			err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store))
			var failure *consumptionFailure
			if !errors.As(err, &failure) || failure.Retryable() != test.retryable || failure.CodeValue() != test.reason {
				t.Fatalf("HandleAppleConsumption() error = %#v", err)
			}
			if !errors.Is(err, test.err) {
				t.Fatal("handler error does not wrap the provider failure")
			}
			attempt := store.onlyAttempt(t)
			wantOutcome, wantStatus := persistence.ConsumptionAttemptPermanentError, persistence.ConsumptionStatusFailed
			if test.retryable {
				wantOutcome, wantStatus = persistence.ConsumptionAttemptTransientError, ""
			}
			if attempt.Outcome != wantOutcome || attempt.Status != wantStatus || attempt.Reason != test.reason ||
				attempt.HTTPStatus != test.response.HTTPStatus || attempt.AppleErrorCode != test.response.AppleErrorCode ||
				len(attempt.RequestBody) == 0 {
				t.Fatalf("attempt = %#v", attempt)
			}
		})
	}
}

// TestHandleAppleConsumptionIgnoresClosedAndForeignWork verifies replays and wrong queues do nothing.
func TestHandleAppleConsumptionIgnoresClosedAndForeignWork(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	store := newConsumptionStore(t, now, true)
	store.record.Status = persistence.ConsumptionStatusSent
	sender := &fakeConsumptionSender{}
	service := newConsumptionService(store, sender, now)
	if err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store)); err != nil {
		t.Fatalf("HandleAppleConsumption(closed) error = %v", err)
	}
	if len(store.attempts) != 0 || len(sender.requests) != 0 {
		t.Fatalf("closed request changed state: %#v", store.attempts)
	}
	if err := service.HandleAppleConsumption(context.Background(), persistence.QueueMessage{Queue: persistence.QueueInbox}); err == nil {
		t.Fatal("HandleAppleConsumption(inbox) error = nil")
	}

	store.record.Status = persistence.ConsumptionStatusPending
	service.consumption = nil
	err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store))
	var failure *consumptionFailure
	if !errors.As(err, &failure) || failure.Retryable() || failure.CodeValue() != "consumption_sender_unavailable" {
		t.Fatalf("HandleAppleConsumption(no sender) error = %v", err)
	}
}

// TestHandleAppleConsumptionReportsAttemptStorageFailure verifies an unrecorded attempt is retried by the worker.
func TestHandleAppleConsumptionReportsAttemptStorageFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 30, 0, 0, time.UTC)
	store := newConsumptionStore(t, now, true)
	store.recordAttempts = persistence.ErrUnavailable
	service := newConsumptionService(store, &fakeConsumptionSender{response: apple.ConsumptionResponse{HTTPStatus: http.StatusAccepted}}, now)
	if err := service.HandleAppleConsumption(context.Background(), consumptionMessage(store)); !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("HandleAppleConsumption() error = %v, want ErrUnavailable", err)
	}
}

// TestHandleAppleInboxSchedulesConsumptionRequest verifies the inbox stores one deadline-bound protected request.
func TestHandleAppleInboxSchedulesConsumptionRequest(t *testing.T) {
	t.Parallel()

	signedAt := time.Date(2026, time.November, 18, 8, 0, 0, 0, time.UTC)
	now := signedAt.Add(2 * time.Minute)
	store := newConsumptionStore(t, signedAt, false)
	service := newConsumptionService(store, nil, now)
	notification := apple.NotificationEnvelope{
		NotificationUUID: consumptionTestNotificationUUID, NotificationType: apple.NotificationTypeConsumptionRequest,
		SignedAt: signedAt,
		Consumption: &apple.ConsumptionSignal{
			TransactionID: consumptionTestTransactionID, ProductType: apple.ConsumptionProductConsumable,
			ProviderProductID: "coins_100", ExternalCustomerID: consumptionTestAccountToken,
			Ownership: apple.ConsumptionOwnershipPurchased, CustomerReason: "fulfillment_issue",
		},
	}
	payload, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	message := persistence.QueueMessage{
		Queue: persistence.QueueInbox, ID: "inbox-1", ProjectID: "project-1", ApplicationID: "application-1",
		Provider: core.ProviderAppleAppStore, OccurredAt: signedAt.Add(time.Minute),
		ProtectedPayload: protection.Value{Ciphertext: payload, Fingerprint: consumptionFingerprint(inboxProtectionPurpose, payload), KeyID: "key-1"},
	}
	for range 2 {
		if err := service.HandleInbox(context.Background(), message); err != nil {
			t.Fatalf("HandleInbox() error = %v", err)
		}
	}
	if len(store.saved) != 2 || store.saved[0].ID != store.saved[1].ID {
		t.Fatalf("saved requests = %#v, want one stable id", store.saved)
	}
	saved := store.saved[0]
	if saved.ProductType != apple.ConsumptionProductConsumable || saved.ProviderProductID != "coins_100" ||
		saved.CustomerReason != "fulfillment_issue" || saved.CustomerID != "customer-1" ||
		!saved.SignedAt.Equal(signedAt) || !saved.ReceivedAt.Equal(signedAt.Add(time.Minute)) ||
		!saved.DeadlineAt.Equal(signedAt.Add(12*time.Hour)) || !saved.AvailableAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("saved request = %#v", saved)
	}
	if err := saved.Validate(); err != nil {
		t.Fatalf("saved request Validate() error = %v", err)
	}
	var protected jobs.AppleConsumptionPayload
	if err := decodeStrict(saved.Payload.Ciphertext, &protected); err != nil || protected != (jobs.AppleConsumptionPayload{
		TransactionID: consumptionTestTransactionID, ExternalCustomerID: consumptionTestAccountToken,
		Ownership: apple.ConsumptionOwnershipPurchased,
	}) {
		t.Fatalf("protected payload = (%#v, %v)", protected, err)
	}

	late := newConsumptionService(store, nil, signedAt.Add(13*time.Hour))
	store.saved = nil
	if err := late.HandleInbox(context.Background(), message); err != nil {
		t.Fatalf("HandleInbox(late) error = %v", err)
	}
	if len(store.saved) != 1 || !store.saved[0].AvailableAt.Equal(signedAt.Add(13*time.Hour)) {
		t.Fatalf("late request = %#v", store.saved)
	}
}

// Operate executes one callback against the in-memory consumption state.
func (store *consumptionStore) Operate(_ context.Context, operation persistence.OperationsFunc) error {
	return operation(&consumptionTransaction{store: store})
}

// setPayload replaces the protected request payload with one ownership value.
func (store *consumptionStore) setPayload(t *testing.T, ownership string) {
	t.Helper()
	payload, err := json.Marshal(jobs.AppleConsumptionPayload{
		TransactionID: consumptionTestTransactionID, ExternalCustomerID: consumptionTestAccountToken, Ownership: ownership,
	})
	if err != nil {
		t.Fatalf("Marshal() payload error = %v", err)
	}
	store.record.Payload = protection.Value{
		Ciphertext: payload, Fingerprint: consumptionFingerprint(appleConsumptionProtectionPurpose, payload), KeyID: "key-1",
	}
}

// onlyAttempt returns the single recorded attempt or fails the test.
func (store *consumptionStore) onlyAttempt(t *testing.T) persistence.AppleConsumptionAttempt {
	t.Helper()
	if len(store.attempts) != 1 {
		t.Fatalf("attempts = %#v, want one", store.attempts)
	}
	return store.attempts[0]
}

// Application returns the configured Apple application.
func (transaction *consumptionTransaction) Application(
	_ context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) (core.Application, error) {
	if projectID != transaction.store.application.ProjectID || applicationID != transaction.store.application.ID {
		return core.Application{}, persistence.ErrNotFound
	}
	return transaction.store.application, nil
}

// AppleConsumptionSettings returns the configured settings or ErrNotFound.
func (transaction *consumptionTransaction) AppleConsumptionSettings(
	context.Context,
	core.ProjectID,
	core.ApplicationID,
) (persistence.AppleConsumptionSettings, error) {
	if transaction.store.settings == nil {
		return persistence.AppleConsumptionSettings{}, persistence.ErrNotFound
	}
	return *transaction.store.settings, nil
}

// CustomerByExternalID returns the configured customer for the test account token.
func (transaction *consumptionTransaction) CustomerByExternalID(
	_ context.Context,
	_ core.ProjectID,
	externalID string,
) (core.Customer, error) {
	if transaction.store.customer == nil || externalID != transaction.store.customer.ExternalID {
		return core.Customer{}, persistence.ErrNotFound
	}
	return *transaction.store.customer, nil
}

// AppleConsumptionConsent returns the configured consent decision or ErrNotFound.
func (transaction *consumptionTransaction) AppleConsumptionConsent(
	context.Context,
	core.ProjectID,
	core.ApplicationID,
	core.CustomerID,
) (persistence.AppleConsumptionConsent, error) {
	if transaction.store.consent == nil {
		return persistence.AppleConsumptionConsent{}, persistence.ErrNotFound
	}
	return *transaction.store.consent, nil
}

// DeliveredPurchaseCustomers records the lookup and returns the configured owners.
func (transaction *consumptionTransaction) DeliveredPurchaseCustomers(
	_ context.Context,
	lookup persistence.ProviderReferenceLookup,
) ([]core.CustomerID, error) {
	transaction.store.lookups = append(transaction.store.lookups, lookup)
	return transaction.store.owners, nil
}

// CustomerInClosure reports configured merge membership.
func (transaction *consumptionTransaction) CustomerInClosure(
	_ context.Context,
	_ core.ProjectID,
	_ core.CustomerID,
	candidate core.CustomerID,
) (bool, error) {
	return transaction.store.closure[candidate], nil
}

// AppleConsumptionRequest returns the configured request record.
func (transaction *consumptionTransaction) AppleConsumptionRequest(
	_ context.Context,
	id string,
) (persistence.AppleConsumptionRequestRecord, error) {
	if id != transaction.store.record.ID {
		return persistence.AppleConsumptionRequestRecord{}, persistence.ErrNotFound
	}
	return transaction.store.record, nil
}

// RecordAppleConsumptionAttempt validates and records one attempt.
func (transaction *consumptionTransaction) RecordAppleConsumptionAttempt(
	_ context.Context,
	attempt persistence.AppleConsumptionAttempt,
) (bool, error) {
	if transaction.store.recordAttempts != nil {
		return false, transaction.store.recordAttempts
	}
	if err := attempt.Validate(); err != nil {
		return false, err
	}
	transaction.store.attempts = append(transaction.store.attempts, attempt)
	return true, nil
}

// SaveAppleConsumptionRequest validates and records one scheduled request.
func (transaction *consumptionTransaction) SaveAppleConsumptionRequest(
	_ context.Context,
	request persistence.AppleConsumptionRequest,
) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	transaction.store.saved = append(transaction.store.saved, request)
	return request.ID, nil
}

// Protect returns the plaintext as ciphertext with a purpose-scoped fingerprint.
func (consumptionProtection) Protect(_ context.Context, request protection.Request) (protection.Value, error) {
	if err := request.Validate(); err != nil {
		return protection.Value{}, err
	}
	plaintext := request.Bytes()
	return protection.Value{
		Ciphertext: plaintext, Fingerprint: consumptionFingerprint(request.Scope.Purpose, plaintext), KeyID: "key-1",
	}, nil
}

// Open authenticates the purpose-scoped fingerprint and returns the plaintext.
func (consumptionProtection) Open(_ context.Context, request protection.OpenRequest) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if request.Value.Fingerprint != consumptionFingerprint(request.Scope.Purpose, request.Value.Ciphertext) {
		return nil, protection.ErrOpenFailed
	}
	return append([]byte(nil), request.Value.Ciphertext...), nil
}

// SendConsumptionInformation records one request and returns the configured response.
func (sender *fakeConsumptionSender) SendConsumptionInformation(
	_ context.Context,
	_ core.Application,
	transactionID string,
	request apple.ConsumptionRequest,
) (apple.ConsumptionResponse, error) {
	sender.transactionID = transactionID
	sender.requests = append(sender.requests, request)
	return sender.response, sender.err
}

// newConsumptionStore returns an enabled Apple application whose customer has delivery evidence.
// consented records the customer's yes; without it the customer has no consent record.
func newConsumptionStore(t *testing.T, signedAt time.Time, consented bool) *consumptionStore {
	t.Helper()
	store := &consumptionStore{
		application: core.Application{
			ID: "application-1", ProjectID: "project-1",
			Store: core.StoreApplication{Provider: core.ProviderAppleAppStore, Environment: core.EnvironmentSandbox, ID: "com.example.iapstack"},
		},
		settings: &persistence.AppleConsumptionSettings{
			ProjectID: "project-1", ApplicationID: "application-1", Enabled: true,
			RefundPreference: persistence.ConsumptionRefundPreferenceNone, Revision: 1,
		},
		customer: &core.Customer{ID: "customer-1", ProjectID: "project-1", ExternalID: consumptionTestAccountToken},
		owners:   []core.CustomerID{"customer-1"},
		record: persistence.AppleConsumptionRequestRecord{
			AppleConsumptionRequest: persistence.AppleConsumptionRequest{
				ID: "consumption-1", ProjectID: "project-1", ApplicationID: "application-1", CustomerID: "customer-1",
				ProviderProductID: "iapstack.pro", ProductType: apple.ConsumptionProductAutoRenewable,
				SignedAt: signedAt, ReceivedAt: signedAt, DeadlineAt: signedAt.Add(jobs.AppleConsumptionResponseWindow),
				AvailableAt: signedAt,
			},
			Status: persistence.ConsumptionStatusPending,
		},
	}
	if consented {
		store.consent = &persistence.AppleConsumptionConsent{Consented: true}
	}
	store.setPayload(t, apple.ConsumptionOwnershipPurchased)
	return store
}

// newConsumptionService builds a processing service with a fixed clock and quiet logger.
func newConsumptionService(store *consumptionStore, sender *fakeConsumptionSender, now time.Time) *Service {
	service := &Service{
		store: store, protection: consumptionProtection{}, clock: func() time.Time { return now },
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if sender != nil {
		service.consumption = sender
	}
	return service
}

// consumptionMessage returns the generic worker message for the store's request.
func consumptionMessage(store *consumptionStore) persistence.QueueMessage {
	return persistence.QueueMessage{
		Queue: persistence.QueueAppleConsumption, ID: store.record.ID, ProjectID: store.record.ProjectID,
		ApplicationID: store.record.ApplicationID, Provider: core.ProviderAppleAppStore,
		ProtectedPayload: store.record.Payload,
	}
}

// consumptionFingerprint derives the test fingerprint for one protection purpose and plaintext.
func consumptionFingerprint(purpose string, plaintext []byte) [32]byte {
	return sha256.Sum256(append([]byte(purpose+"\x00"), plaintext...))
}
