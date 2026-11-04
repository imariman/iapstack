package stores_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/platform/metrics"
	"github.com/imariman/iapstack/internal/stores"
)

// linkedResolverAdapter reports one configured replacement link and records the forwarded request.
type linkedResolverAdapter struct {
	fakeAdapter
	linked  stores.LinkedPurchase
	err     error
	request stores.LinkedPurchaseRequest
	calls   int
}

// TestObservedAdapterForwardsLinkedPurchase verifies the production wrapper exposes the customer-free lookup.
// The verification service finds it by type assertion on the registered adapter, and production registers
// every adapter through ObservedAdapter, so a wrapper without the method hides it from the RTDN worker.
func TestObservedAdapterForwardsLinkedPurchase(t *testing.T) {
	t.Parallel()

	linkedToken, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", "old-purchase-token")
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	queryToken, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", "new-purchase-token")
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	inner := &linkedResolverAdapter{
		fakeAdapter: fakeAdapter{provider: core.ProviderGooglePlay},
		linked:      stores.LinkedPurchase{ProductID: "premium_yearly", LinkedReferences: []core.StoreReference{linkedToken}},
	}
	registry := metrics.New()
	observed, err := stores.NewObservedAdapter(inner, registry)
	if err != nil {
		t.Fatalf("NewObservedAdapter() error = %v", err)
	}
	var adapter stores.Adapter = observed
	resolver, ok := adapter.(stores.LinkedPurchaseResolver)
	if !ok {
		t.Fatal("ObservedAdapter does not expose LinkedPurchaseResolver")
	}
	request := stores.LinkedPurchaseRequest{
		ProductKind: core.ProductKindSubscription, QueryReferences: []core.StoreReference{queryToken},
	}
	linked, err := resolver.LinkedPurchase(context.Background(), request)
	if err != nil {
		t.Fatalf("LinkedPurchase() error = %v", err)
	}
	if inner.calls != 1 || len(inner.request.QueryReferences) != 1 || !inner.request.QueryReferences[0].Equal(queryToken) ||
		linked.ProductID != "premium_yearly" || len(linked.LinkedReferences) != 1 || !linked.LinkedReferences[0].Equal(linkedToken) {
		t.Fatalf("LinkedPurchase() = (%#v, %d calls, request %#v)", linked, inner.calls, inner.request)
	}

	inner.err = stores.NewFailure(core.ProviderGooglePlay, "query", stores.FailureNotFound, 0, nil)
	if _, err := resolver.LinkedPurchase(context.Background(), request); !strings.Contains(err.Error(), "not_found") {
		t.Fatalf("LinkedPurchase() provider failure = %v, want the wrapped failure", err)
	}
	assertProviderCalls(t, registry, `provider="google_play",operation="linked_purchase",outcome="success"} 1`)
	assertProviderCalls(t, registry, `provider="google_play",operation="linked_purchase",outcome="not_found"} 1`)
}

// TestObservedAdapterRejectsLinkedPurchaseForUnsupportedAdapter keeps unsupported providers explicit.
func TestObservedAdapterRejectsLinkedPurchaseForUnsupportedAdapter(t *testing.T) {
	t.Parallel()

	registry := metrics.New()
	observed, err := stores.NewObservedAdapter(fakeAdapter{provider: core.ProviderAppleAppStore}, registry)
	if err != nil {
		t.Fatalf("NewObservedAdapter() error = %v", err)
	}
	if _, err := observed.LinkedPurchase(context.Background(), stores.LinkedPurchaseRequest{}); err == nil {
		t.Fatal("LinkedPurchase() for an adapter without the contract error = nil")
	}
	assertProviderCalls(t, registry, `provider="apple_app_store",operation="linked_purchase",outcome="error"} 1`)
}

// assertProviderCalls checks one provider call counter line in the Prometheus exposition.
func assertProviderCalls(t *testing.T, registry *metrics.Registry, want string) {
	t.Helper()
	var output bytes.Buffer
	if err := registry.WritePrometheus(&output); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	if !strings.Contains(output.String(), "iapstack_provider_calls_total{"+want) {
		t.Fatalf("provider call metrics = %s, want %s", output.String(), want)
	}
}

// LinkedPurchase records the forwarded request and returns the configured link or failure.
func (adapter *linkedResolverAdapter) LinkedPurchase(
	_ context.Context,
	request stores.LinkedPurchaseRequest,
) (stores.LinkedPurchase, error) {
	adapter.calls++
	adapter.request = request
	return adapter.linked, adapter.err
}
