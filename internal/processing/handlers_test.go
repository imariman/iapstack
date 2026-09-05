package processing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/googleplay"
	"github.com/imariman/iapstack/internal/stores/huawei"
)

// googleLookupStore returns one verified purchase context for processing tests.
type googleLookupStore struct {
	purchase persistence.PurchaseReferenceContext
	lookup   persistence.ProviderReferenceLookup
	err      error
}

// googleLookupTransaction implements the provider reference lookup test port.
type googleLookupTransaction struct {
	persistence.OperationsTransaction
	store *googleLookupStore
}

// processingProtection provides deterministic scoped fingerprints for processing tests.
type processingProtection struct {
	scope     protection.Scope
	opened    []byte
	protected []byte
}

// reconciliationStore records future generations and rejects provider preparation for ordering tests.
type reconciliationStore struct {
	saved             *persistence.ReconciliationJob
	applicationCalled bool
}

// reconciliationTransaction implements the scheduling and application lookup test ports.
type reconciliationTransaction struct {
	persistence.OperationsTransaction
	store *reconciliationStore
}

// TestHuaweiReconciliationCommandResolvesProtectedPurchaseScope verifies native V2 token binding.
func TestHuaweiReconciliationCommandResolvesProtectedPurchaseScope(t *testing.T) {
	t.Parallel()

	store := &googleLookupStore{purchase: persistence.PurchaseReferenceContext{
		Customer:          core.Customer{ID: "customer-1", ProjectID: "project-1", ExternalID: "account-1"},
		ProviderProductID: "premium_monthly", ProductKind: core.ProductKindSubscription,
	}}
	protector := &processingProtection{}
	service := &Service{store: store, protection: protector}
	message := persistence.QueueMessage{
		Queue: persistence.QueueInbox, ProjectID: "project-1", ApplicationID: "application-1",
		Provider: core.ProviderHuaweiAppGallery,
	}
	notification := huawei.NotificationEnvelope{
		Version: "v2", EventType: "SUBSCRIPTION", NotifyTime: time.Now().UTC(),
		ApplicationID: "provider-app-1", NotificationType: 2,
		PurchaseToken: "purchase-token-1", ProviderProductID: "premium_monthly",
		ProductKind: core.ProductKindSubscription,
	}
	payload, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	command, err := service.huaweiReconciliationCommand(context.Background(), message, notification, payload)
	if err != nil {
		t.Fatalf("huaweiReconciliationCommand() error = %v", err)
	}
	if command.ExternalCustomerID != "account-1" || len(command.ExpectedProducts) != 1 ||
		command.ExpectedProducts[0] != "premium_monthly" || command.Signal.ContentType != huawei.NotificationContentType ||
		len(command.QueryReferences) != 1 || command.QueryReferences[0].Value() != "purchase-token-1" ||
		len(command.ExpectedCustomerBindings) != 1 || command.ExpectedCustomerBindings[0].Value() != "account-1" {
		t.Fatalf("huaweiReconciliationCommand() = %#v", command)
	}
	if protector.scope.Purpose != "provider_reference:query:purchase_token" || store.lookup.Fingerprint == ([32]byte{}) {
		t.Fatalf("lookup protection = (%#v, %#v)", protector.scope, store.lookup)
	}
}

