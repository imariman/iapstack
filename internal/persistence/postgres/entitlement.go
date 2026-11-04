package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// observationLineageSQL derives the purchase lineage key of the observation aliased po.
	// Apple and Google Play store a lineage reference. Huawei has none, and its purchase token
	// stays stable across renewals, so the query token is the lineage there.
	observationLineageSQL = `COALESCE((
		SELECT 'reference:' || lineage_reference.id::text
		FROM observation_references AS lineage_link
		JOIN provider_references AS lineage_reference
			ON lineage_reference.id = lineage_link.reference_id
			AND lineage_reference.application_id = lineage_link.application_id
		WHERE lineage_link.observation_id = po.id
			AND (
				lineage_reference.role = 'lineage'
				OR (lineage_reference.role = 'query' AND lineage_reference.kind = 'purchase_token')
			)
		ORDER BY CASE WHEN lineage_reference.role = 'lineage' THEN 0 ELSE 1 END, lineage_reference.id
		LIMIT 1
	), '')`
	// importLineageSQL derives the purchase lineage key of the import grant aliased ig.
	importLineageSQL = `'import:' || encode(ig.lineage_fingerprint, 'hex')`
	// entitlementSelectSQL reads one projection from either its observation or its import grant.
	entitlementSelectSQL = `
		SELECT
			ce.project_id,
			ce.customer_id,
			ce.source_customer_id,
			ce.entitlement_id,
			e.key,
			ce.source_kind,
			ce.source_observation_id,
			ce.source_import_id,
			COALESCE(po.application_id, ig.application_id),
			ce.source_product_id,
			CASE WHEN ce.source_kind = 'import' THEN ` + importLineageSQL + ` ELSE ` + observationLineageSQL + ` END,
			ce.access_status,
			ce.access_reason,
			ce.effective_starts_at,
			ce.effective_ends_at,
			ce.version
		FROM customer_entitlements AS ce
		JOIN entitlements AS e
			ON e.id = ce.entitlement_id AND e.project_id = ce.project_id
		LEFT JOIN purchase_observations AS po
			ON ce.source_kind = 'store_observation'
			AND po.id = ce.source_observation_id
			AND po.customer_id = ce.source_customer_id
			AND po.project_id = ce.project_id
		LEFT JOIN import_grants AS ig
			ON ce.source_kind = 'import'
			AND ig.id = ce.source_import_id
			AND ig.project_id = ce.project_id
			AND ig.customer_id = ce.source_customer_id
			AND ig.product_id = ce.source_product_id
			AND ig.entitlement_id = ce.entitlement_id
		WHERE (
			(ce.source_kind = 'store_observation' AND po.id IS NOT NULL)
			OR (ce.source_kind = 'import' AND ig.id IS NOT NULL)
		) AND `
)

// entitlementRow contains one projection joined to its public entitlement key.
type entitlementRow struct {
	projectID           string
	customerID          string
	sourceCustomerID    string
	entitlementID       string
	entitlementKey      string
	sourceKind          string
	sourceObservationID string
	sourceImportID      string
	sourceApplicationID string
	sourceProductID     string
	sourceLineage       string
	access              string
	accessReason        string
	effectiveStartsAt   pgtype.Timestamptz
	effectiveEndsAt     pgtype.Timestamptz
	version             int64
}

// entitlementSourceRow contains the latest snapshot of one purchase lineage mapped to an entitlement.
type entitlementSourceRow struct {
	projectID           string
	customerID          string
	entitlementID       string
	sourceKind          string
	sourceObservationID string
	sourceImportID      string
	sourceApplicationID string
	sourceProductID     string
	sourceLineage       string
	access              string
	accessReason        string
	effectiveStartsAt   pgtype.Timestamptz
	effectiveEndsAt     pgtype.Timestamptz
	observedAt          time.Time
}

// LockCustomer establishes a write barrier so concurrent serializable projections retry with a fresh snapshot.
func (repository *transaction) LockCustomer(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) error {
	if err := errors.Join(projectID.Validate(), customerID.Validate()); err != nil {
		return err
	}
	var lockedID string
	err := repository.tx.QueryRow(ctx, `
		UPDATE customers
		SET updated_at = updated_at
		WHERE project_id = $1 AND id = $2
		RETURNING id
	`, projectID, customerID).Scan(&lockedID)
	if err != nil {
		return classifyError("lock customer entitlement projection", err)
	}
	return nil
}

