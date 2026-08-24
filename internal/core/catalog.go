package core

import (
	"errors"
	"fmt"
)

const (
	// ProviderAppleAppStore identifies Apple's App Store purchase provider.
	ProviderAppleAppStore Provider = "apple_app_store"
	// ProviderGooglePlay identifies the Google Play purchase provider.
	ProviderGooglePlay Provider = "google_play"
	// ProviderHuaweiAppGallery identifies the Huawei AppGallery purchase provider.
	ProviderHuaweiAppGallery Provider = "huawei_appgallery"
	// ProviderAmazonAppstore identifies the Amazon Appstore purchase provider.
	ProviderAmazonAppstore Provider = "amazon_appstore"
	// ProviderSamsungGalaxyStore identifies the Samsung Galaxy Store purchase provider.
	ProviderSamsungGalaxyStore Provider = "samsung_galaxy_store"

	// EnvironmentProduction identifies live provider transactions.
	EnvironmentProduction Environment = "production"
	// EnvironmentSandbox identifies provider-managed sandbox transactions.
	EnvironmentSandbox Environment = "sandbox"
	// EnvironmentTest identifies local, beta, or other test transactions.
	EnvironmentTest Environment = "test"

	// ProductKindSubscription identifies access sold for a bounded recurring or prepaid period.
	ProductKindSubscription ProductKind = "subscription"
	// ProductKindNonConsumable identifies a durable one-time purchase.
	ProductKindNonConsumable ProductKind = "non_consumable"
	// ProductKindConsumable identifies a quantity that can be fulfilled or consumed.
	ProductKindConsumable ProductKind = "consumable"
)

type Provider string

type Environment string

type ProductKind string

type StoreApplication struct {
	Provider    Provider
	Environment Environment
	ID          ProviderApplicationID
}

type Application struct {
	ID        ApplicationID
	ProjectID ProjectID
	Store     StoreApplication
}

type Customer struct {
	ID         CustomerID
	ProjectID  ProjectID
	ExternalID string
}

type Entitlement struct {
	ID        EntitlementID
	ProjectID ProjectID
	Key       string
}

type Product struct {
	ID             ProductID
	ProjectID      ProjectID
	Kind           ProductKind
	EntitlementIDs []EntitlementID
}

type StoreProduct struct {
	ApplicationID ApplicationID
	ProductID     ProductID
	ProviderID    ProviderProductID
}

// Validate checks that the provider identifier is present and well formed.
func (provider Provider) Validate() error {
	return validateIdentifier("provider", string(provider))
}

// Validate checks that the environment is one of the normalized built-in values.
func (environment Environment) Validate() error {
	switch environment {
	case EnvironmentProduction, EnvironmentSandbox, EnvironmentTest:
		return nil
	default:
		return fmt.Errorf("unsupported environment %q", environment)
	}
}

// Validate checks that the extensible product kind is present and well formed.
func (kind ProductKind) Validate() error {
	return validateIdentifier("product kind", string(kind))
}

// Validate checks that every provider application scope field is valid.
func (application StoreApplication) Validate() error {
	return errors.Join(
		application.Provider.Validate(),
		application.Environment.Validate(),
		application.ID.Validate(),
	)
}

// Validate checks the internal identity, project ownership, and store scope of an application.
func (application Application) Validate() error {
	return errors.Join(
		application.ID.Validate(),
		application.ProjectID.Validate(),
		application.Store.Validate(),
	)
}

// Validate checks the internal, project, and external identities of a customer.
func (customer Customer) Validate() error {
	return errors.Join(
		customer.ID.Validate(),
		customer.ProjectID.Validate(),
		validateIdentifier("external customer ID", customer.ExternalID),
	)
}

// Validate checks the identity, project ownership, and key of an entitlement.
func (entitlement Entitlement) Validate() error {
	return errors.Join(
		entitlement.ID.Validate(),
		entitlement.ProjectID.Validate(),
		validateIdentifier("entitlement key", entitlement.Key),
	)
}

// Validate checks product identity, kind, and entitlement mappings for duplicates.
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

// Validate checks the internal and provider identifiers in a store product mapping.
func (product StoreProduct) Validate() error {
	return errors.Join(
		product.ApplicationID.Validate(),
		product.ProductID.Validate(),
		product.ProviderID.Validate(),
	)
}
