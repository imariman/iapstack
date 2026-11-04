package googleplay

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

// publisherPathRecorder records Android Publisher request paths before delegating to a fixture transport.
type publisherPathRecorder struct {
	next  http.RoundTripper
	paths []string
}

// TestAdapterReadsLinkedPurchaseWithoutCustomer verifies the replacement link for each documented purchase shape.
func TestAdapterReadsLinkedPurchaseWithoutCustomer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 25, 15, 0, 0, 0, time.UTC)
	immediate := completedDeferredReplacement(now, now.Add(30*24*time.Hour))
	immediateItem := immediate["lineItems"].([]map[string]any)[1]
	immediateItem["itemReplacement"] = map[string]any{
		"productId": fixtureProductID, "basePlanId": "monthly", "replacementMode": "WITH_TIME_PRORATION",
	}
	immediate["lineItems"] = []map[string]any{immediateItem}
	immediate["acknowledgementState"] = acknowledgementStatePending
	unlinked := completedDeferredReplacement(now, now.Add(30*24*time.Hour))
	unlinked["lineItems"] = []map[string]any{unlinked["lineItems"].([]map[string]any)[1]}
	delete(unlinked, "linkedPurchaseToken")
	for _, test := range []struct {
		name        string
		payload     map[string]any
		wantProduct core.ProviderProductID
		wantLinked  string
		wantNamed   int
	}{
		{
			name:    "pending deferred replacement",
			payload: pendingDeferredReplacement(now, now.Add(10*24*time.Hour)), wantProduct: fixtureProductID,
			wantLinked: "previous-purchase-token", wantNamed: 2,
		},
		{
			name:    "completed deferred replacement",
			payload: completedDeferredReplacement(now, now.Add(30*24*time.Hour)), wantProduct: fixtureReplacementProductID,
			wantLinked: "previous-purchase-token", wantNamed: 2,
		},
		{
			name:    "immediate replacement",
			payload: immediate, wantProduct: fixtureReplacementProductID, wantLinked: "previous-purchase-token",
			wantNamed: 2,
		},
		{name: "purchase that replaces nothing", payload: unlinked, wantProduct: fixtureReplacementProductID},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newAdapterFixture(t, core.EnvironmentProduction)
			paths := recordPublisherPaths(providerTransport(t, fixture, test.payload))
			fixture.adapter.client.Transport = paths
			linked, err := fixture.adapter.LinkedPurchase(
				context.Background(),
				linkedPurchaseRequest(t, core.EnvironmentProduction, core.ProductKindSubscription),
			)
			if err != nil {
				t.Fatalf("LinkedPurchase() error = %v", err)
			}
			if linked.ProductID != test.wantProduct || len(linked.CustomerBindings) != 1 ||
				linked.CustomerBindings[0].Kind != "obfuscated_external_account_id" ||
				linked.CustomerBindings[0].Value() != fixtureAccountID {
				t.Fatalf("LinkedPurchase() = %#v", linked)
			}
			if test.wantLinked == "" && len(linked.LinkedReferences) != 0 {
				t.Fatalf("LinkedPurchase() linked references = %#v, want none", linked.LinkedReferences)
			}
			if test.wantLinked != "" && (len(linked.LinkedReferences) != 1 ||
				linked.LinkedReferences[0].Role != core.ReferenceQuery ||
				linked.LinkedReferences[0].Kind != "purchase_token" ||
				linked.LinkedReferences[0].Value() != test.wantLinked) {
				t.Fatalf("LinkedPurchase() linked references = %#v, want %q", linked.LinkedReferences, test.wantLinked)
			}
			if len(paths.paths) != 1 || !strings.Contains(paths.paths[0], "/purchases/subscriptionsv2/tokens/") {
				t.Fatalf("publisher paths = %v, want one subscriptionsv2 query", paths.paths)
			}

			// The worker reconciles the new token with the returned product and binding. That request goes
			// through ProductsForClaim, so a purchase that names several products must accept the product.
			verification := verificationRequest(t, core.EnvironmentProduction, core.ProductKindSubscription)
			result, err := fixture.adapter.Reconcile(context.Background(), stores.ReconciliationRequest{
				Application: verification.Application, CustomerID: verification.CustomerID,
				ExpectedProducts:         []core.ProviderProductID{linked.ProductID},
				ExpectedProductKind:      core.ProductKindSubscription,
				ExpectedCustomerBindings: linked.CustomerBindings,
				QueryReferences:          linkedPurchaseRequest(t, core.EnvironmentProduction, core.ProductKindSubscription).QueryReferences,
			})
			if err != nil {
				t.Fatalf("Reconcile(linked product %q) error = %v", linked.ProductID, err)
			}
			if result.Observations[0].ProductID != linked.ProductID || result.Observations[0].Access != core.AccessAllowed {
				t.Fatalf("Reconcile(linked product %q) current observation = %#v", linked.ProductID, result.Observations[0])
			}
			if test.wantNamed != len(result.NamedProducts) {
				t.Fatalf("Reconcile(linked product %q) named products = %v, want %d", linked.ProductID, result.NamedProducts, test.wantNamed)
			}
		})
	}
}