// PutEntitlement creates or replaces one current projection with optimistic versioning.
func (repository *transaction) PutEntitlement(
	ctx context.Context,
	projection persistence.EntitlementProjection,
) (persistence.EntitlementWriteResult, error) {
	projection = normalizedProjection(projection)
	if err := projection.Validate(); err != nil {
		return persistence.EntitlementWriteResult{}, err
	}

	var version int64
	err := repository.tx.QueryRow(ctx, `
		INSERT INTO customer_entitlements (
			project_id,
			customer_id,
			entitlement_id,
			source_kind,
			source_observation_id,
			source_import_id,
			source_product_id,
			source_customer_id,
			access_status,
			access_reason,
			effective_starts_at,
			effective_ends_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (customer_id, entitlement_id) DO NOTHING
		RETURNING version
	`,
		projection.ProjectID,
		projection.CustomerID,
		projection.EntitlementID,
		projection.SourceKind,
		nullableText(string(projection.SourceObservationID)),
		nullableText(projection.SourceImportID),
		projection.SourceProductID,
		projection.SourceCustomerID,
		projection.Access,
		projection.AccessReason,
		nullableTime(projection.EffectivePeriod.StartsAt),
		nullableTimePointer(projection.EffectivePeriod.EndsAt),
	).Scan(&version)
	if err == nil {
		entitlement, loadErr := repository.loadCustomerEntitlement(
			ctx,
			projection.ProjectID,
			projection.CustomerID,
			projection.EntitlementID,
		)
		return persistence.EntitlementWriteResult{Entitlement: entitlement, Changed: true}, loadErr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return persistence.EntitlementWriteResult{}, classifyError("create entitlement projection", err)
	}

	current, err := repository.loadCustomerEntitlement(
		ctx,
		projection.ProjectID,
		projection.CustomerID,
		projection.EntitlementID,
	)
	if err != nil {
		return persistence.EntitlementWriteResult{}, err
	}
	if projectionsEqual(current.Projection, projection) {
		return persistence.EntitlementWriteResult{Entitlement: current, Changed: false}, nil
	}

	err = repository.tx.QueryRow(ctx, `
		UPDATE customer_entitlements
		SET source_kind = $1,
			source_observation_id = $2,
			source_import_id = $3,
			source_product_id = $4,
			source_customer_id = $5,
			access_status = $6,
			access_reason = $7,
			effective_starts_at = $8,
			effective_ends_at = $9,
			version = version + 1,
			updated_at = now()
		WHERE project_id = $10
			AND customer_id = $11
			AND entitlement_id = $12
			AND version = $13
		RETURNING version
	`,
		projection.SourceKind,
		nullableText(string(projection.SourceObservationID)),
		nullableText(projection.SourceImportID),
		projection.SourceProductID,
		projection.SourceCustomerID,
		projection.Access,
		projection.AccessReason,
		nullableTime(projection.EffectivePeriod.StartsAt),
		nullableTimePointer(projection.EffectivePeriod.EndsAt),
		projection.ProjectID,
		projection.CustomerID,
		projection.EntitlementID,
		current.Version,
	).Scan(&version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return persistence.EntitlementWriteResult{}, fmt.Errorf(
				"replace entitlement projection: %w",
				errRetryableTransaction,
			)
		}
		return persistence.EntitlementWriteResult{}, classifyError("replace entitlement projection", err)
	}
	entitlement, err := repository.loadCustomerEntitlement(
		ctx,
		projection.ProjectID,
		projection.CustomerID,
		projection.EntitlementID,
	)
	return persistence.EntitlementWriteResult{Entitlement: entitlement, Changed: true}, err
}

