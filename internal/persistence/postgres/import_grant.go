package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EnsureImportBatch records one export hash for a batch or rejects a different hash.
func (repository *transaction) EnsureImportBatch(ctx context.Context, batch persistence.ImportBatch) error {
	if err := batch.Validate(); err != nil {
		return err
	}
	var existing []byte
	err := repository.tx.QueryRow(ctx, `
		SELECT export_sha256
		FROM import_batches
		WHERE project_id = $1 AND batch_id = $2
	`, batch.ProjectID, batch.BatchID).Scan(&existing)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return classifyError("load import batch", err)
		}
		_, err = repository.tx.Exec(ctx, `
			INSERT INTO import_batches (project_id, batch_id, export_sha256, format)
			VALUES ($1, $2, $3, $4)
		`, batch.ProjectID, batch.BatchID, batch.ExportSHA256[:], batch.Format)
		if err != nil {
			return classifyError("insert import batch", err)
		}
		return nil
	}
	if len(existing) != len(batch.ExportSHA256) || !bytes.Equal(existing, batch.ExportSHA256[:]) {
		return fmt.Errorf("import batch: %w", persistence.ErrConflict)
	}
	return nil
}

// UpsertImportGrant inserts or replaces one historical grant without storing a raw store transaction id.
func (repository *transaction) UpsertImportGrant(
	ctx context.Context,
	grant persistence.ImportGrant,
) (persistence.ImportGrantResult, error) {
	if err := grant.Validate(); err != nil {
		return persistence.ImportGrantResult{}, err
	}
	existing, err := repository.loadImportGrant(ctx, grant)
	if err != nil && !errors.Is(err, persistence.ErrNotFound) {
		return persistence.ImportGrantResult{}, err
	}
	if err == nil {
		if importGrantsEqual(existing, grant) {
			return persistence.ImportGrantResult{}, nil
		}
		_, err = repository.tx.Exec(ctx, `
			UPDATE import_grants
			SET origin_store = $1,
				environment = $2,
				access_status = $3,
				access_reason = $4,
				effective_starts_at = $5,
				effective_ends_at = $6,
				observed_at = $7,
				export_sha256 = $8,
				updated_at = now()
			WHERE project_id = $9
				AND customer_id = $10
				AND application_id = $11
				AND product_id = $12
				AND entitlement_id = $13
				AND lineage_fingerprint = $14
		`,
			grant.OriginStore,
			grant.Environment,
			grant.Access,
			grant.AccessReason,
			nullableTime(grant.EffectivePeriod.StartsAt),
			nullableTimePointer(grant.EffectivePeriod.EndsAt),
			normalizeTime(grant.ObservedAt),
			grant.ExportSHA256[:],
			grant.ProjectID,
			grant.CustomerID,
			grant.ApplicationID,
			grant.ProductID,
			grant.EntitlementID,
			grant.LineageFingerprint[:],
		)
		if err != nil {
			return persistence.ImportGrantResult{}, classifyError("update import grant", err)
		}
		return persistence.ImportGrantResult{Updated: true}, nil
	}

	_, err = repository.tx.Exec(ctx, `
		INSERT INTO import_grants (
			id, project_id, customer_id, application_id, product_id, entitlement_id,
			origin, origin_store, environment, access_status, access_reason,
			effective_starts_at, effective_ends_at, observed_at, export_sha256, lineage_fingerprint
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16
		)
	`,
		grant.ID,
		grant.ProjectID,
		grant.CustomerID,
		grant.ApplicationID,
		grant.ProductID,
		grant.EntitlementID,
		grant.Origin,
		grant.OriginStore,
		grant.Environment,
		grant.Access,
		grant.AccessReason,
		nullableTime(grant.EffectivePeriod.StartsAt),
		nullableTimePointer(grant.EffectivePeriod.EndsAt),
		normalizeTime(grant.ObservedAt),
		grant.ExportSHA256[:],
		grant.LineageFingerprint[:],
	)
	if err != nil {
		return persistence.ImportGrantResult{}, classifyError("insert import grant", err)
	}
	return persistence.ImportGrantResult{Inserted: true}, nil
}

// PrimaryCustomerByExternalID returns the customer row that owns an external id, ignoring aliases.
func (repository *transaction) PrimaryCustomerByExternalID(
	ctx context.Context,
	projectID core.ProjectID,
	externalID string,
) (core.Customer, error) {
	return repository.customerByExternalIDDirect(ctx, projectID, externalID)
}

