package verification_test

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/verification"
)

// createdCustomerIDPattern matches the internal id a first purchase gives its new customer.
var createdCustomerIDPattern = regexp.MustCompile(`^cust-[0-9a-f]{32}$`)

// TestServiceVerifyCreatesUnknownCustomerWithPurchase verifies the first purchase creates its customer atomically.
func TestServiceVerifyCreatesUnknownCustomerWithPurchase(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	store := newFakeStore(fixture)
	store.repository.customer = core.Customer{}
	adapter := &fakeAdapter{result: fixture.result}
	service := newVerificationService(t, store, adapter, &fakeProtector{}, fixture.clock)

	first, err := service.Verify(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("Verify() error = %v, want the first purchase to create its customer", err)
	}
	created := store.repository.customer
	if !createdCustomerIDPattern.MatchString(string(first.Customer.ID)) || created.ID != first.Customer.ID ||
		created.ProjectID != verificationProjectID || created.ExternalID != verificationExternalCustomerID {
		t.Fatalf("created customer = %#v, result customer %q", created, first.Customer.ID)
	}
	for _, evidence := range store.repository.evidence {
		if evidence.CustomerID != created.ID {
			t.Fatalf("evidence customer = %q, want %q", evidence.CustomerID, created.ID)
		}
	}
	for _, observation := range store.repository.observations {
		if observation.CustomerID != created.ID {
			t.Fatalf("observation customer = %q, want %q", observation.CustomerID, created.ID)
		}
	}
	if len(first.Entitlements) != 1 {
		t.Fatalf("entitlements = %#v, want the purchased entitlement", first.Entitlements)
	}

	second, err := service.Verify(context.Background(), fixture.command)
	if err != nil || second.Customer.ID != created.ID || store.repository.customer != created {
		t.Fatalf("replayed Verify() = (%q, %v), stored %#v, want the same customer", second.Customer.ID, err, store.repository.customer)
	}
}

// TestServiceCreatesNoCustomerWhenPurchaseIsNotStored verifies every failure before commit leaves no customer.
func TestServiceCreatesNoCustomerWhenPurchaseIsNotStored(t *testing.T) {
	t.Parallel()

	providerFailure := stores.NewFailure(
		core.ProviderHuaweiAppGallery, "verify", stores.FailureTemporary, time.Minute, errors.New("provider unavailable"),
	)
	tests := []struct {
		name    string
		mutate  func(*verificationFixture, *fakeStore, *fakeAdapter)
		wantErr error
	}{
		{
			name: "unknown claimed product",
			mutate: func(fixture *verificationFixture, _ *fakeStore, _ *fakeAdapter) {
				fixture.command.ClaimedProducts = []core.ProviderProductID{"unknown-product"}
			},
			wantErr: persistence.ErrNotFound,
		},
		{
			name: "provider failure",
			mutate: func(_ *verificationFixture, _ *fakeStore, adapter *fakeAdapter) {
				adapter.err = providerFailure
			},
			wantErr: providerFailure,
		},
		{
			name: "rolled back persistence",
			mutate: func(_ *verificationFixture, store *fakeStore, _ *fakeAdapter) {
				store.repository.failOutbox = true
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newVerificationFixture(t)
			store := newFakeStore(fixture)
			store.repository.customer = core.Customer{}
			adapter := &fakeAdapter{result: fixture.result}
			test.mutate(&fixture, store, adapter)
			service := newVerificationService(t, store, adapter, &fakeProtector{}, fixture.clock)

			_, err := service.Verify(context.Background(), fixture.command)
			if err == nil || (test.wantErr != nil && !errors.Is(err, test.wantErr)) {
				t.Fatalf("Verify() error = %v, want %v", err, test.wantErr)
			}
			if store.repository.customer != (core.Customer{}) {
				t.Fatalf("customer = %#v, want none after a purchase that was not stored", store.repository.customer)
			}
			assertNoDurableWrites(t, store.repository)
		})
	}
}

// TestServiceStoresFirstPurchaseOnAliasAttachedBeforeCommit verifies an alias attached during verification wins.
func TestServiceStoresFirstPurchaseOnAliasAttachedBeforeCommit(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	store := newFakeStore(fixture)
	store.repository.customer = core.Customer{}
	owner := core.Customer{ID: "customer-owner", ProjectID: verificationProjectID, ExternalID: "external-owner"}
	store.prepare = func(transactionNumber int, repository *fakeTransaction) {
		if transactionNumber < 2 {
			return
		}
		repository.customer = owner
		repository.resolutions = map[string]core.Customer{verificationExternalCustomerID: owner}
	}
	service := newVerificationService(t, store, &fakeAdapter{result: fixture.result}, &fakeProtector{}, fixture.clock)

	result, err := service.Verify(context.Background(), fixture.command)
	if err != nil || result.Customer.ID != owner.ID {
		t.Fatalf("Verify() = (%q, %v), want the alias owner %q", result.Customer.ID, err, owner.ID)
	}
	if store.repository.customer != owner {
		t.Fatalf("stored customer = %#v, want no new customer beside %q", store.repository.customer, owner.ID)
	}
}

// TestServiceReconcileNeverCreatesCustomer verifies a provider signal for an unknown customer still fails.
func TestServiceReconcileNeverCreatesCustomer(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	store := newFakeStore(fixture)
	store.repository.customer = core.Customer{}
	adapter := &fakeAdapter{result: fixture.result}
	service := newVerificationService(t, store, adapter, &fakeProtector{}, fixture.clock)
	queryReference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", "purchase-token")
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	signal, err := stores.NewEvidence(
		"application/vnd.iapstack.huawei-notification-v2+json",
		[]byte(`{"event_type":"ORDER","purchase_token":"purchase-token"}`),
	)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}

	_, err = service.Reconcile(context.Background(), verification.ReconciliationCommand{
		ProjectID:           verificationProjectID,
		ApplicationID:       verificationApplicationID,
		ExternalCustomerID:  verificationExternalCustomerID,
		ExpectedProducts:    []core.ProviderProductID{verificationProviderProductID},
		ExpectedProductKind: core.ProductKindNonConsumable,
		QueryReferences:     []core.StoreReference{queryReference},
		Signal:              signal,
	})
	if !errors.Is(err, persistence.ErrNotFound) || adapter.reconcileCalls != 0 {
		t.Fatalf("Reconcile() error = %v, provider calls %d, want not found before the provider", err, adapter.reconcileCalls)
	}
	if store.repository.customer != (core.Customer{}) {
		t.Fatalf("customer = %#v, want none", store.repository.customer)
	}
}

// PutCustomer creates the single fake customer for an unknown external id or returns the known one.
func (repository *fakeTransaction) PutCustomer(ctx context.Context, customer core.Customer) (core.Customer, error) {
	if existing, err := repository.CustomerByExternalID(ctx, customer.ProjectID, customer.ExternalID); err == nil {
		return existing, nil
	}
	if repository.customer != (core.Customer{}) {
		return core.Customer{}, persistence.ErrConflict
	}
	repository.customer = customer
	return customer, nil
}
