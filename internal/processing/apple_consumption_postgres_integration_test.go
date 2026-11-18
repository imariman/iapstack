//go:build integration

package processing

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/apple"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// consumptionIntegrationApplicationID identifies the App Store application in the pipeline test.
	consumptionIntegrationApplicationID = "processing-apple-application-1"
	// consumptionIntegrationProductID identifies the internal lifetime product.
	consumptionIntegrationProductID = "processing-apple-lifetime"
	// consumptionIntegrationProviderProductID identifies the App Store product.
	consumptionIntegrationProviderProductID = "iapstack.lifetime"
	// consumptionIntegrationUndeliveredTransactionID names a transaction IAPStack never verified.
	consumptionIntegrationUndeliveredTransactionID = "2000000923456799"
)

// consumptionIntegrationAdapter returns one allowed App Store observation for the delivery evidence.
type consumptionIntegrationAdapter struct {
	result stores.VerificationResult
}

// TestAppleConsumptionPipelineWithPostgreSQL verifies inbox scheduling, consent, delivery evidence, and audit rows.
func TestAppleConsumptionPipelineWithPostgreSQL(t *testing.T) {
	database := openProcessingIntegrationDatabase(t)
	now := time.Date(2026, time.November, 18, 9, 0, 0, 0, time.UTC)
	application := core.Application{
		ID: consumptionIntegrationApplicationID, ProjectID: processingIntegrationProjectID,
		Store: core.StoreApplication{
			Provider: core.ProviderAppleAppStore, Environment: core.EnvironmentSandbox, ID: "com.example.iapstack",
		},
	}
	customer := core.Customer{
		ID: processingIntegrationCustomerID, ProjectID: processingIntegrationProjectID,
		ExternalID: consumptionTestAccountToken,
	}
	seedConsumptionIntegrationCatalog(t, database, application, customer)
	protector := processingIntegrationProtection{}
	binding := processingIntegrationReference(t, core.ReferenceCustomerBinding, "app_account_token", consumptionTestAccountToken)
	adapter := &consumptionIntegrationAdapter{result: consumptionIntegrationResult(t, application, now, binding)}
	registry, err := stores.NewRegistry(adapter)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	verificationService, err := verification.NewService(
		database.store, registry, protector, verification.NewDefaultProjector(), processingIntegrationClock{now: now},
	)
	if err != nil {
		t.Fatalf("NewService() verification error = %v", err)
	}
	if _, err := verificationService.Verify(database.ctx, verification.Command{
		ProjectID: application.ProjectID, ApplicationID: application.ID, ExternalCustomerID: consumptionTestAccountToken,
		ClaimedProducts:          []core.ProviderProductID{consumptionIntegrationProviderProductID},
		ExpectedCustomerBindings: []core.StoreReference{binding},
		Evidence: processingIntegrationEvidence(t, stores.AppleEvidenceContentType,
			[]byte(`{"signed_transaction":"client-jws","product_kind":"non_consumable"}`)),
	}); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if err := database.store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		if _, putErr := repository.PutAppleConsumptionSettings(database.ctx, persistence.AppleConsumptionSettingsWrite{
			ProjectID: application.ProjectID, ApplicationID: application.ID, Enabled: true,
			ConsentMode: persistence.ConsumptionConsentCustomerOptIn, RefundPreference: persistence.ConsumptionRefundPreferenceDecline,
			ActorKeyID: "admin-key-1",
		}); putErr != nil {
			return putErr
		}
		_, putErr := repository.PutAppleConsumptionConsent(database.ctx, persistence.AppleConsumptionConsentWrite{
			ProjectID: application.ProjectID, ApplicationID: application.ID, CustomerID: customer.ID,
			Consented: true, ActorKeyID: "application-key-1",
		})
		return putErr
	}); err != nil {
		t.Fatalf("configure consumption answers: %v", err)
	}

	processingService, err := New(database.store, protector, verificationService)
	if err != nil {
		t.Fatalf("New() processing error = %v", err)
	}
	processingService.SetClock(func() time.Time { return now.Add(time.Minute) })
	delivered := saveConsumptionIntegrationInbox(t, database, protector, application, "018f59d0-a200-7000-8000-000000000101",
		consumptionTestTransactionID, now)
	undelivered := saveConsumptionIntegrationInbox(t, database, protector, application, "018f59d0-a200-7000-8000-000000000102",
		consumptionIntegrationUndeliveredTransactionID, now)
	for _, message := range []persistence.QueueMessage{delivered, delivered, undelivered} {
		if err := processingService.HandleInbox(database.ctx, message); err != nil {
			t.Fatalf("HandleInbox() error = %v", err)
		}
	}
	assertProcessingIntegrationCount(t, database, "apple_consumption_requests", 2)

	sender := &fakeConsumptionSender{response: apple.ConsumptionResponse{HTTPStatus: http.StatusAccepted}}
	processingService.SetAppleConsumptionSender(sender)
	processingService.SetClock(func() time.Time { return now.Add(3 * time.Minute) })
	rows, err := database.conn.Query(database.ctx, `SELECT id FROM apple_consumption_requests ORDER BY id`)
	if err != nil {
		t.Fatalf("list consumption requests: %v", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan consumption request: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		var message persistence.QueueMessage
		if err := database.store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
			var loadErr error
			message, loadErr = repository.QueueMessage(database.ctx, persistence.QueueAppleConsumption, id)
			return loadErr
		}); err != nil {
			t.Fatalf("QueueMessage(%s) error = %v", id, err)
		}
		if err := processingService.HandleAppleConsumption(database.ctx, message); err != nil {
			t.Fatalf("HandleAppleConsumption(%s) error = %v", id, err)
		}
	}
	if len(sender.requests) != 1 || sender.transactionID != consumptionTestTransactionID ||
		sender.requests[0].RefundPreference != apple.RefundPreferenceDecline {
		t.Fatalf("sender = %#v", sender)
	}
	statusRows, err := database.conn.Query(database.ctx, `
		SELECT request.status, request.status_reason, request.attempt_count, attempt.outcome,
			attempt.http_status, attempt.request_body::text, request.deadline_at - request.signed_at
		FROM apple_consumption_requests AS request
		JOIN apple_consumption_attempts AS attempt ON attempt.request_id = request.id
		ORDER BY request.status
	`)
	if err != nil {
		t.Fatalf("load consumption outcomes: %v", err)
	}
	defer statusRows.Close()
	outcomes := map[string]string{}
	for statusRows.Next() {
		var status, reason, outcome string
		var attempts int
		var httpStatus *int
		var body *string
		var window time.Duration
		if err := statusRows.Scan(&status, &reason, &attempts, &outcome, &httpStatus, &body, &window); err != nil {
			t.Fatalf("scan consumption outcome: %v", err)
		}
		if attempts != 1 || window != 12*time.Hour {
			t.Fatalf("request %s attempts = %d, window = %s", status, attempts, window)
		}
		if status == persistence.ConsumptionStatusSent {
			var decoded map[string]any
			if httpStatus == nil || *httpStatus != http.StatusAccepted || body == nil || json.Unmarshal([]byte(*body), &decoded) != nil ||
				decoded["refundPreference"] != apple.RefundPreferenceDecline || decoded["customerConsented"] != true {
				t.Fatalf("sent attempt = (%v, %v)", httpStatus, body)
			}
		}
		outcomes[status] = reason + "/" + outcome
	}
	if outcomes[persistence.ConsumptionStatusSent] != "accepted/sent" ||
		outcomes[persistence.ConsumptionStatusSkipped] != "no_delivery_evidence/skipped" {
		t.Fatalf("outcomes = %#v", outcomes)
	}
}