// LoadImportCatalog returns the project catalog an importer may reference and must not create.
func (repository *transaction) LoadImportCatalog(
	ctx context.Context,
	projectID core.ProjectID,
) (persistence.ImportCatalog, error) {
	if err := projectID.Validate(); err != nil {
		return persistence.ImportCatalog{}, err
	}
	catalog := persistence.ImportCatalog{}
	if err := repository.loadImportApplications(ctx, projectID, &catalog); err != nil {
		return persistence.ImportCatalog{}, err
	}
	if err := repository.loadImportProducts(ctx, projectID, &catalog); err != nil {
		return persistence.ImportCatalog{}, err
	}
	if err := repository.loadImportEntitlements(ctx, projectID, &catalog); err != nil {
		return persistence.ImportCatalog{}, err
	}
	if err := repository.loadImportStoreProducts(ctx, projectID, &catalog); err != nil {
		return persistence.ImportCatalog{}, err
	}
	return catalog, nil
}

// loadImportGrant reads one grant by its lineage identity.
func (repository *transaction) loadImportGrant(
	ctx context.Context,
	grant persistence.ImportGrant,
) (persistence.ImportGrant, error) {
	row := persistence.ImportGrant{
		ProjectID:     grant.ProjectID,
		CustomerID:    grant.CustomerID,
		ApplicationID: grant.ApplicationID,
		ProductID:     grant.ProductID,
		EntitlementID: grant.EntitlementID,
	}
	var starts pgtype.Timestamptz
	var ends pgtype.Timestamptz
	var exportSHA []byte
	var lineage []byte
	err := repository.tx.QueryRow(ctx, `
		SELECT id, origin, origin_store, environment, access_status, access_reason,
			effective_starts_at, effective_ends_at, observed_at, export_sha256, lineage_fingerprint
		FROM import_grants
		WHERE project_id = $1
			AND customer_id = $2
			AND application_id = $3
			AND product_id = $4
			AND entitlement_id = $5
			AND lineage_fingerprint = $6
	`, grant.ProjectID, grant.CustomerID, grant.ApplicationID, grant.ProductID, grant.EntitlementID, grant.LineageFingerprint[:]).Scan(
		&row.ID,
		&row.Origin,
		&row.OriginStore,
		&row.Environment,
		&row.Access,
		&row.AccessReason,
		&starts,
		&ends,
		&row.ObservedAt,
		&exportSHA,
		&lineage,
	)
	if err != nil {
		return persistence.ImportGrant{}, classifyError("load import grant", err)
	}
	row.EffectivePeriod = core.EffectivePeriod{StartsAt: timestampValue(starts), EndsAt: timestampPointer(ends)}
	if len(exportSHA) == len(row.ExportSHA256) {
		copy(row.ExportSHA256[:], exportSHA)
	}
	if len(lineage) == len(row.LineageFingerprint) {
		copy(row.LineageFingerprint[:], lineage)
	}
	return row, nil
}

// importGrantsEqual reports whether two grants carry the same access decision and export fingerprint.
func importGrantsEqual(left, right persistence.ImportGrant) bool {
	return left.OriginStore == right.OriginStore &&
		left.Environment == right.Environment &&
		left.Access == right.Access &&
		left.AccessReason == right.AccessReason &&
		normalizeTime(left.EffectivePeriod.StartsAt).Equal(normalizeTime(right.EffectivePeriod.StartsAt)) &&
		timePointersEqual(left.EffectivePeriod.EndsAt, right.EffectivePeriod.EndsAt) &&
		normalizeTime(left.ObservedAt).Equal(normalizeTime(right.ObservedAt)) &&
		left.ExportSHA256 == right.ExportSHA256
}