// TestHuaweiReconciliationCommandClassifiesLookupFailures verifies unknown evidence is terminal but storage outages retry.
func TestHuaweiReconciliationCommandClassifiesLookupFailures(t *testing.T) {
	t.Parallel()

	message := persistence.QueueMessage{ProjectID: "project-1", ApplicationID: "application-1"}
	notification := huawei.NotificationEnvelope{
		PurchaseToken: "purchase-token-1", ProviderProductID: "premium_lifetime",
		ProductKind: core.ProductKindNonConsumable,
	}
	payload, _ := json.Marshal(notification)
	service := &Service{store: &googleLookupStore{err: persistence.ErrNotFound}, protection: &processingProtection{}}
	_, err := service.huaweiReconciliationCommand(context.Background(), message, notification, payload)
	var failure *stores.Failure
	if !errors.As(err, &failure) || failure.Kind != stores.FailureInvalidEvidence || failure.Retryable() {
		t.Fatalf("unknown purchase error = %#v, want permanent invalid evidence", err)
	}

	service = &Service{store: &googleLookupStore{err: persistence.ErrUnavailable}, protection: &processingProtection{}}
	_, err = service.huaweiReconciliationCommand(context.Background(), message, notification, payload)
	if !errors.As(err, &failure) || failure.Kind != stores.FailureTemporary || !failure.Retryable() {
		t.Fatalf("unavailable storage error = %#v, want retryable temporary failure", err)
	}

	store := &googleLookupStore{purchase: persistence.PurchaseReferenceContext{
		Customer:          core.Customer{ID: "customer-1", ProjectID: "project-1", ExternalID: "account-1"},
		ProviderProductID: "another-product", ProductKind: core.ProductKindNonConsumable,
	}}
	service = &Service{store: store, protection: &processingProtection{}}
	_, err = service.huaweiReconciliationCommand(context.Background(), message, notification, payload)
	if !errors.As(err, &failure) || failure.Kind != stores.FailureInvalidEvidence || failure.Retryable() {
		t.Fatalf("scope mismatch error = %#v", err)
	}
}

// TestGooglePlayReconciliationCommandResolvesProtectedPurchaseScope verifies native RTDN customer and catalog binding.
func TestGooglePlayReconciliationCommandResolvesProtectedPurchaseScope(t *testing.T) {
	t.Parallel()

	store := &googleLookupStore{purchase: persistence.PurchaseReferenceContext{
		Customer:          core.Customer{ID: "customer-1", ProjectID: "project-1", ExternalID: "account-1"},
		ProviderProductID: "iapstack.pro", ProductKind: core.ProductKindSubscription,
	}}
	protector := &processingProtection{}
	service := &Service{store: store, protection: protector}
	message := persistence.QueueMessage{
		Queue: persistence.QueueInbox, ProjectID: "project-1", ApplicationID: "application-1",
		Provider: core.ProviderGooglePlay,
	}
	notification := googleplay.NotificationEnvelope{
		MessageID: "pubsub-message-1", Kind: googleplay.NotificationKindSubscription,
		NotificationType: 2, EventTime: time.Date(2026, time.August, 25, 15, 0, 0, 0, time.UTC),
		PurchaseToken: "purchase-token-1", ProductKind: core.ProductKindSubscription,
	}
	notificationPayload, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	command, process, err := service.googlePlayReconciliationCommand(
		context.Background(), message, notification, notificationPayload,
	)
	if err != nil || !process {
		t.Fatalf("googlePlayReconciliationCommand() = (%#v, %t, %v)", command, process, err)
	}
	if command.ExternalCustomerID != "account-1" || len(command.ExpectedProducts) != 1 ||
		command.ExpectedProducts[0] != "iapstack.pro" || command.ExpectedProductKind != core.ProductKindSubscription ||
		len(command.QueryReferences) != 1 || command.QueryReferences[0].Value() != "purchase-token-1" ||
		len(command.ExpectedCustomerBindings) != 1 || command.ExpectedCustomerBindings[0].Value() != "account-1" ||
		command.Signal.ContentType != googleplay.NotificationContentType {
		t.Fatalf("googlePlayReconciliationCommand() = %#v", command)
	}
	if protector.scope.Purpose != "provider_reference:query:purchase_token" ||
		protector.scope.ProjectID != message.ProjectID || protector.scope.ApplicationID != message.ApplicationID {
		t.Fatalf("protection scope = %#v", protector.scope)
	}
	if store.lookup.Role != core.ReferenceQuery || store.lookup.Kind != "purchase_token" ||
		store.lookup.Fingerprint == ([32]byte{}) {
		t.Fatalf("provider reference lookup = %#v", store.lookup)
	}
}

