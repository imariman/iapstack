package core

import (
	"errors"
	"fmt"
)

type Provider string

const (
	ProviderAppleAppStore      Provider = "apple_app_store"
	ProviderGooglePlay         Provider = "google_play"
	ProviderHuaweiAppGallery   Provider = "huawei_appgallery"
	ProviderAmazonAppstore     Provider = "amazon_appstore"
	ProviderSamsungGalaxyStore Provider = "samsung_galaxy_store"
)

func (provider Provider) Validate() error {
	return validateIdentifier("provider", string(provider))
}

type Environment string

const (
	EnvironmentProduction Environment = "production"
	EnvironmentSandbox    Environment = "sandbox"
	EnvironmentTest       Environment = "test"
)

func (environment Environment) Validate() error {
	switch environment {
	case EnvironmentProduction, EnvironmentSandbox, EnvironmentTest:
		return nil
	default:
		return fmt.Errorf("unsupported environment %q", environment)
	}
}

type ProductKind string

const (
	ProductKindSubscription  ProductKind = "subscription"
	ProductKindNonConsumable ProductKind = "non_consumable"
	ProductKindConsumable    ProductKind = "consumable"
)

func (kind ProductKind) Validate() error {
	return validateIdentifier("product kind", string(kind))
}

type StoreApplication struct {
	Provider    Provider
	Environment Environment
	ID          ProviderApplicationID
}

func (application StoreApplication) Validate() error {
	return errors.Join(
		application.Provider.Validate(),
		application.Environment.Validate(),
		application.ID.Validate(),
	)
}

type Application struct {
	ID        ApplicationID
	ProjectID ProjectID
	Store     StoreApplication
}

func (application Application) Validate() error {
	return errors.Join(
		application.ID.Validate(),
		application.ProjectID.Validate(),
		application.Store.Validate(),
	)
}

type Customer struct {
	ID         CustomerID
	ProjectID  ProjectID
	ExternalID string
}

func (customer Customer) Validate() error {
	return errors.Join(
		customer.ID.Validate(),
		customer.ProjectID.Validate(),
		validateIdentifier("external customer ID", customer.ExternalID),
	)
}

type Entitlement struct {
	ID        EntitlementID
	ProjectID ProjectID
	Key       string
}

func (entitlement Entitlement) Validate() error {
	return errors.Join(
		entitlement.ID.Validate(),
		entitlement.ProjectID.Validate(),
		validateIdentifier("entitlement key", entitlement.Key),
	)
}

type Product struct {
	ID             ProductID
	ProjectID      ProjectID
	Kind           ProductKind
	EntitlementIDs []EntitlementID
}

func (product Product) Validate() error {
	if err := errors.Join(product.ID.Validate(), product.ProjectID.Validate(), product.Kind.Validate()); err != nil {
		return err
	}
	if len(product.EntitlementIDs) == 0 {
		return errors.New("product must grant at least one entitlement")
	}

	seen := make(map[EntitlementID]struct{}, len(product.EntitlementIDs))
	for _, entitlementID := range product.EntitlementIDs {
		if err := entitlementID.Validate(); err != nil {
			return err
		}
		if _, exists := seen[entitlementID]; exists {
			return fmt.Errorf("duplicate entitlement ID %q", entitlementID)
		}
		seen[entitlementID] = struct{}{}
	}
	return nil
}

type StoreProduct struct {
	ApplicationID ApplicationID
	ProductID     ProductID
	ProviderID    ProviderProductID
}

func (product StoreProduct) Validate() error {
	return errors.Join(
		product.ApplicationID.Validate(),
		product.ProductID.Validate(),
		product.ProviderID.Validate(),
	)
}