// CustomerEntitlementSources returns the latest observation or import grant for every purchase lineage
// of every application product in the customer closure.
// Observation times are compared only inside one lineage, so a refreshed expired lineage cannot hide a
// later purchase of the same product. Equal times prefer an allowed snapshot, so a merged source grant
// is not hidden by a denied twin.
func (repository *transaction) CustomerEntitlementSources(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) ([]persistence.EntitlementSource, error) {
	if err := errors.Join(projectID.Validate(), customerID.Validate()); err != nil {
		return nil, err
	}

	canonicalID, members, err := repository.customerClosure(ctx, projectID, customerID)
	if err != nil {
		return nil, err
	}
	rows, err := repository.tx.Query(ctx, `
		SELECT project_id, customer_id, entitlement_id, source_kind, source_observation_id,
			source_import_id, application_id, product_id, source_lineage, access_status, access_reason,
			effective_starts_at, effective_ends_at, observed_at
		FROM (
			SELECT DISTINCT ON (pe.entitlement_id, po.application_id, po.product_id, lineage.source_lineage)
				po.project_id,
				po.customer_id,
				pe.entitlement_id,
				'store_observation'::text AS source_kind,
				po.id AS source_observation_id,
				NULL::text AS source_import_id,
				po.application_id,
				po.product_id,
				lineage.source_lineage,
				po.access_status,
				po.access_reason,
				po.effective_starts_at,
				po.effective_ends_at,
				po.observed_at
			FROM purchase_observations AS po
			JOIN product_entitlements AS pe
				ON pe.project_id = po.project_id AND pe.product_id = po.product_id
			CROSS JOIN LATERAL (SELECT `+observationLineageSQL+` AS source_lineage) AS lineage
			WHERE po.project_id = $1 AND po.customer_id = ANY($2)
			ORDER BY pe.entitlement_id, po.application_id, po.product_id, lineage.source_lineage,
				po.observed_at DESC,
				CASE WHEN po.access_status = 'allowed' THEN 0 ELSE 1 END,
				po.id ASC
		) AS observations
		UNION ALL
		SELECT project_id, customer_id, entitlement_id, source_kind, source_observation_id,
			source_import_id, application_id, product_id, source_lineage, access_status, access_reason,
			effective_starts_at, effective_ends_at, observed_at
		FROM (
			SELECT DISTINCT ON (ig.entitlement_id, ig.application_id, ig.product_id, ig.lineage_fingerprint)
				ig.project_id,
				ig.customer_id,
				ig.entitlement_id,
				'import'::text AS source_kind,
				NULL::text AS source_observation_id,
				ig.id AS source_import_id,
				ig.application_id,
				ig.product_id,
				`+importLineageSQL+` AS source_lineage,
				ig.access_status,
				ig.access_reason,
				ig.effective_starts_at,
				ig.effective_ends_at,
				ig.observed_at
			FROM import_grants AS ig
			WHERE ig.project_id = $1 AND ig.customer_id = ANY($2)
			ORDER BY ig.entitlement_id, ig.application_id, ig.product_id, ig.lineage_fingerprint,
				ig.observed_at DESC,
				ig.id ASC
		) AS imports
	`, projectID, members)
	if err != nil {
		return nil, classifyError("load customer entitlement sources", err)
	}
	defer rows.Close()

	sources := make([]persistence.EntitlementSource, 0)
	for rows.Next() {
		row, err := scanEntitlementSourceRow(rows)
		if err != nil {
			return nil, err
		}
		source, err := row.entitlementSource()
		if err != nil {
			return nil, err
		}
		source.Projection.CustomerID = canonicalID
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate customer entitlement sources", err)
	}
	return sources, nil
}

// CustomerEntitlements returns the current project-scoped entitlement snapshot.
func (repository *transaction) CustomerEntitlements(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) ([]persistence.CustomerEntitlement, error) {
	if err := errors.Join(projectID.Validate(), customerID.Validate()); err != nil {
		return nil, err
	}

	rows, err := repository.tx.Query(ctx, entitlementSelectSQL+`
		ce.project_id = $1 AND ce.customer_id = $2
		ORDER BY e.key
	`, projectID, customerID)
	if err != nil {
		return nil, classifyError("load customer entitlements", err)
	}
	defer rows.Close()

	entitlements := make([]persistence.CustomerEntitlement, 0)
	for rows.Next() {
		row, err := scanEntitlementRow(rows)
		if err != nil {
			return nil, err
		}
		entitlement, err := row.entitlement()
		if err != nil {
			return nil, err
		}
		entitlements = append(entitlements, entitlement)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate customer entitlements", err)
	}
	return entitlements, nil
}

