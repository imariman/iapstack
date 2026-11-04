package processing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/googleplay"
	"github.com/imariman/iapstack/internal/verification"
)

// replacementLookupStore resolves stored purchases by purchase token and customers by external id.
type replacementLookupStore struct {
	purchases   map[[sha256.Size]byte]persistence.PurchaseReferenceContext
	customers   map[string]core.Customer
	customerErr error
	lookups     int
}

// replacementLookupTransaction implements the purchase and customer lookup test ports.
type replacementLookupTransaction struct {
	persistence.OperationsTransaction
	store *replacementLookupStore
}

// fakeLinkedPurchases returns one configured provider replacement link.
type fakeLinkedPurchases struct {
	linked  stores.LinkedPurchase
	err     error
	calls   int
	command verification.LinkedPurchaseCommand
}

// TestGooglePlayReconciliationCommandBindsReplacementThroughLinkedPurchase verifies the RTDN-only binding rules.
func TestGooglePlayReconciliationCommandBindsReplacementThroughLinkedPurchase(t *testing.T) {
	t.Parallel()

	oldPurchase := persistence.PurchaseReferenceContext{
		Customer:          core.Customer{ID: "customer-1", ProjectID: "project-1", ExternalID: "account-1"},
		ProviderProductID: "premium_monthly", ProductKind: core.ProductKindSubscription,
	}
	oneTimePurchase := oldPurchase
	oneTimePurchase.ProviderProductID, oneTimePurchase.ProductKind = "premium_lifetime", core.ProductKindNonConsumable
	customers := map[string]core.Customer{
		"account-1":      {ID: "customer-1", ProjectID: "project-1", ExternalID: "account-1"},
		"account-merged": {ID: "customer-1", ProjectID: "project-1", ExternalID: "account-1"},
		"account-2":      {ID: "customer-2", ProjectID: "project-1", ExternalID: "account-2"},
	}
	for _, test := range []struct {
		name         string
		linkedToken  string
		accountID    string
		readerErr    error
		customerErr  error
		wantCustomer string
		wantFailure  stores.FailureKind
	}{
		{name: "same account", linkedToken: "old-token", accountID: "account-1", wantCustomer: "account-1"},
		{name: "merged account", linkedToken: "old-token", accountID: "account-merged", wantCustomer: "account-merged"},
		{name: "another customer", linkedToken: "old-token", accountID: "account-2", wantFailure: stores.FailureInvalidEvidence},
		{name: "unknown account", linkedToken: "old-token", accountID: "account-unknown", wantFailure: stores.FailureInvalidEvidence},
		{name: "missing account", linkedToken: "old-token", wantFailure: stores.FailureInvalidEvidence},
		{name: "replaces nothing", accountID: "account-1", wantFailure: stores.FailureTemporary},
		{name: "replaced purchase unknown", linkedToken: "missing-token", accountID: "account-1", wantFailure: stores.FailureTemporary},
		{name: "replaced one-time product", linkedToken: "one-time-token", accountID: "account-1", wantFailure: stores.FailureInvalidEvidence},
		{
			name: "provider not found", linkedToken: "old-token", accountID: "account-1",
			readerErr:   stores.NewFailure(core.ProviderGooglePlay, "query", stores.FailureNotFound, 0, nil),
			wantFailure: stores.FailureTemporary,
		},
		{
			name: "provider credentials rejected", linkedToken: "old-token", accountID: "account-1",
			readerErr:   stores.NewFailure(core.ProviderGooglePlay, "query", stores.FailureUnauthorized, 0, nil),
			wantFailure: stores.FailureUnauthorized,
		},
		{
			name: "customer storage unavailable", linkedToken: "old-token", accountID: "account-2",
			customerErr: persistence.ErrUnavailable, wantFailure: stores.FailureTemporary,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := &replacementLookupStore{
				purchases: map[[sha256.Size]byte]persistence.PurchaseReferenceContext{
					sha256.Sum256([]byte("old-token")):      oldPurchase,
					sha256.Sum256([]byte("one-time-token")): oneTimePurchase,
				},
				customers: customers, customerErr: test.customerErr,
			}
			reader := &fakeLinkedPurchases{err: test.readerErr, linked: stores.LinkedPurchase{ProductID: "premium_yearly"}}
			if test.linkedToken != "" {
				reader.linked.LinkedReferences = []core.StoreReference{
					replacementReference(t, core.ReferenceQuery, "purchase_token", test.linkedToken),
				}
			}
			if test.accountID != "" {
				reader.linked.CustomerBindings = []core.StoreReference{
					replacementReference(t, core.ReferenceCustomerBinding, "obfuscated_external_account_id", test.accountID),
				}
			}
			service := &Service{store: store, protection: &processingProtection{}, replacements: reader}
			message, notification, payload := replacementNotification(t, googleplay.NotificationKindSubscription, "new-token")

			command, process, err := service.googlePlayReconciliationCommand(context.Background(), message, notification, payload)
			if reader.calls != 1 || reader.command.ProjectID != message.ProjectID ||
				reader.command.ApplicationID != message.ApplicationID ||
				reader.command.ProductKind != core.ProductKindSubscription ||
				len(reader.command.QueryReferences) != 1 || reader.command.QueryReferences[0].Value() != "new-token" {
				t.Fatalf("linked purchase lookup = (%d, %#v)", reader.calls, reader.command)
			}
			if test.wantFailure != "" {
				var failure *stores.Failure
				if !errors.As(err, &failure) || failure.Kind != test.wantFailure || process {
					t.Fatalf("googlePlayReconciliationCommand() = (%t, %#v), want %q", process, err, test.wantFailure)
				}
				return
			}
			if err != nil || !process {
				t.Fatalf("googlePlayReconciliationCommand() = (%t, %v)", process, err)
			}
			if command.ExternalCustomerID != test.wantCustomer || len(command.ExpectedProducts) != 1 ||
				command.ExpectedProducts[0] != "premium_yearly" || command.ExpectedProductKind != core.ProductKindSubscription ||
				len(command.QueryReferences) != 1 || command.QueryReferences[0].Value() != "new-token" ||
				len(command.ExpectedCustomerBindings) != 1 ||
				command.ExpectedCustomerBindings[0].Kind != "obfuscated_external_account_id" ||
				command.ExpectedCustomerBindings[0].Value() != test.wantCustomer ||
				command.Signal.ContentType != googleplay.NotificationContentType {
				t.Fatalf("googlePlayReconciliationCommand() = %#v", command)
			}
		})
	}
}