// loadImportApplications reads applications the importer may target.
func (repository *transaction) loadImportApplications(
	ctx context.Context,
	projectID core.ProjectID,
	catalog *persistence.ImportCatalog,
) error {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, provider, environment, provider_application_id
		FROM applications
		WHERE project_id = $1
		ORDER BY id
	`, projectID)
	if err != nil {
		return classifyError("load import applications", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, provider, environment, providerApplicationID string
		if err := rows.Scan(&id, &provider, &environment, &providerApplicationID); err != nil {
			return classifyError("scan import application", err)
		}
		application := core.Application{
			ID:        core.ApplicationID(id),
			ProjectID: projectID,
			Store: core.StoreApplication{
				Provider:    core.Provider(provider),
				Environment: core.Environment(environment),
				ID:          core.ProviderApplicationID(providerApplicationID),
			},
		}
		if err := application.Validate(); err != nil {
			return fmt.Errorf("validate stored import application: %w", err)
		}
		catalog.Applications = append(catalog.Applications, application)
	}
	return classifyError("iterate import applications", rows.Err())
}

// loadImportProducts reads products and the entitlements each product grants.
func (repository *transaction) loadImportProducts(
	ctx context.Context,
	projectID core.ProjectID,
	catalog *persistence.ImportCatalog,
) error {
	rows, err := repository.tx.Query(ctx, `
		SELECT p.id, p.kind, pe.entitlement_id
		FROM products AS p
		JOIN product_entitlements AS pe
			ON pe.project_id = p.project_id AND pe.product_id = p.id
		WHERE p.project_id = $1
		ORDER BY p.id, pe.entitlement_id
	`, projectID)
	if err != nil {
		return classifyError("load import products", err)
	}
	defer rows.Close()
	index := map[core.ProductID]int{}
	for rows.Next() {
		var id, kind, entitlementID string
		if err := rows.Scan(&id, &kind, &entitlementID); err != nil {
			return classifyError("scan import product", err)
		}
		productID := core.ProductID(id)
		position, exists := index[productID]
		if !exists {
			catalog.Products = append(catalog.Products, core.Product{
				ID:        productID,
				ProjectID: projectID,
				Kind:      core.ProductKind(kind),
			})
			position = len(catalog.Products) - 1
			index[productID] = position
		}
		catalog.Products[position].EntitlementIDs = append(
			catalog.Products[position].EntitlementIDs,
			core.EntitlementID(entitlementID),
		)
	}
	if err := rows.Err(); err != nil {
		return classifyError("iterate import products", err)
	}
	for _, product := range catalog.Products {
		if err := product.Validate(); err != nil {
			return fmt.Errorf("validate stored import product: %w", err)
		}
	}
	return nil
}

// loadImportEntitlements reads entitlement keys in one project.
func (repository *transaction) loadImportEntitlements(
	ctx context.Context,
	projectID core.ProjectID,
	catalog *persistence.ImportCatalog,
) error {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, key FROM entitlements WHERE project_id = $1 ORDER BY id
	`, projectID)
	if err != nil {
		return classifyError("load import entitlements", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, key string
		if err := rows.Scan(&id, &key); err != nil {
			return classifyError("scan import entitlement", err)
		}
		entitlement := core.Entitlement{ID: core.EntitlementID(id), ProjectID: projectID, Key: key}
		if err := entitlement.Validate(); err != nil {
			return fmt.Errorf("validate stored import entitlement: %w", err)
		}
		catalog.Entitlements = append(catalog.Entitlements, entitlement)
	}
	return classifyError("iterate import entitlements", rows.Err())
}

// loadImportStoreProducts reads provider product mappings the importer may require.
func (repository *transaction) loadImportStoreProducts(
	ctx context.Context,
	projectID core.ProjectID,
	catalog *persistence.ImportCatalog,
) error {
	rows, err := repository.tx.Query(ctx, `
		SELECT application_id, product_id, provider_product_id
		FROM store_products
		WHERE project_id = $1
		ORDER BY application_id, provider_product_id
	`, projectID)
	if err != nil {
		return classifyError("load import store products", err)
	}
	defer rows.Close()
	for rows.Next() {
		var applicationID, productID, providerProductID string
		if err := rows.Scan(&applicationID, &productID, &providerProductID); err != nil {
			return classifyError("scan import store product", err)
		}
		mapping := core.StoreProduct{
			ApplicationID: core.ApplicationID(applicationID),
			ProductID:     core.ProductID(productID),
			ProviderID:    core.ProviderProductID(providerProductID),
		}
		if err := mapping.Validate(); err != nil {
			return fmt.Errorf("validate stored import store product: %w", err)
		}
		catalog.StoreProducts = append(catalog.StoreProducts, mapping)
	}
	return classifyError("iterate import store products", rows.Err())
}
