package processing

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/googleplay"
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
	scope protection.Scope
}

// TestGooglePlayVerificationPayloadResolvesProtectedPurchaseScope verifies RTDN customer and catalog binding.
func TestGooglePlayVerificationPayloadResolvesProtectedPurchaseScope(t *testing.T) {
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
	payload, process, err := service.googlePlayVerificationPayload(context.Background(), message, notification)
	if err != nil || !process {
		t.Fatalf("googlePlayVerificationPayload() = (%#v, %t, %v)", payload, process, err)
	}
	if payload.ExternalCustomerID != "account-1" || len(payload.ClaimedProducts) != 1 ||
		payload.ClaimedProducts[0] != "iapstack.pro" {
		t.Fatalf("googlePlayVerificationPayload() = %#v", payload)
	}
	if protector.scope.Purpose != "provider_reference:query:purchase_token" ||
		protector.scope.ProjectID != message.ProjectID || protector.scope.ApplicationID != message.ApplicationID {
		t.Fatalf("protection scope = %#v", protector.scope)
	}
	if store.lookup.Role != core.ReferenceQuery || store.lookup.Kind != "purchase_token" ||
		store.lookup.Fingerprint == ([32]byte{}) {
		t.Fatalf("provider reference lookup = %#v", store.lookup)
	}
	evidence, bindings, err := payload.VerificationInputs(core.ProviderGooglePlay)
	if err != nil {
		t.Fatalf("VerificationInputs() error = %v", err)
	}
	if evidence.ContentType != googleplay.EvidenceContentType || len(bindings) != 1 ||
		bindings[0].Value() != "account-1" {
		t.Fatalf("VerificationInputs() = (%#v, %#v)", evidence, bindings)
	}
}

// TestGooglePlayVerificationPayloadRetriesUnknownPurchaseAndRejectsScopeMismatch verifies safe worker classification.
func TestGooglePlayVerificationPayloadRetriesUnknownPurchaseAndRejectsScopeMismatch(t *testing.T) {
	t.Parallel()

	notification := googleplay.NotificationEnvelope{
		MessageID: "pubsub-message-1", Kind: googleplay.NotificationKindOneTimeProduct,
		NotificationType: 1, EventTime: time.Date(2026, time.August, 25, 15, 0, 0, 0, time.UTC),
		PurchaseToken: "purchase-token-1", ProductKind: core.ProductKindNonConsumable,
		ProviderProductID: "iapstack.pro",
	}
	message := persistence.QueueMessage{ProjectID: "project-1", ApplicationID: "application-1"}
	missingStore := &googleLookupStore{err: persistence.ErrNotFound}
	service := &Service{store: missingStore, protection: &processingProtection{}}
	_, _, err := service.googlePlayVerificationPayload(context.Background(), message, notification)
	var failure *stores.Failure
	if !errors.As(err, &failure) || !failure.Retryable() {
		t.Fatalf("unknown purchase error = %#v", err)
	}

	mismatchStore := &googleLookupStore{purchase: persistence.PurchaseReferenceContext{
		Customer:          core.Customer{ID: "customer-1", ProjectID: "project-1", ExternalID: "account-1"},
		ProviderProductID: "different.product", ProductKind: core.ProductKindNonConsumable,
	}}
	service = &Service{store: mismatchStore, protection: &processingProtection{}}
	_, _, err = service.googlePlayVerificationPayload(context.Background(), message, notification)
	if !errors.As(err, &failure) || failure.Kind != stores.FailureInvalidEvidence || failure.Retryable() {
		t.Fatalf("scope mismatch error = %#v", err)
	}
}

// Operate executes one provider reference lookup callback against deterministic state.
func (store *googleLookupStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	return operation(&googleLookupTransaction{store: store})
}

// PurchaseContextByReference records the protected lookup and returns its configured result.
func (transaction *googleLookupTransaction) PurchaseContextByReference(
	_ context.Context,
	lookup persistence.ProviderReferenceLookup,
) (persistence.PurchaseReferenceContext, error) {
	transaction.store.lookup = lookup
	return transaction.store.purchase, transaction.store.err
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
	fingerprint := sha256.Sum256(request.Bytes())
	return protection.Value{Ciphertext: []byte{1}, Fingerprint: fingerprint, KeyID: "test-key"}, nil
}

// Open is unused because these tests exercise lookup preparation after protected inbox decoding.
func (*processingProtection) Open(context.Context, protection.OpenRequest) ([]byte, error) {
	return nil, errors.New("not implemented")
}