// TestGooglePlayReconciliationCommandRetriesUnknownPurchaseAndRejectsScopeMismatch verifies safe worker classification.
func TestGooglePlayReconciliationCommandRetriesUnknownPurchaseAndRejectsScopeMismatch(t *testing.T) {
	t.Parallel()

	notification := googleplay.NotificationEnvelope{
		MessageID: "pubsub-message-1", Kind: googleplay.NotificationKindOneTimeProduct,
		NotificationType: 1, EventTime: time.Date(2026, time.August, 25, 15, 0, 0, 0, time.UTC),
		PurchaseToken: "purchase-token-1", ProductKind: core.ProductKindNonConsumable,
		ProviderProductID: "iapstack.pro",
	}
	message := persistence.QueueMessage{ProjectID: "project-1", ApplicationID: "application-1"}
	payload, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	missingStore := &googleLookupStore{err: persistence.ErrNotFound}
	service := &Service{store: missingStore, protection: &processingProtection{}}
	_, _, err = service.googlePlayReconciliationCommand(context.Background(), message, notification, payload)
	var failure *stores.Failure
	if !errors.As(err, &failure) || !failure.Retryable() {
		t.Fatalf("unknown purchase error = %#v", err)
	}

	mismatchStore := &googleLookupStore{purchase: persistence.PurchaseReferenceContext{
		Customer:          core.Customer{ID: "customer-1", ProjectID: "project-1", ExternalID: "account-1"},
		ProviderProductID: "different.product", ProductKind: core.ProductKindNonConsumable,
	}}
	service = &Service{store: mismatchStore, protection: &processingProtection{}}
	_, _, err = service.googlePlayReconciliationCommand(context.Background(), message, notification, payload)
	if !errors.As(err, &failure) || failure.Kind != stores.FailureInvalidEvidence || failure.Retryable() {
		t.Fatalf("scope mismatch error = %#v", err)
	}
}

// TestHandleGooglePlayInboxSkipsAuditSignalsAndRejectsMalformedPayload verifies no-op RTDNs never reach verification.
func TestHandleGooglePlayInboxSkipsAuditSignalsAndRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	service := &Service{}
	message := persistence.QueueMessage{
		ProjectID: "project-1", ApplicationID: "application-1",
		Provider: core.ProviderGooglePlay,
	}
	for _, kind := range []googleplay.NotificationKind{
		googleplay.NotificationKindTest,
		googleplay.NotificationKindPendingRefundReview,
	} {
		kind := kind
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			payload, err := json.Marshal(googleplay.NotificationEnvelope{
				MessageID: "pubsub-message-1", Kind: kind,
				EventTime: time.Date(2026, time.August, 25, 15, 0, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if err := service.handleGooglePlayInbox(context.Background(), message, payload); err != nil {
				t.Fatalf("handleGooglePlayInbox() error = %v", err)
			}
		})
	}

	if err := service.handleGooglePlayInbox(context.Background(), message, []byte(`{"unknown":true}`)); err == nil {
		t.Fatal("handleGooglePlayInbox() malformed payload error = nil")
	}
}

// TestWorkerHandlersRejectWrongQueueProviderAndPayload verifies durable job routing is fail closed.
func TestWorkerHandlersRejectWrongQueueProviderAndPayload(t *testing.T) {
	t.Parallel()

	if err := (&Service{}).HandleInbox(context.Background(), persistence.QueueMessage{
		Queue: persistence.QueueReconciliation,
	}); err == nil {
		t.Fatal("HandleInbox() wrong queue error = nil")
	}
	if err := (&Service{}).HandleReconciliation(context.Background(), persistence.QueueMessage{
		Queue: persistence.QueueInbox,
	}); err == nil {
		t.Fatal("HandleReconciliation() wrong queue error = nil")
	}

	fingerprint := sha256.Sum256([]byte("protected"))
	message := persistence.QueueMessage{
		Queue: persistence.QueueInbox, ProjectID: "project-1", ApplicationID: "application-1",
		Provider: core.Provider("unsupported"),
		ProtectedPayload: protection.Value{
			Ciphertext: []byte{1}, Fingerprint: fingerprint, KeyID: "test-key",
		},
	}
	service := &Service{protection: &processingProtection{opened: []byte(`{}`)}}
	if err := service.HandleInbox(context.Background(), message); err == nil {
		t.Fatal("HandleInbox() unsupported provider error = nil")
	}

	message.Queue = persistence.QueueReconciliation
	message.Provider = core.ProviderGooglePlay
	service.protection = &processingProtection{opened: []byte(`{"unknown":true}`)}
	if err := service.HandleReconciliation(context.Background(), message); err == nil {
		t.Fatal("HandleReconciliation() malformed payload error = nil")
	}
}

