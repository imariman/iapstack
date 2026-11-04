package verification_test

import (
	"context"
	"errors"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/verification"
)

// linkedFakeAdapter reports one configured replacement link in addition to the fake adapter behavior.
type linkedFakeAdapter struct {
	*fakeAdapter
	linked      stores.LinkedPurchase
	linkedErr   error
	linkedCalls int
	request     stores.LinkedPurchaseRequest
}

// TestServiceLinkedPurchaseReadsProviderWithoutPersisting verifies the customer-free provider lookup.
func TestServiceLinkedPurchaseReadsProviderWithoutPersisting(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	fixture.application.Store.Provider = core.ProviderGooglePlay
	store := newFakeStore(fixture)
	newToken := linkedPurchaseReference(t, core.ReferenceQuery, "purchase_token", "new-purchase-token")
	adapter := &linkedFakeAdapter{
		fakeAdapter: &fakeAdapter{provider: core.ProviderGooglePlay},
		linked: stores.LinkedPurchase{
			ProductID: verificationProviderProductID,
			LinkedReferences: []core.StoreReference{
				linkedPurchaseReference(t, core.ReferenceQuery, "purchase_token", "old-purchase-token"),
			},
			CustomerBindings: []core.StoreReference{linkedPurchaseReference(
				t, core.ReferenceCustomerBinding, "obfuscated_external_account_id", verificationExternalCustomerID,
			)},
		},
	}
	service := newVerificationService(t, store, adapter, &fakeProtector{}, fixture.clock)

	linked, err := service.LinkedPurchase(context.Background(), verification.LinkedPurchaseCommand{
		ProjectID: verificationProjectID, ApplicationID: verificationApplicationID,
		ProductKind: core.ProductKindSubscription, QueryReferences: []core.StoreReference{newToken},
	})
	if err != nil {
		t.Fatalf("LinkedPurchase() error = %v", err)
	}
	if linked.ProductID != verificationProviderProductID || len(linked.LinkedReferences) != 1 ||
		linked.LinkedReferences[0].Value() != "old-purchase-token" {
		t.Fatalf("LinkedPurchase() = %#v", linked)
	}
	if adapter.linkedCalls != 1 || adapter.request.Application != fixture.application ||
		adapter.request.ProductKind != core.ProductKindSubscription ||
		len(adapter.request.QueryReferences) != 1 || !adapter.request.QueryReferences[0].Equal(newToken) {
		t.Fatalf("provider request = (%d, %#v)", adapter.linkedCalls, adapter.request)
	}
	if adapter.calls != 0 || adapter.reconcileCalls != 0 || adapter.postCommitCalls != 0 ||
		len(store.repository.observations) != 0 || len(store.repository.evidence) != 0 {
		t.Fatalf("LinkedPurchase() stored or completed work: verify %d, reconcile %d, post-commit %d, observations %d",
			adapter.calls, adapter.reconcileCalls, adapter.postCommitCalls, len(store.repository.observations))
	}
}

// TestServiceLinkedPurchaseRejectsUnsupportedAndInvalidResults verifies fail-closed lookups.
func TestServiceLinkedPurchaseRejectsUnsupportedAndInvalidResults(t *testing.T) {
	t.Parallel()

	fixture := newVerificationFixture(t)
	fixture.application.Store.Provider = core.ProviderGooglePlay
	command := verification.LinkedPurchaseCommand{
		ProjectID: verificationProjectID, ApplicationID: verificationApplicationID,
		ProductKind: core.ProductKindSubscription,
		QueryReferences: []core.StoreReference{
			linkedPurchaseReference(t, core.ReferenceQuery, "purchase_token", "new-purchase-token"),
		},
	}

	unsupported := newVerificationService(
		t, newFakeStore(fixture), &fakeAdapter{provider: core.ProviderGooglePlay}, &fakeProtector{}, fixture.clock,
	)
	if _, err := unsupported.LinkedPurchase(context.Background(), command); err == nil {
		t.Fatal("LinkedPurchase() without provider support error = nil")
	}

	providerFailure := stores.NewFailure(core.ProviderGooglePlay, "query", stores.FailureNotFound, 0, nil)
	failing := &linkedFakeAdapter{fakeAdapter: &fakeAdapter{provider: core.ProviderGooglePlay}, linkedErr: providerFailure}
	service := newVerificationService(t, newFakeStore(fixture), failing, &fakeProtector{}, fixture.clock)
	var failure *stores.Failure
	if _, err := service.LinkedPurchase(context.Background(), command); !errors.As(err, &failure) ||
		failure.Kind != stores.FailureNotFound {
		t.Fatalf("LinkedPurchase() provider failure = %v, want classified not found", err)
	}

	invalid := &linkedFakeAdapter{
		fakeAdapter: &fakeAdapter{provider: core.ProviderGooglePlay},
		linked: stores.LinkedPurchase{
			ProductID: verificationProviderProductID,
			LinkedReferences: []core.StoreReference{
				linkedPurchaseReference(t, core.ReferenceLineage, "purchase_token", "old-purchase-token"),
			},
		},
	}
	service = newVerificationService(t, newFakeStore(fixture), invalid, &fakeProtector{}, fixture.clock)
	if _, err := service.LinkedPurchase(context.Background(), command); err == nil {
		t.Fatal("LinkedPurchase() with a lineage reference as the replaced purchase error = nil")
	}

	missing := command
	missing.ApplicationID = "application-unknown"
	valid := &linkedFakeAdapter{fakeAdapter: &fakeAdapter{provider: core.ProviderGooglePlay}}
	service = newVerificationService(t, newFakeStore(fixture), valid, &fakeProtector{}, fixture.clock)
	if _, err := service.LinkedPurchase(context.Background(), missing); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("LinkedPurchase() unknown application error = %v, want not found", err)
	}
	empty := command
	empty.QueryReferences = nil
	if _, err := service.LinkedPurchase(context.Background(), empty); err == nil {
		t.Fatal("LinkedPurchase() without query references error = nil")
	}
	if valid.linkedCalls != 0 {
		t.Fatalf("provider calls for rejected commands = %d, want 0", valid.linkedCalls)
	}
}

// linkedPurchaseReference constructs one valid opaque provider reference.
func linkedPurchaseReference(t *testing.T, role core.ReferenceRole, kind, value string) core.StoreReference {
	t.Helper()
	reference, err := core.NewStoreReference(role, kind, value)
	if err != nil {
		t.Fatalf("NewStoreReference(%q, %q) error = %v", role, kind, err)
	}
	return reference
}

// LinkedPurchase records the provider request and returns the configured link or failure.
func (adapter *linkedFakeAdapter) LinkedPurchase(
	_ context.Context,
	request stores.LinkedPurchaseRequest,
) (stores.LinkedPurchase, error) {
	adapter.linkedCalls++
	adapter.request = request
	return adapter.linked, adapter.linkedErr
}
