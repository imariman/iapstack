package stores_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

type fakeAdapter struct {
	provider core.Provider
}

// TestEvidenceIsOpaqueCopiedAndLogSafe verifies defensive copying, hashing, and redaction.
func TestEvidenceIsOpaqueCopiedAndLogSafe(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"purchaseToken":"secret"}`)
	evidence, err := stores.NewEvidence("application/vnd.iapstack.google-play.purchase+json", payload)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	digest := evidence.Digest()

	payload[0] = 'x'
	copyOfEvidence := evidence.Bytes()
	copyOfEvidence[1] = 'x'
	if evidence.Digest() != digest {
		t.Fatal("evidence changed after mutating input or returned copy")
	}
	if strings.Contains(evidence.LogValue().String(), "secret") {
		t.Fatal("LogValue() leaked evidence payload")
	}
}

// TestEvidenceRequiresMediaType verifies rejection of malformed evidence content types.
func TestEvidenceRequiresMediaType(t *testing.T) {
	t.Parallel()

	if _, err := stores.NewEvidence("not a media type", []byte("payload")); err == nil {
		t.Fatal("NewEvidence() error = nil, want media type error")
	}
}

// TestVerificationResultSupportsMultipleLineItems verifies provider responses with several products.
func TestVerificationResultSupportsMultipleLineItems(t *testing.T) {
	t.Parallel()

	application := googleApplication()
	evidence, err := stores.NewEvidence("application/vnd.iapstack.google-play.purchase+json", []byte(`{"token":"opaque"}`))
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	request := stores.VerificationRequest{
		Application:     application,
		CustomerID:      "customer_1",
		ClaimedProducts: []core.ProviderProductID{"coins_100", "bonus_pack"},
		Evidence:        evidence,
	}
	result := stores.VerificationResult{
		VerifiedAt: time.Now().UTC(),
		Artifacts:  []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: evidence}},
		Observations: []core.PurchaseObservation{
			productObservation(t, application, "observation_1", "coins_100"),
			productObservation(t, application, "observation_2", "bonus_pack"),
		},
	}

	if err := result.ValidateForVerification(request); err != nil {
		t.Fatalf("ValidateForVerification() error = %v", err)
	}
}

// TestVerificationResultRejectsScopeAndProductMismatch verifies request-result consistency.
func TestVerificationResultRejectsScopeAndProductMismatch(t *testing.T) {
	t.Parallel()

	application := googleApplication()
	evidence, err := stores.NewEvidence("application/json", []byte(`{"token":"opaque"}`))
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	request := stores.VerificationRequest{
		Application:     application,
		CustomerID:      "customer_1",
		ClaimedProducts: []core.ProviderProductID{"coins_100"},
		Evidence:        evidence,
	}

	tests := []struct {
		name        string
		observation core.PurchaseObservation
	}{
		{
			name: "provider application",
			observation: func() core.PurchaseObservation {
				observation := productObservation(t, application, "observation_1", "coins_100")
				observation.Store.ID = "other.app"
				return observation
			}(),
		},
		{
			name:        "product",
			observation: productObservation(t, application, "observation_1", "other_product"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := stores.VerificationResult{
				VerifiedAt:   time.Now().UTC(),
				Artifacts:    []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: evidence}},
				Observations: []core.PurchaseObservation{tt.observation},
			}
			if err := result.ValidateForVerification(request); err == nil {
				t.Fatal("ValidateForVerification() error = nil, want mismatch error")
			}
		})
	}
}

// TestVerificationResultChecksExpectedCustomerBinding verifies customer-bound purchase evidence.
func TestVerificationResultChecksExpectedCustomerBinding(t *testing.T) {
	t.Parallel()

	application := googleApplication()
	evidence, err := stores.NewEvidence("application/json", []byte(`{"token":"opaque"}`))
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	binding := storeReference(t, core.ReferenceCustomerBinding, "obfuscated_account_id", "customer-binding")
	request := stores.VerificationRequest{
		Application:              application,
		CustomerID:               "customer_1",
		ClaimedProducts:          []core.ProviderProductID{"coins_100"},
		ExpectedCustomerBindings: []core.StoreReference{binding},
		Evidence:                 evidence,
	}
	observation := productObservation(t, application, "observation_1", "coins_100")
	result := stores.VerificationResult{
		VerifiedAt:   time.Now().UTC(),
		Artifacts:    []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: evidence}},
		Observations: []core.PurchaseObservation{observation},
	}

	if err := result.ValidateForVerification(request); err == nil {
		t.Fatal("ValidateForVerification() error = nil, want customer binding mismatch")
	}

	result.Observations[0].References = append(result.Observations[0].References, binding)
	if err := result.ValidateForVerification(request); err != nil {
		t.Fatalf("ValidateForVerification() error = %v", err)
	}
}

// TestReconciliationUsesOpaqueQueryReferences verifies query role and uniqueness rules.
func TestReconciliationUsesOpaqueQueryReferences(t *testing.T) {
	t.Parallel()

	application := googleApplication()
	request := stores.ReconciliationRequest{
		Application:         application,
		CustomerID:          "customer_1",
		ExpectedProducts:    []core.ProviderProductID{"pro_monthly"},
		ExpectedProductKind: core.ProductKindSubscription,
		QueryReferences: []core.StoreReference{
			storeReference(t, core.ReferenceQuery, "purchase_token", "secret-token"),
		},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	request.QueryReferences = append(request.QueryReferences, request.QueryReferences[0])
	if err := request.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want duplicate query reference error")
	}

	request.QueryReferences = []core.StoreReference{
		storeReference(t, core.ReferenceLineage, "purchase_token", "wrong-role"),
	}
	if err := request.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want query role error")
	}
}

// TestReconciliationResultMustMatchQueryReference verifies query-to-result identity binding.
func TestReconciliationResultMustMatchQueryReference(t *testing.T) {
	t.Parallel()

	application := googleApplication()
	evidence, err := stores.NewEvidence("application/json", []byte(`{"state":"active"}`))
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	query := storeReference(t, core.ReferenceQuery, "purchase_token", "requested-token")
	request := stores.ReconciliationRequest{
		Application:         application,
		CustomerID:          "customer_1",
		ExpectedProducts:    []core.ProviderProductID{"coins_100"},
		ExpectedProductKind: core.ProductKindConsumable,
		QueryReferences:     []core.StoreReference{query},
	}
	observation := productObservation(t, application, "observation_1", "coins_100")
	result := stores.VerificationResult{
		VerifiedAt:   time.Now().UTC(),
		Artifacts:    []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: evidence}},
		Observations: []core.PurchaseObservation{observation},
	}

	if err := result.ValidateForReconciliation(request); err == nil {
		t.Fatal("ValidateForReconciliation() error = nil, want query mismatch")
	}

	result.Observations[0].References = append(result.Observations[0].References, query)
	if err := result.ValidateForReconciliation(request); err != nil {
		t.Fatalf("ValidateForReconciliation() error = %v", err)
	}
}

// TestVerificationResultBindsPostCommitActionsToObservations verifies provider actions cannot escape verified purchase scope.
func TestVerificationResultBindsPostCommitActionsToObservations(t *testing.T) {
	t.Parallel()

	application := googleApplication()
	evidence, err := stores.NewEvidence("application/json", []byte(`{"state":"active"}`))
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	request := stores.VerificationRequest{
		Application: application, CustomerID: "customer_1",
		ClaimedProducts: []core.ProviderProductID{"pro_lifetime"}, Evidence: evidence,
	}
	observation := productObservation(t, application, "observation_1", "pro_lifetime")
	observation.ProductKind = core.ProductKindNonConsumable
	queryReference := observation.ReferencesFor(core.ReferenceQuery)[0]
	result := stores.VerificationResult{
		VerifiedAt:   time.Now().UTC(),
		Artifacts:    []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: evidence}},
		Observations: []core.PurchaseObservation{observation},
		PostCommitActions: []stores.PostCommitAction{{
			Kind: "acknowledge_purchase", ProductID: observation.ProductID,
			ProductKind: observation.ProductKind, QueryReferences: []core.StoreReference{queryReference},
		}},
	}

	if err := result.ValidateForVerification(request); err != nil {
		t.Fatalf("ValidateForVerification() matching action error = %v", err)
	}
	result.PostCommitActions[0].QueryReferences = []core.StoreReference{
		storeReference(t, core.ReferenceQuery, "purchase_token", "other-token"),
	}
	if err := result.ValidateForVerification(request); err == nil {
		t.Fatal("ValidateForVerification() unbound action error = nil")
	}
}

// TestPostCommitRequestRejectsDuplicateActions verifies duplicate provider effects cannot be requested together.
func TestPostCommitRequestRejectsDuplicateActions(t *testing.T) {
	t.Parallel()

	action := stores.PostCommitAction{
		Kind: "acknowledge_purchase", ProductID: "pro_lifetime", ProductKind: core.ProductKindNonConsumable,
		QueryReferences: []core.StoreReference{
			storeReference(t, core.ReferenceQuery, "purchase_token", "purchase-token"),
		},
	}
	request := stores.PostCommitRequest{
		Application: googleApplication(),
		Actions:     []stores.PostCommitAction{action, action},
	}
	if err := request.Validate(); err == nil {
		t.Fatal("Validate() duplicate action error = nil")
	}
}

// TestRegistrySupportsBuiltInAndFutureProviders verifies extensible and duplicate-safe registration.
func TestRegistrySupportsBuiltInAndFutureProviders(t *testing.T) {
	t.Parallel()

	google := fakeAdapter{provider: core.ProviderGooglePlay}
	future := fakeAdapter{provider: "future_store"}
	registry, err := stores.NewRegistry(google, future)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	for _, provider := range []core.Provider{core.ProviderGooglePlay, "future_store"} {
		adapter, err := registry.Adapter(provider)
		if err != nil {
			t.Fatalf("Adapter(%q) error = %v", provider, err)
		}
		if adapter.Provider() != provider {
			t.Errorf("Adapter(%q).Provider() = %q", provider, adapter.Provider())
		}
	}

	if _, err := stores.NewRegistry(google, google); err == nil {
		t.Fatal("NewRegistry() error = nil, want duplicate provider error")
	}
	var nilAdapter *fakeAdapter
	if _, err := stores.NewRegistry(nilAdapter); err == nil {
		t.Fatal("NewRegistry() error = nil, want typed nil adapter error")
	}
	if _, err := registry.Adapter(core.ProviderAppleAppStore); !errors.Is(err, stores.ErrAdapterNotFound) {
		t.Fatalf("Adapter() error = %v, want ErrAdapterNotFound", err)
	}
}

// TestFailureClassificationDoesNotLeakCause verifies retry classification and safe messages.
func TestFailureClassificationDoesNotLeakCause(t *testing.T) {
	t.Parallel()

	failure := stores.NewFailure(
		core.ProviderHuaweiAppGallery,
		"verify",
		stores.FailureTemporary,
		time.Second,
		errors.New("response included secret-token"),
	)
	if !failure.Retryable() {
		t.Fatal("Retryable() = false, want true")
	}
	if strings.Contains(failure.Error(), "secret-token") {
		t.Fatal("Error() leaked provider cause")
	}
	if failure.Unwrap() == nil {
		t.Fatal("Unwrap() = nil, want original cause")
	}
}

// Provider returns the provider assigned to the fake adapter.
func (adapter fakeAdapter) Provider() core.Provider {
	return adapter.provider
}

// Verify satisfies the adapter contract for registry tests.
func (fakeAdapter) Verify(context.Context, stores.VerificationRequest) (stores.VerificationResult, error) {
	return stores.VerificationResult{}, nil
}

// Reconcile satisfies the adapter contract for registry tests.
func (fakeAdapter) Reconcile(context.Context, stores.ReconciliationRequest) (stores.VerificationResult, error) {
	return stores.VerificationResult{}, nil
}

// googleApplication builds a valid Google Play application fixture.
func googleApplication() core.Application {
	return core.Application{
		ID:        "app_1",
		ProjectID: "project_1",
		Store: core.StoreApplication{
			Provider:    core.ProviderGooglePlay,
			Environment: core.EnvironmentProduction,
			ID:          "com.example.app",
		},
	}
}

// productObservation builds a valid consumable line-item observation for contract tests.
func productObservation(
	t *testing.T,
	application core.Application,
	id core.ObservationID,
	productID core.ProviderProductID,
) core.PurchaseObservation {
	t.Helper()
	purchasedAt := time.Now().UTC().Add(-time.Minute)
	return core.PurchaseObservation{
		ID:              id,
		ApplicationID:   application.ID,
		Store:           application.Store,
		ProductID:       productID,
		ProductKind:     core.ProductKindConsumable,
		State:           core.LifecycleActive,
		ProviderState:   "PURCHASED",
		Access:          core.AccessAllowed,
		AccessReason:    core.AccessReasonPurchaseValid,
		Ownership:       core.OwnershipPurchased,
		Quantity:        1,
		OccurredAt:      purchasedAt,
		ObservedAt:      purchasedAt.Add(time.Second),
		EffectivePeriod: core.EffectivePeriod{StartsAt: purchasedAt},
		Renewal:         core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable},
		References: []core.StoreReference{
			storeReference(t, core.ReferenceTransaction, "order_id", string(id)+"-order"),
			storeReference(t, core.ReferenceQuery, "purchase_token", string(id)+"-token"),
		},
	}
}

// storeReference builds a validated opaque reference for store contract tests.
func storeReference(t *testing.T, role core.ReferenceRole, kind, value string) core.StoreReference {
	t.Helper()
	reference, err := core.NewStoreReference(role, kind, value)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	return reference
}
