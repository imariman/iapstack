package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// ReprojectionProjects returns every project id in id order.
func (repository *transaction) ReprojectionProjects(ctx context.Context) ([]core.ProjectID, error) {
	rows, err := repository.tx.Query(ctx, `SELECT id FROM projects ORDER BY id`)
	if err != nil {
		return nil, classifyError("list reprojection projects", err)
	}
	defer rows.Close()
	projects := make([]core.ProjectID, 0)
	for rows.Next() {
		var id core.ProjectID
		if err := rows.Scan(&id); err != nil {
			return nil, classifyError("scan reprojection project", err)
		}
		projects = append(projects, id)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate reprojection projects", err)
	}
	return projects, nil
}

// ReprojectionCustomers examines one keyset page of canonical customers in id order and returns
// the ones it selects. Unless the page selects every customer, it keeps only customers whose
// closure holds an allowed import grant and an allowed store observation of another application
// product that grants the same entitlement. That pair is the only one the projector ranks
// differently since the allowed end is compared before the source kind, so other customers cannot
// hold a stale projection from it. The filter runs only on the page's examined customers, so a
// page costs the same however few customers match.
func (repository *transaction) ReprojectionCustomers(
	ctx context.Context,
	page persistence.ReprojectionPage,
) (persistence.ReprojectionPageResult, error) {
	if err := page.Validate(); err != nil {
		return persistence.ReprojectionPageResult{}, err
	}
	rows, err := repository.tx.Query(ctx, `
		WITH examined AS (
			SELECT c.id, c.project_id
			FROM customers AS c
			WHERE c.project_id = $1
				AND c.merged_into_customer_id IS NULL
				AND c.id > $2
			ORDER BY c.id
			LIMIT $3
		)
		SELECT examined.id, (
			$4::boolean
			OR EXISTS (
				SELECT 1
				FROM customers AS import_member
				JOIN import_grants AS ig
					ON ig.project_id = import_member.project_id
					AND ig.customer_id = import_member.id
					AND ig.access_status = 'allowed'
				JOIN product_entitlements AS pe
					ON pe.project_id = ig.project_id
					AND pe.entitlement_id = ig.entitlement_id
				JOIN customers AS store_member
					ON store_member.project_id = examined.project_id
					AND (store_member.id = examined.id OR store_member.merged_into_customer_id = examined.id)
				JOIN purchase_observations AS po
					ON po.project_id = pe.project_id
					AND po.customer_id = store_member.id
					AND po.product_id = pe.product_id
					AND po.access_status = 'allowed'
				WHERE import_member.project_id = examined.project_id
					AND (import_member.id = examined.id OR import_member.merged_into_customer_id = examined.id)
					AND (po.application_id <> ig.application_id OR po.product_id <> ig.product_id)
			)
		) AS selected
		FROM examined
		ORDER BY examined.id
	`, page.ProjectID, string(page.AfterCustomerID), page.Limit, page.AllCustomers)
	if err != nil {
		return persistence.ReprojectionPageResult{}, classifyError("list reprojection customers", err)
	}
	defer rows.Close()
	result := persistence.ReprojectionPageResult{CustomerIDs: make([]core.CustomerID, 0, page.Limit)}
	examined := 0
	var last core.CustomerID
	for rows.Next() {
		var id core.CustomerID
		var selected bool
		if err := rows.Scan(&id, &selected); err != nil {
			return persistence.ReprojectionPageResult{}, classifyError("scan reprojection customer", err)
		}
		examined++
		last = id
		if selected {
			result.CustomerIDs = append(result.CustomerIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		return persistence.ReprojectionPageResult{}, classifyError("iterate reprojection customers", err)
	}
	if examined == page.Limit {
		result.NextAfterCustomerID = last
	}
	return result, nil
}

// CustomerIsCanonical reports whether one customer exists and has not been merged into another.
func (repository *transaction) CustomerIsCanonical(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (bool, error) {
	identity, err := repository.customerIdentityByID(ctx, projectID, customerID)
	if errors.Is(err, persistence.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check canonical customer: %w", err)
	}
	return identity.mergedInto == nil, nil
}