// Provider identifies the App Store test adapter.
func (adapter *consumptionIntegrationAdapter) Provider() core.Provider {
	return core.ProviderAppleAppStore
}

// Verify returns the configured allowed lifetime purchase.
func (adapter *consumptionIntegrationAdapter) Verify(
	context.Context,
	stores.VerificationRequest,
) (stores.VerificationResult, error) {
	return adapter.result, nil
}

// Reconcile returns the configured allowed lifetime purchase.
func (adapter *consumptionIntegrationAdapter) Reconcile(
	context.Context,
	stores.ReconciliationRequest,
) (stores.VerificationResult, error) {
	return adapter.result, nil
}

// consumptionIntegrationResult builds an allowed App Store observation carrying the consumption transaction.
func consumptionIntegrationResult(
	t *testing.T,
	application core.Application,
	observedAt time.Time,
	binding core.StoreReference,
) stores.VerificationResult {
	t.Helper()
	observation := core.PurchaseObservation{
		ID: "processing-apple-observation-1", ApplicationID: application.ID, Store: application.Store,
		ProductID: consumptionIntegrationProviderProductID, ProductKind: core.ProductKindNonConsumable,
		State: core.LifecycleActive, ProviderState: "active", Access: core.AccessAllowed,
		AccessReason: core.AccessReasonPurchaseValid, Ownership: core.OwnershipPurchased, Quantity: 1,
		OccurredAt: observedAt.Add(-time.Hour), ObservedAt: observedAt,
		EffectivePeriod: core.EffectivePeriod{StartsAt: observedAt.Add(-time.Hour)},
		Renewal:         core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable},
		References: []core.StoreReference{
			processingIntegrationReference(t, core.ReferenceTransaction, "transaction_id", consumptionTestTransactionID),
			processingIntegrationReference(t, core.ReferenceLineage, "original_transaction_id", consumptionTestTransactionID),
			processingIntegrationReference(t, core.ReferenceQuery, "transaction_id", consumptionTestTransactionID),
			binding,
		},
	}
	return stores.VerificationResult{
		VerifiedAt: observedAt,
		Artifacts: []stores.VerifiedArtifact{{
			Kind: "apple_server_transaction",
			Evidence: processingIntegrationEvidence(t, "application/json",
				[]byte(`{"signedTransactionInfo":"server-jws"}`)),
		}},
		Observations: []core.PurchaseObservation{observation},
	}
}

