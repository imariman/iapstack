package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

const (
	// confirmStoreProductsQuery reads provider product membership and does not write catalog rows.
	confirmStoreProductsQuery = `
		SELECT provider_product_id
		FROM store_products
		WHERE application_id = $1
			AND provider_product_id = ANY($2)`
)

// ConfirmStoreProducts reports whether every provider product ID already exists for the application.
// It only reads store_products and does not create catalog rows.
func (store *Store) ConfirmStoreProducts(
	ctx context.Context,
	applicationID core.ApplicationID,
	ids []core.ProviderProductID,
) error {
	if store == nil || store.pool == nil {
		return errors.New("postgresql store is not initialized")
	}
	if err := applicationID.Validate(); err != nil {
		return err
	}
	unique, err := uniqueStoreProductIDs(ids)
	if err != nil {
		return err
	}

	rows, err := store.pool.Query(ctx, confirmStoreProductsQuery, string(applicationID), unique)
	if err != nil {
		return classifyError("confirm store products", err)
	}
	defer rows.Close()

	found := make(map[string]struct{}, len(unique))
	for rows.Next() {
		var providerProductID string
		if err := rows.Scan(&providerProductID); err != nil {
			return classifyError("scan store product", err)
		}
		found[providerProductID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return classifyError("iterate store products", err)
	}
	if len(found) != len(unique) {
		return fmt.Errorf("confirm store products: %w", persistence.ErrNotFound)
	}
	return nil
}

// uniqueStoreProductIDs validates provider product IDs and drops duplicates.
func uniqueStoreProductIDs(ids []core.ProviderProductID) ([]string, error) {
	if len(ids) == 0 {
		return nil, errors.New("store product IDs are required")
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[core.ProviderProductID]struct{}, len(ids))
	for _, id := range ids {
		if err := id.Validate(); err != nil {
			return nil, err
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, string(id))
	}
	return unique, nil
}