// TestReconciliationSchedulesNextGenerationBeforeProviderRefresh verifies terminal failures cannot stop the chain.
func TestReconciliationSchedulesNextGenerationBeforeProviderRefresh(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(jobs.ReconciliationPayload{
		Verification: jobs.VerificationPayload{
			ExternalCustomerID: "account-1",
			ClaimedProducts:    []core.ProviderProductID{"iapstack.pro"},
			Evidence:           json.RawMessage(`{"product_kind":"subscription"}`),
		},
		ScheduledFor: now,
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	fingerprint := sha256.Sum256(payload)
	store := &reconciliationStore{}
	protector := &processingProtection{opened: payload}
	service := &Service{
		store: store, protection: protector, clock: func() time.Time { return now },
	}
	message := persistence.QueueMessage{
		Queue: persistence.QueueReconciliation, ID: "reconcile-1",
		ProjectID: "project-1", ApplicationID: "application-1", CustomerID: "customer-1",
		ProtectedPayload: protection.Value{
			Ciphertext: []byte{1}, Fingerprint: fingerprint, KeyID: "test-key",
		},
	}

	err = service.HandleReconciliation(context.Background(), message)
	if !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("HandleReconciliation() error = %v, want unavailable", err)
	}
	if !store.applicationCalled || store.saved == nil {
		t.Fatalf("reconciliation ordering = (application=%t, saved=%#v)", store.applicationCalled, store.saved)
	}
	wantAvailableAt := nextReconciliationTime(now)
	if store.saved.CustomerID != message.CustomerID || !store.saved.AvailableAt.Equal(wantAvailableAt) {
		t.Fatalf("next reconciliation = %#v, want customer %q at %v", store.saved, message.CustomerID, wantAvailableAt)
	}
	var next jobs.ReconciliationPayload
	if err := json.Unmarshal(protector.protected, &next); err != nil {
		t.Fatalf("Unmarshal() protected payload error = %v", err)
	}
	if !next.ScheduledFor.Equal(wantAvailableAt) || next.Verification.ExternalCustomerID != "account-1" {
		t.Fatalf("next reconciliation payload = %#v", next)
	}
}

// Operate executes one provider reference lookup callback against deterministic state.
func (store *googleLookupStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	return operation(&googleLookupTransaction{store: store})
}

// Operate executes one scheduling or application lookup callback against deterministic state.
func (store *reconciliationStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	return operation(&reconciliationTransaction{store: store})
}

// PurchaseContextByReference records the protected lookup and returns its configured result.
func (transaction *googleLookupTransaction) PurchaseContextByReference(
	_ context.Context,
	lookup persistence.ProviderReferenceLookup,
) (persistence.PurchaseReferenceContext, error) {
	transaction.store.lookup = lookup
	return transaction.store.purchase, transaction.store.err
}

// SaveReconciliationJob records the next generation before provider preparation begins.
func (transaction *reconciliationTransaction) SaveReconciliationJob(
	_ context.Context,
	job persistence.ReconciliationJob,
) (string, error) {
	copy := job
	transaction.store.saved = &copy
	return job.ID, nil
}

// Application verifies scheduling already happened and simulates a retryable provider preparation failure.
func (transaction *reconciliationTransaction) Application(
	_ context.Context,
	_ core.ProjectID,
	_ core.ApplicationID,
) (core.Application, error) {
	transaction.store.applicationCalled = true
	if transaction.store.saved == nil {
		return core.Application{}, errors.New("application lookup happened before reconciliation scheduling")
	}
	return core.Application{}, persistence.ErrUnavailable
}

// Protect creates a deterministic non-empty protected value for one scoped plaintext.
func (service *processingProtection) Protect(
	_ context.Context,
	request protection.Request,
) (protection.Value, error) {
	if err := request.Validate(); err != nil {
		return protection.Value{}, err
	}
	service.scope = request.Scope
	service.protected = request.Bytes()
	fingerprint := sha256.Sum256(service.protected)
	return protection.Value{Ciphertext: []byte{1}, Fingerprint: fingerprint, KeyID: "test-key"}, nil
}

// Open returns one deterministic payload for protected worker handler tests.
func (service *processingProtection) Open(_ context.Context, request protection.OpenRequest) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return append([]byte(nil), service.opened...), nil
}