// TestGooglePlayReconciliationCommandQueriesLinksOnlyForUnknownSubscriptions verifies the provider lookup is narrow.
// A stored token keeps its stored customer and product. An unknown token from a voided-purchase notification keeps
// the previous retry without a provider call.
func TestGooglePlayReconciliationCommandQueriesLinksOnlyForUnknownSubscriptions(t *testing.T) {
	t.Parallel()

	stored := persistence.PurchaseReferenceContext{
		Customer:          core.Customer{ID: "customer-1", ProjectID: "project-1", ExternalID: "account-1"},
		ProviderProductID: "premium_monthly", ProductKind: core.ProductKindSubscription,
	}
	store := &replacementLookupStore{purchases: map[[sha256.Size]byte]persistence.PurchaseReferenceContext{
		sha256.Sum256([]byte("new-token")): stored,
	}}
	reader := &fakeLinkedPurchases{}
	service := &Service{store: store, protection: &processingProtection{}, replacements: reader}
	message, notification, payload := replacementNotification(t, googleplay.NotificationKindSubscription, "new-token")
	command, process, err := service.googlePlayReconciliationCommand(context.Background(), message, notification, payload)
	if err != nil || !process || command.ExternalCustomerID != "account-1" || command.ExpectedProducts[0] != "premium_monthly" {
		t.Fatalf("googlePlayReconciliationCommand() stored token = (%#v, %t, %v)", command, process, err)
	}

	message, notification, payload = replacementNotification(t, googleplay.NotificationKindVoidedPurchase, "unknown-token")
	_, process, err = service.googlePlayReconciliationCommand(context.Background(), message, notification, payload)
	var failure *stores.Failure
	if !errors.As(err, &failure) || failure.Kind != stores.FailureTemporary || process {
		t.Fatalf("googlePlayReconciliationCommand() voided unknown token = (%t, %#v), want temporary", process, err)
	}
	if reader.calls != 0 || store.lookups != 2 {
		t.Fatalf("linked purchase lookups = %d, stored lookups = %d, want 0 and 2", reader.calls, store.lookups)
	}

	service.replacements = nil
	message, notification, payload = replacementNotification(t, googleplay.NotificationKindSubscription, "unknown-token")
	if _, _, err := service.googlePlayReconciliationCommand(context.Background(), message, notification, payload); err == nil {
		t.Fatal("googlePlayReconciliationCommand() without a linked purchase reader error = nil")
	}
}