// seedConsumptionIntegrationCatalog creates an App Store application with one lifetime product.
func seedConsumptionIntegrationCatalog(
	t *testing.T,
	database *processingIntegrationDatabase,
	application core.Application,
	customer core.Customer,
) {
	t.Helper()
	err := database.store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		if err := repository.PutProject(database.ctx, application.ProjectID); err != nil {
			return err
		}
		if err := repository.PutApplication(database.ctx, application); err != nil {
			return err
		}
		if _, err := repository.PutCustomer(database.ctx, customer); err != nil {
			return err
		}
		if err := repository.PutEntitlementDefinition(database.ctx, core.Entitlement{
			ID: processingIntegrationEntitlementID, ProjectID: application.ProjectID, Key: "premium",
		}); err != nil {
			return err
		}
		if err := repository.PutProduct(database.ctx, core.Product{
			ID: consumptionIntegrationProductID, ProjectID: application.ProjectID, Kind: core.ProductKindNonConsumable,
			EntitlementIDs: []core.EntitlementID{processingIntegrationEntitlementID},
		}); err != nil {
			return err
		}
		return repository.PutStoreProduct(database.ctx, application.ProjectID, core.StoreProduct{
			ApplicationID: application.ID, ProductID: consumptionIntegrationProductID,
			ProviderID: consumptionIntegrationProviderProductID,
		})
	})
	if err != nil {
		t.Fatalf("seed consumption integration catalog: %v", err)
	}
}

// saveConsumptionIntegrationInbox stores one protected CONSUMPTION_REQUEST envelope and loads it as worker input.
func saveConsumptionIntegrationInbox(
	t *testing.T,
	database *processingIntegrationDatabase,
	protector protection.Service,
	application core.Application,
	notificationUUID string,
	transactionID string,
	signedAt time.Time,
) persistence.QueueMessage {
	t.Helper()
	payload, err := json.Marshal(apple.NotificationEnvelope{
		NotificationUUID: notificationUUID, NotificationType: apple.NotificationTypeConsumptionRequest, SignedAt: signedAt,
		Consumption: &apple.ConsumptionSignal{
			TransactionID: transactionID, ProductType: apple.ConsumptionProductNonConsumable,
			ProviderProductID: consumptionIntegrationProviderProductID, ExternalCustomerID: consumptionTestAccountToken,
			Ownership: apple.ConsumptionOwnershipPurchased, CustomerReason: "unsatisfied_with_purchase",
		},
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
			ID: "inbox-" + notificationUUID, ProjectID: application.ProjectID, ApplicationID: application.ID,
			Provider: application.Store.Provider, Kind: "app_store_server_notification_v2",
			ContentType: apple.NotificationContentType, Payload: protected,
			ReceivedAt: signedAt, AvailableAt: signedAt,
		})
		if saveErr != nil {
			return saveErr
		}
		message, saveErr = repository.QueueMessage(database.ctx, persistence.QueueInbox, id)
		return saveErr
	})
	if err != nil {
		t.Fatalf("save and load consumption inbox: %v", err)
	}
	return message
}
