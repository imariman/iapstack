package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
)

// PutProject creates one project or confirms the identity already exists.
func (repository *transaction) PutProject(ctx context.Context, projectID core.ProjectID) error {
	if err := projectID.Validate(); err != nil {
		return err
	}
	_, err := repository.tx.Exec(ctx, `INSERT INTO projects (id) VALUES ($1) ON CONFLICT (id) DO NOTHING`, projectID)
	return classifyError("put project", err)
}

// PutApplication creates one provider application or confirms identical existing configuration.
func (repository *transaction) PutApplication(ctx context.Context, application core.Application) error {
	if err := application.Validate(); err != nil {
		return err
	}
	_, err := repository.tx.Exec(ctx, `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING
	`, application.ID, application.ProjectID, application.Store.Provider, application.Store.Environment, application.Store.ID)
	if err != nil {
		return classifyError("put application", err)
	}
	stored, err := repository.Application(ctx, application.ProjectID, application.ID)
	if err != nil {
		return err
	}
	if stored != application {
		return fmt.Errorf("put application: %w", persistence.ErrConflict)
	}
	return nil
}

// PutCustomer creates one external identity or returns its authoritative existing record.
func (repository *transaction) PutCustomer(ctx context.Context, customer core.Customer) (core.Customer, error) {
	if err := customer.Validate(); err != nil {
		return core.Customer{}, err
	}
	_, err := repository.tx.Exec(ctx, `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (project_id, external_id) DO NOTHING
	`, customer.ID, customer.ProjectID, customer.ExternalID)
	if err != nil {
		return core.Customer{}, classifyError("put customer", err)
	}
	stored, err := repository.CustomerByExternalID(ctx, customer.ProjectID, customer.ExternalID)
	if err != nil {
		return core.Customer{}, err
	}
	if stored.ID != customer.ID {
		return stored, nil
	}
	return stored, nil
}

// PutEntitlementDefinition creates one named entitlement or confirms identical configuration.
func (repository *transaction) PutEntitlementDefinition(ctx context.Context, entitlement core.Entitlement) error {
	if err := entitlement.Validate(); err != nil {
		return err
	}
	_, err := repository.tx.Exec(ctx, `
		INSERT INTO entitlements (id, project_id, key)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING
	`, entitlement.ID, entitlement.ProjectID, entitlement.Key)
	if err != nil {
		return classifyError("put entitlement definition", err)
	}
	var projectID, key string
	err = repository.tx.QueryRow(ctx, `SELECT project_id, key FROM entitlements WHERE id = $1`, entitlement.ID).Scan(&projectID, &key)
	if err != nil {
		return classifyError("load entitlement definition", err)
	}
	if projectID != string(entitlement.ProjectID) || key != entitlement.Key {
		return fmt.Errorf("put entitlement definition: %w", persistence.ErrConflict)
	}
	return nil
}

// PutProduct creates one product and its exact entitlement grant set.
func (repository *transaction) PutProduct(ctx context.Context, product core.Product) error {
	if err := product.Validate(); err != nil {
		return err
	}
	_, err := repository.tx.Exec(ctx, `
		INSERT INTO products (id, project_id, kind)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING
	`, product.ID, product.ProjectID, product.Kind)
	if err != nil {
		return classifyError("put product", err)
	}
	var projectID, kind string
	err = repository.tx.QueryRow(ctx, `SELECT project_id, kind FROM products WHERE id = $1`, product.ID).Scan(&projectID, &kind)
	if err != nil {
		return classifyError("load product", err)
	}
	if projectID != string(product.ProjectID) || kind != string(product.Kind) {
		return fmt.Errorf("put product: %w", persistence.ErrConflict)
	}
	for _, entitlementID := range product.EntitlementIDs {
		_, err = repository.tx.Exec(ctx, `
			INSERT INTO product_entitlements (project_id, product_id, entitlement_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (product_id, entitlement_id) DO NOTHING
		`, product.ProjectID, product.ID, entitlementID)
		if err != nil {
			return classifyError("put product entitlement", err)
		}
	}
	rows, err := repository.tx.Query(ctx, `
		SELECT entitlement_id FROM product_entitlements WHERE project_id = $1 AND product_id = $2
	`, product.ProjectID, product.ID)
	if err != nil {
		return classifyError("load product entitlements", err)
	}
	defer rows.Close()
	stored := make(map[core.EntitlementID]struct{})
	for rows.Next() {
		var entitlementID core.EntitlementID
		if err := rows.Scan(&entitlementID); err != nil {
			return classifyError("scan product entitlement", err)
		}
		stored[entitlementID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return classifyError("iterate product entitlements", err)
	}
	if len(stored) != len(product.EntitlementIDs) {
		return fmt.Errorf("put product entitlements: %w", persistence.ErrConflict)
	}
	for _, entitlementID := range product.EntitlementIDs {
		if _, exists := stored[entitlementID]; !exists {
			return fmt.Errorf("put product entitlements: %w", persistence.ErrConflict)
		}
	}
	return nil
}

// PutStoreProduct creates one provider mapping or confirms the same application product identity.
func (repository *transaction) PutStoreProduct(
	ctx context.Context,
	projectID core.ProjectID,
	mapping core.StoreProduct,
) error {
	if err := errors.Join(projectID.Validate(), mapping.Validate()); err != nil {
		return err
	}
	_, err := repository.tx.Exec(ctx, `
		INSERT INTO store_products (project_id, application_id, product_id, provider_product_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (application_id, provider_product_id) DO NOTHING
	`, projectID, mapping.ApplicationID, mapping.ProductID, mapping.ProviderID)
	if err != nil {
		return classifyError("put store product", err)
	}
	var storedProjectID, storedProductID string
	err = repository.tx.QueryRow(ctx, `
		SELECT project_id, product_id FROM store_products
		WHERE application_id = $1 AND provider_product_id = $2
	`, mapping.ApplicationID, mapping.ProviderID).Scan(&storedProjectID, &storedProductID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return classifyError("load store product", err)
	}
	if storedProjectID != string(projectID) || storedProductID != string(mapping.ProductID) {
		return fmt.Errorf("put store product: %w", persistence.ErrConflict)
	}
	return nil
}