// replacementNotification builds one validated Google Play RTDN for a purchase token.
func replacementNotification(
	t *testing.T,
	kind googleplay.NotificationKind,
	token string,
) (persistence.QueueMessage, googleplay.NotificationEnvelope, []byte) {
	t.Helper()
	notification := googleplay.NotificationEnvelope{
		MessageID: "pubsub-message-" + token, Kind: kind,
		EventTime:     time.Date(2026, time.November, 3, 10, 0, 0, 0, time.UTC),
		PurchaseToken: token, ProductKind: core.ProductKindSubscription,
	}
	if kind == googleplay.NotificationKindSubscription {
		notification.NotificationType = 4
	}
	payload, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	message := persistence.QueueMessage{
		Queue: persistence.QueueInbox, ProjectID: "project-1", ApplicationID: "application-1",
		Provider: core.ProviderGooglePlay,
	}
	return message, notification, payload
}

// replacementReference constructs one valid opaque provider reference.
func replacementReference(t *testing.T, role core.ReferenceRole, kind, value string) core.StoreReference {
	t.Helper()
	reference, err := core.NewStoreReference(role, kind, value)
	if err != nil {
		t.Fatalf("NewStoreReference(%q, %q) error = %v", role, kind, err)
	}
	return reference
}

// Operate executes one lookup callback against deterministic state.
func (store *replacementLookupStore) Operate(_ context.Context, operation persistence.OperationsFunc) error {
	return operation(&replacementLookupTransaction{store: store})
}

// PurchaseContextByReference returns the purchase stored for one token fingerprint.
func (transaction *replacementLookupTransaction) PurchaseContextByReference(
	_ context.Context,
	lookup persistence.ProviderReferenceLookup,
) (persistence.PurchaseReferenceContext, error) {
	transaction.store.lookups++
	if lookup.Role != core.ReferenceQuery || lookup.Kind != "purchase_token" {
		return persistence.PurchaseReferenceContext{}, errors.New("unexpected provider reference lookup")
	}
	purchase, exists := transaction.store.purchases[lookup.Fingerprint]
	if !exists {
		return persistence.PurchaseReferenceContext{}, persistence.ErrNotFound
	}
	return purchase, nil
}

// CustomerByExternalID resolves one external id to its configured canonical customer.
func (transaction *replacementLookupTransaction) CustomerByExternalID(
	_ context.Context,
	_ core.ProjectID,
	externalID string,
) (core.Customer, error) {
	if transaction.store.customerErr != nil {
		return core.Customer{}, transaction.store.customerErr
	}
	customer, exists := transaction.store.customers[externalID]
	if !exists {
		return core.Customer{}, persistence.ErrNotFound
	}
	return customer, nil
}

// LinkedPurchase records the lookup and returns the configured link or failure.
func (reader *fakeLinkedPurchases) LinkedPurchase(
	_ context.Context,
	command verification.LinkedPurchaseCommand,
) (stores.LinkedPurchase, error) {
	reader.calls++
	reader.command = command
	return reader.linked, reader.err
}
