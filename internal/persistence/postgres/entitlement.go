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

// entitlementRow contains one projection joined to its public entitlement key.
type entitlementRow struct {
	projectID           string
	customerID          string
	entitlementID       string
	entitlementKey      string
	sourceObservationID string
	sourceApplicationID string
	sourceProductID     string
	access              string
	accessReason        string
	effectiveStartsAt   pgtype.Timestamptz
	effectiveEndsAt     pgtype.Timestamptz
	version             int64
}

// PutEntitlement creates or replaces one current projection with optimistic versioning.
func (repository *transaction) PutEntitlement(
	ctx context.Context,
	projection persistence.EntitlementProjection,
) (persistence.EntitlementWriteResult, error) {
	if err := projection.Validate(); err != nil {
		return persistence.EntitlementWriteResult{}, err
	}

	var version int64
	err := repository.tx.QueryRow(ctx, `
		INSERT INTO customer_entitlements (
			project_id,
			customer_id,
			entitlement_id,
			source_observation_id,
			source_product_id,
			access_status,
			access_reason,
			effective_starts_at,
			effective_ends_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (customer_id, entitlement_id) DO NOTHING
		RETURNING version
	`,
		projection.ProjectID,
		projection.CustomerID,
		projection.EntitlementID,
		projection.SourceObservationID,
		projection.SourceProductID,
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
		SET source_observation_id = $1,
			source_product_id = $2,
			access_status = $3,
			access_reason = $4,
			effective_starts_at = $5,
			effective_ends_at = $6,
			version = version + 1,
			updated_at = now()
		WHERE project_id = $7
			AND customer_id = $8
			AND entitlement_id = $9
			AND version = $10
		RETURNING version
	`,
		projection.SourceObservationID,
		projection.SourceProductID,
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
				persistence.ErrConflict,
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

// CustomerEntitlements returns the current project-scoped entitlement snapshot.
func (repository *transaction) CustomerEntitlements(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) ([]persistence.CustomerEntitlement, error) {
	if err := errors.Join(projectID.Validate(), customerID.Validate()); err != nil {
		return nil, err
	}

	rows, err := repository.tx.Query(ctx, `
		SELECT
			ce.project_id,
			ce.customer_id,
			ce.entitlement_id,
			e.key,
			ce.source_observation_id,
			po.application_id,
			ce.source_product_id,
			ce.access_status,
			ce.access_reason,
			ce.effective_starts_at,
			ce.effective_ends_at,
			ce.version
		FROM customer_entitlements AS ce
		JOIN entitlements AS e
			ON e.id = ce.entitlement_id AND e.project_id = ce.project_id
		JOIN purchase_observations AS po
			ON po.id = ce.source_observation_id
			AND po.customer_id = ce.customer_id
			AND po.project_id = ce.project_id
		WHERE ce.project_id = $1 AND ce.customer_id = $2
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
	err := repository.tx.QueryRow(ctx, `
		SELECT
			ce.project_id,
			ce.customer_id,
			ce.entitlement_id,
			e.key,
			ce.source_observation_id,
			po.application_id,
			ce.source_product_id,
			ce.access_status,
			ce.access_reason,
			ce.effective_starts_at,
			ce.effective_ends_at,
			ce.version
		FROM customer_entitlements AS ce
		JOIN entitlements AS e
			ON e.id = ce.entitlement_id AND e.project_id = ce.project_id
		JOIN purchase_observations AS po
			ON po.id = ce.source_observation_id
			AND po.customer_id = ce.customer_id
			AND po.project_id = ce.project_id
		WHERE ce.project_id = $1 AND ce.customer_id = $2 AND ce.entitlement_id = $3
	`, projectID, customerID, entitlementID).Scan(
		&row.projectID,
		&row.customerID,
		&row.entitlementID,
		&row.entitlementKey,
		&row.sourceObservationID,
		&row.sourceApplicationID,
		&row.sourceProductID,
		&row.access,
		&row.accessReason,
		&row.effectiveStartsAt,
		&row.effectiveEndsAt,
		&row.version,
	)
	if err != nil {
		return persistence.CustomerEntitlement{}, classifyError("load entitlement projection", err)
	}
	return row.entitlement()
}

// scanEntitlementRow scans one projection row from a PostgreSQL result set.
func scanEntitlementRow(rows pgx.Rows) (entitlementRow, error) {
	row := entitlementRow{}
	if err := rows.Scan(
		&row.projectID,
		&row.customerID,
		&row.entitlementID,
		&row.entitlementKey,
		&row.sourceObservationID,
		&row.sourceApplicationID,
		&row.sourceProductID,
		&row.access,
		&row.accessReason,
		&row.effectiveStartsAt,
		&row.effectiveEndsAt,
		&row.version,
	); err != nil {
		return entitlementRow{}, classifyError("scan entitlement projection", err)
	}
	return row, nil
}

// entitlement converts one database row into a validated persistence projection.
func (row entitlementRow) entitlement() (persistence.CustomerEntitlement, error) {
	projection := persistence.EntitlementProjection{
		ProjectID:           core.ProjectID(row.projectID),
		CustomerID:          core.CustomerID(row.customerID),
		EntitlementID:       core.EntitlementID(row.entitlementID),
		SourceObservationID: core.ObservationID(row.sourceObservationID),
		SourceProductID:     core.ProductID(row.sourceProductID),
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
		Key:                 row.entitlementKey,
		Version:             row.version,
	}, nil
}

// projectionsEqual reports whether two logical projections have identical persisted fields.
func projectionsEqual(left, right persistence.EntitlementProjection) bool {
	return left.ProjectID == right.ProjectID &&
		left.CustomerID == right.CustomerID &&
		left.EntitlementID == right.EntitlementID &&
		left.SourceObservationID == right.SourceObservationID &&
		left.SourceProductID == right.SourceProductID &&
		left.Access == right.Access &&
		left.AccessReason == right.AccessReason &&
		normalizeTime(left.EffectivePeriod.StartsAt).Equal(normalizeTime(right.EffectivePeriod.StartsAt)) &&
		timePointersEqual(left.EffectivePeriod.EndsAt, right.EffectivePeriod.EndsAt)
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
