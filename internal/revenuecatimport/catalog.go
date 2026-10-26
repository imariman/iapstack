package revenuecatimport

import (
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// Catalog is an in-memory copy of the project records an import may reference.
type Catalog struct {
	applications  map[core.ApplicationID]core.Application
	products      map[core.ProductID]core.Product
	entitlements  map[string]core.Entitlement
	storeProducts map[string]struct{}
}

// NewCatalog indexes one loaded project catalog.
func NewCatalog(snapshot persistence.ImportCatalog) Catalog {
	catalog := Catalog{
		applications:  make(map[core.ApplicationID]core.Application, len(snapshot.Applications)),
		products:      make(map[core.ProductID]core.Product, len(snapshot.Products)),
		entitlements:  make(map[string]core.Entitlement, len(snapshot.Entitlements)),
		storeProducts: make(map[string]struct{}, len(snapshot.StoreProducts)),
	}
	for _, application := range snapshot.Applications {
		catalog.applications[application.ID] = application
	}
	for _, product := range snapshot.Products {
		catalog.products[product.ID] = product
	}
	for _, entitlement := range snapshot.Entitlements {
		catalog.entitlements[entitlement.Key] = entitlement
	}
	for _, mapping := range snapshot.StoreProducts {
		catalog.storeProducts[storeProductKey(mapping.ApplicationID, string(mapping.ProviderID))] = struct{}{}
	}
	return catalog
}

// storeProductKey joins an application and provider product id.
func storeProductKey(applicationID core.ApplicationID, providerProductID string) string {
	return string(applicationID) + "\x00" + providerProductID
}

// requiresStoreProduct reports whether the RevenueCat store must already have a store_products row.
func requiresStoreProduct(store string) bool {
	return store == "app_store" || store == "play_store"
}
