package core_test

import (
	"testing"

	"github.com/imariman/iapstack/internal/core"
)

func TestProviderIsExtensible(t *testing.T) {
	t.Parallel()

	for _, provider := range []core.Provider{
		core.ProviderAppleAppStore,
		core.ProviderGooglePlay,
		core.ProviderHuaweiAppGallery,
		core.ProviderAmazonAppstore,
		core.ProviderSamsungGalaxyStore,
		"future_store",
	} {
		if err := provider.Validate(); err != nil {
			t.Errorf("Provider(%q).Validate() error = %v", provider, err)
		}
	}
}

func TestProductValidation(t *testing.T) {
	t.Parallel()

	product := core.Product{
		ID:             "pro_monthly",
		ProjectID:      "project_1",
		Kind:           core.ProductKindSubscription,
		EntitlementIDs: []core.EntitlementID{"pro"},
	}
	if err := product.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	product.EntitlementIDs = append(product.EntitlementIDs, "pro")
	if err := product.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want duplicate entitlement error")
	}
}

func TestApplicationRequiresStoreScope(t *testing.T) {
	t.Parallel()

	application := core.Application{
		ID:        "app_1",
		ProjectID: "project_1",
		Store: core.StoreApplication{
			Provider:    core.ProviderGooglePlay,
			Environment: core.EnvironmentProduction,
			ID:          "com.example.app",
		},
	}
	if err := application.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	application.Store.Environment = "unknown"
	if err := application.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want environment error")
	}
}