// loadCustomerEntitlement returns one current projection joined to its entitlement key.
func (repository *transaction) loadCustomerEntitlement(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
	entitlementID core.EntitlementID,
) (persistence.CustomerEntitlement, error) {
	row := entitlementRow{}
	var observationID pgtype.Text
	var importID pgtype.Text
	err := repository.tx.QueryRow(ctx, entitlementSelectSQL+`
		ce.project_id = $1 AND ce.customer_id = $2 AND ce.entitlement_id = $3
	`, projectID, customerID, entitlementID).Scan(
		&row.projectID,
		&row.customerID,
		&row.sourceCustomerID,
		&row.entitlementID,
		&row.entitlementKey,
		&row.sourceKind,
		&observationID,
		&importID,
		&row.sourceApplicationID,
		&row.sourceProductID,
		&row.sourceLineage,
		&row.access,
		&row.accessReason,
		&row.effectiveStartsAt,
		&row.effectiveEndsAt,
		&row.version,
	)
	row.sourceObservationID = textValue(observationID)
	row.sourceImportID = textValue(importID)
	if err != nil {
		return persistence.CustomerEntitlement{}, classifyError("load entitlement projection", err)
	}
	return row.entitlement()
}

// scanEntitlementRow scans one projection row from a PostgreSQL result set.
func scanEntitlementRow(rows pgx.Rows) (entitlementRow, error) {
	row := entitlementRow{}
	var observationID pgtype.Text
	var importID pgtype.Text
	if err := rows.Scan(
		&row.projectID,
		&row.customerID,
		&row.sourceCustomerID,
		&row.entitlementID,
		&row.entitlementKey,
		&row.sourceKind,
		&observationID,
		&importID,
		&row.sourceApplicationID,
		&row.sourceProductID,
		&row.sourceLineage,
		&row.access,
		&row.accessReason,
		&row.effectiveStartsAt,
		&row.effectiveEndsAt,
		&row.version,
	); err != nil {
		return entitlementRow{}, classifyError("scan entitlement projection", err)
	}
	row.sourceObservationID = textValue(observationID)
	row.sourceImportID = textValue(importID)
	return row, nil
}

// scanEntitlementSourceRow scans the latest source projection of one lineage from a PostgreSQL result set.
func scanEntitlementSourceRow(rows pgx.Rows) (entitlementSourceRow, error) {
	row := entitlementSourceRow{}
	var observationID pgtype.Text
	var importID pgtype.Text
	if err := rows.Scan(
		&row.projectID,
		&row.customerID,
		&row.entitlementID,
		&row.sourceKind,
		&observationID,
		&importID,
		&row.sourceApplicationID,
		&row.sourceProductID,
		&row.sourceLineage,
		&row.access,
		&row.accessReason,
		&row.effectiveStartsAt,
		&row.effectiveEndsAt,
		&row.observedAt,
	); err != nil {
		return entitlementSourceRow{}, classifyError("scan entitlement source", err)
	}
	row.sourceObservationID = textValue(observationID)
	row.sourceImportID = textValue(importID)
	return row, nil
}

// entitlement converts one database row into a validated persistence projection.
func (row entitlementRow) entitlement() (persistence.CustomerEntitlement, error) {
	projection := persistence.EntitlementProjection{
		ProjectID:           core.ProjectID(row.projectID),
		CustomerID:          core.CustomerID(row.customerID),
		EntitlementID:       core.EntitlementID(row.entitlementID),
		SourceKind:          persistence.SourceKind(row.sourceKind),
		SourceObservationID: core.ObservationID(row.sourceObservationID),
		SourceImportID:      row.sourceImportID,
		SourceProductID:     core.ProductID(row.sourceProductID),
		SourceCustomerID:    core.CustomerID(row.sourceCustomerID),
		Access:              core.AccessStatus(row.access),
		AccessReason:        core.AccessReason(row.accessReason),
		EffectivePeriod: core.EffectivePeriod{
			StartsAt: timestampValue(row.effectiveStartsAt),
			EndsAt:   timestampPointer(row.effectiveEndsAt),
		},
	}
	if err := errors.Join(
		projection.Validate(),
		core.ApplicationID(row.sourceApplicationID).Validate(),
		validateText("stored entitlement key", row.entitlementKey),
	); err != nil {
		return persistence.CustomerEntitlement{}, fmt.Errorf("validate stored entitlement projection: %w", err)
	}
	if row.version <= 0 {
		return persistence.CustomerEntitlement{}, errors.New("stored entitlement projection version must be positive")
	}
	return persistence.CustomerEntitlement{
		Projection:          projection,
		SourceApplicationID: core.ApplicationID(row.sourceApplicationID),
		SourceLineage:       row.sourceLineage,
		Key:                 row.entitlementKey,
		Version:             row.version,
	}, nil
}