// TestAdapterLinkedPurchaseRejectsWhatVerificationRejects verifies the customer-free lookup is not a weaker path.
func TestAdapterLinkedPurchaseRejectsWhatVerificationRejects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 25, 15, 0, 0, 0, time.UTC)
	withoutAccount := pendingDeferredReplacement(now, now.Add(10*24*time.Hour))
	delete(withoutAccount, "externalAccountIdentifiers")
	for _, test := range []struct {
		name        string
		environment core.Environment
		payload     map[string]any
		want        stores.FailureKind
	}{
		{name: "missing account id", environment: core.EnvironmentProduction, payload: withoutAccount, want: stores.FailureInvalidEvidence},
		{
			name: "live purchase in a test application", environment: core.EnvironmentSandbox,
			payload: pendingDeferredReplacement(now, now.Add(10*24*time.Hour)), want: stores.FailureInvalidEvidence,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newAdapterFixture(t, test.environment)
			fixture.adapter.client.Transport = providerTransport(t, fixture, test.payload)
			_, err := fixture.adapter.LinkedPurchase(
				context.Background(),
				linkedPurchaseRequest(t, test.environment, core.ProductKindSubscription),
			)
			if !isFailureKind(err, test.want) {
				t.Fatalf("LinkedPurchase() error = %v, want %q", err, test.want)
			}
		})
	}

	fixture := newAdapterFixture(t, core.EnvironmentProduction)
	fixture.adapter.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected provider request %s", request.URL)
		return nil, nil
	})
	if _, err := fixture.adapter.LinkedPurchase(
		context.Background(),
		linkedPurchaseRequest(t, core.EnvironmentProduction, core.ProductKindNonConsumable),
	); !isFailureKind(err, stores.FailureInvalidEvidence) {
		t.Fatalf("LinkedPurchase() one-time product error = %v, want invalid evidence", err)
	}

	fixture.adapter.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() == fixture.adapter.tokenURL {
			return jsonResponse(t, http.StatusOK, tokenResponse{
				AccessToken: "provider-access-token", TokenType: "Bearer", ExpiresIn: 3600,
			}, nil), nil
		}
		return jsonResponse(t, http.StatusNotFound, map[string]any{"error": "not found"}, nil), nil
	})
	if _, err := fixture.adapter.LinkedPurchase(
		context.Background(),
		linkedPurchaseRequest(t, core.EnvironmentProduction, core.ProductKindSubscription),
	); !isFailureKind(err, stores.FailureNotFound) {
		t.Fatalf("LinkedPurchase() unknown token error = %v, want provider not found", err)
	}
}

// recordPublisherPaths wraps one fixture transport.
func recordPublisherPaths(next http.RoundTripper) *publisherPathRecorder {
	return &publisherPathRecorder{next: next}
}

// RoundTrip records non-OAuth request paths and delegates the request.
func (recorder *publisherPathRecorder) RoundTrip(request *http.Request) (*http.Response, error) {
	if strings.Contains(request.URL.Path, "/androidpublisher/") {
		recorder.paths = append(recorder.paths, request.URL.Path)
	}
	return recorder.next.RoundTrip(request)
}

// linkedPurchaseRequest builds one customer-free lookup of the fixture purchase token.
func linkedPurchaseRequest(t *testing.T, environment core.Environment, kind core.ProductKind) stores.LinkedPurchaseRequest {
	t.Helper()
	reference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", fixturePurchaseToken)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	return stores.LinkedPurchaseRequest{
		Application:     verificationRequest(t, environment, kind).Application,
		ProductKind:     kind,
		QueryReferences: []core.StoreReference{reference},
	}
}
