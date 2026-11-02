package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// Application returns one application inside its owning project.
func (repository *transaction) Application(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) (core.Application, error) {
	if err := errors.Join(projectID.Validate(), applicationID.Validate()); err != nil {
		return core.Application{}, err
	}

	var provider string
	var environment string
	var providerApplicationID string
	err := repository.tx.QueryRow(ctx, `
		SELECT provider, environment, provider_application_id
		FROM applications
		WHERE project_id = $1 AND id = $2
	`, projectID, applicationID).Scan(&provider, &environment, &providerApplicationID)
	if err != nil {
		return core.Application{}, classifyError("load application", err)
	}

	application := core.Application{
		ID:        applicationID,
		ProjectID: projectID,
		Store: core.StoreApplication{
			Provider:    core.Provider(provider),
			Environment: core.Environment(environment),
			ID:          core.ProviderApplicationID(providerApplicationID),
		},
	}
	if err := application.Validate(); err != nil {
		return core.Application{}, fmt.Errorf("validate stored application: %w", err)
	}
	return application, nil
}

// CustomerByExternalID resolves one external id to its canonical project customer.
func (repository *transaction) CustomerByExternalID(
	ctx context.Context,
	projectID core.ProjectID,
	externalID string,
) (core.Customer, error) {
	resolution, err := repository.ResolveCustomer(ctx, projectID, externalID)
	if err != nil {
		return core.Customer{}, err
	}
	identity, err := repository.customerIdentityByID(ctx, projectID, resolution.CanonicalCustomerID)
	if err != nil {
		return core.Customer{}, err
	}
	if identity.mergedInto != nil {
		return core.Customer{}, fmt.Errorf("load canonical customer: %w", persistence.ErrConflict)
	}
	return identity.customer(), nil
}

// CatalogProducts resolves provider product mappings and their granted entitlements.
// A mapped product with no grants is returned with an empty entitlement list.
func (repository *transaction) CatalogProducts(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	providerProductIDs []core.ProviderProductID,
) ([]persistence.CatalogProduct, error) {
	productIDs, err := validateCatalogProductIDs(projectID, applicationID, providerProductIDs)
	if err != nil {
		return nil, err
	}
	if len(productIDs) == 0 {
		return []persistence.CatalogProduct{}, nil
	}

	rows, err := repository.tx.Query(ctx, `
		SELECT
			sp.provider_product_id,
			sp.product_id,
			p.kind,
			e.id,
			e.key
		FROM store_products AS sp
		JOIN products AS p
			ON p.id = sp.product_id AND p.project_id = sp.project_id
		LEFT JOIN product_entitlements AS pe
			ON pe.product_id = p.id AND pe.project_id = p.project_id
		LEFT JOIN entitlements AS e
			ON e.id = pe.entitlement_id AND e.project_id = pe.project_id
		WHERE sp.project_id = $1
			AND sp.application_id = $2
			AND sp.provider_product_id = ANY($3)
		ORDER BY sp.provider_product_id, e.id
	`, projectID, applicationID, productIDs)
	if err != nil {
		return nil, classifyError("load catalog products", err)
	}
	defer rows.Close()

	productsByProviderID := make(map[core.ProviderProductID]*persistence.CatalogProduct, len(productIDs))
	for rows.Next() {
		var providerProductID string
		var productID string
		var productKind string
		var entitlementID *string
		var entitlementKey *string
		if err := rows.Scan(
			&providerProductID,
			&productID,
			&productKind,
			&entitlementID,
			&entitlementKey,
		); err != nil {
			return nil, classifyError("scan catalog product", err)
		}

		providerID := core.ProviderProductID(providerProductID)
		catalogProduct, exists := productsByProviderID[providerID]
		if !exists {
			catalogProduct = &persistence.CatalogProduct{
				Mapping: core.StoreProduct{
					ApplicationID: applicationID,
					ProductID:     core.ProductID(productID),
					ProviderID:    providerID,
				},
				Product: core.Product{
					ID:             core.ProductID(productID),
					ProjectID:      projectID,
					Kind:           core.ProductKind(productKind),
					EntitlementIDs: []core.EntitlementID{},
				},
				Entitlements: []core.Entitlement{},
			}
			productsByProviderID[providerID] = catalogProduct
		}
		if entitlementID == nil && entitlementKey == nil {
			continue
		}
		if entitlementID == nil || entitlementKey == nil {
			return nil, fmt.Errorf("stored catalog entitlement for %q is incomplete", providerProductID)
		}
		entitlement := core.Entitlement{
			ID:        core.EntitlementID(*entitlementID),
			ProjectID: projectID,
			Key:       *entitlementKey,
		}
		catalogProduct.Product.EntitlementIDs = append(catalogProduct.Product.EntitlementIDs, entitlement.ID)
		catalogProduct.Entitlements = append(catalogProduct.Entitlements, entitlement)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate catalog products", err)
	}

	products := make([]persistence.CatalogProduct, 0, len(providerProductIDs))
	for _, providerProductID := range providerProductIDs {
		catalogProduct, exists := productsByProviderID[providerProductID]
		if !exists {
			return nil, fmt.Errorf(
				"load catalog product %q: %w",
				providerProductID,
				persistence.ErrNotFound,
			)
		}
		if err := validateLoadedCatalogProduct(*catalogProduct); err != nil {
			return nil, fmt.Errorf("validate stored catalog product %q: %w", providerProductID, err)
		}
		products = append(products, *catalogProduct)
	}
	return products, nil
}

// validateCatalogProductIDs checks catalog scope and rejects duplicate provider product IDs.
func validateCatalogProductIDs(
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	providerProductIDs []core.ProviderProductID,
) ([]string, error) {
	if err := errors.Join(projectID.Validate(), applicationID.Validate()); err != nil {
		return nil, err
	}
	productIDs := make([]string, 0, len(providerProductIDs))
	seen := make(map[core.ProviderProductID]struct{}, len(providerProductIDs))
	for _, providerProductID := range providerProductIDs {
		if err := providerProductID.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := seen[providerProductID]; duplicate {
			return nil, fmt.Errorf("duplicate provider product ID %q", providerProductID)
		}
		seen[providerProductID] = struct{}{}
		productIDs = append(productIDs, string(providerProductID))
	}
	return productIDs, nil
}

// validateLoadedCatalogProduct checks a stored mapping and allows a product that grants nothing.
// Writers still reject an empty grant set; callers decide what an empty read means.
func validateLoadedCatalogProduct(product persistence.CatalogProduct) error {
	if len(product.Entitlements) == 0 {
		return errors.Join(
			product.Mapping.Validate(),
			product.Product.ID.Validate(),
			product.Product.ProjectID.Validate(),
			product.Product.Kind.Validate(),
		)
	}
	return errors.Join(
		product.Mapping.Validate(),
		product.Product.Validate(),
		validateEntitlements(product.Entitlements),
	)
}

// validateEntitlements checks every entitlement loaded with a catalog product.
func validateEntitlements(entitlements []core.Entitlement) error {
	for index, entitlement := range entitlements {
		if err := entitlement.Validate(); err != nil {
			return fmt.Errorf("entitlement %d: %w", index, err)
		}
	}
	return nil
}