// entitlementSource converts one source row into a validated persistence candidate.
func (row entitlementSourceRow) entitlementSource() (persistence.EntitlementSource, error) {
	source := persistence.EntitlementSource{
		Projection: persistence.EntitlementProjection{
			ProjectID:           core.ProjectID(row.projectID),
			CustomerID:          core.CustomerID(row.customerID),
			EntitlementID:       core.EntitlementID(row.entitlementID),
			SourceKind:          persistence.SourceKind(row.sourceKind),
			SourceObservationID: core.ObservationID(row.sourceObservationID),
			SourceImportID:      row.sourceImportID,
			SourceProductID:     core.ProductID(row.sourceProductID),
			SourceCustomerID:    core.CustomerID(row.customerID),
			Access:              core.AccessStatus(row.access),
			AccessReason:        core.AccessReason(row.accessReason),
			EffectivePeriod: core.EffectivePeriod{
				StartsAt: timestampValue(row.effectiveStartsAt),
				EndsAt:   timestampPointer(row.effectiveEndsAt),
			},
		},
		SourceApplicationID: core.ApplicationID(row.sourceApplicationID),
		SourceLineage:       row.sourceLineage,
		ObservedAt:          row.observedAt,
	}
	if err := source.Validate(); err != nil {
		return persistence.EntitlementSource{}, fmt.Errorf("validate stored entitlement source: %w", err)
	}
	return source, nil
}

// projectionsEqual reports whether two logical projections have identical persisted fields.
func projectionsEqual(left, right persistence.EntitlementProjection) bool {
	left = normalizedProjection(left)
	right = normalizedProjection(right)
	return left.ProjectID == right.ProjectID &&
		left.CustomerID == right.CustomerID &&
		left.EntitlementID == right.EntitlementID &&
		left.SourceKind == right.SourceKind &&
		left.SourceObservationID == right.SourceObservationID &&
		left.SourceImportID == right.SourceImportID &&
		left.SourceProductID == right.SourceProductID &&
		left.SourceCustomerID == right.SourceCustomerID &&
		left.Access == right.Access &&
		left.AccessReason == right.AccessReason &&
		normalizeTime(left.EffectivePeriod.StartsAt).Equal(normalizeTime(right.EffectivePeriod.StartsAt)) &&
		timePointersEqual(left.EffectivePeriod.EndsAt, right.EffectivePeriod.EndsAt)
}

// normalizedProjection stores an omitted source customer as the projection owner.
func normalizedProjection(projection persistence.EntitlementProjection) persistence.EntitlementProjection {
	return projection.Normalized()
}

// nullableText converts an empty identifier into SQL NULL.
func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// textValue reads a nullable PostgreSQL string.
func textValue(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

// timePointersEqual compares optional timestamps at PostgreSQL precision.
func timePointersEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return normalizeTime(*left).Equal(normalizeTime(*right))
}

// timestampValue converts a nullable PostgreSQL timestamp into a domain value.
func timestampValue(timestamp pgtype.Timestamptz) time.Time {
	if !timestamp.Valid {
		return time.Time{}
	}
	return timestamp.Time
}

// timestampPointer converts a nullable PostgreSQL timestamp into an optional domain value.
func timestampPointer(timestamp pgtype.Timestamptz) *time.Time {
	if !timestamp.Valid {
		return nil
	}
	value := timestamp.Time
	return &value
}
