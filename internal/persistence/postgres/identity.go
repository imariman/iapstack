package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// customerIdentity is one customers row used by alias and merge decisions.
type customerIdentity struct {
	id         core.CustomerID
	projectID  core.ProjectID
	externalID string
	mergedInto *string
}

// ResolveCustomer maps one external id to its canonical customer inside one project.
func (repository *transaction) ResolveCustomer(
	ctx context.Context,
	projectID core.ProjectID,
	externalID string,
) (persistence.CustomerResolution, error) {
	if err := errors.Join(projectID.Validate(), core.ValidateExternalCustomerID(externalID)); err != nil {
		return persistence.CustomerResolution{}, err
	}
	direct, err := repository.customerIdentityByExternalID(ctx, projectID, externalID)
	if err == nil {
		return repository.resolutionFromCustomer(ctx, projectID, externalID, direct)
	}
	if !errors.Is(err, persistence.ErrNotFound) {
		return persistence.CustomerResolution{}, err
	}

	var canonicalID string
	err = repository.tx.QueryRow(ctx, `
		SELECT canonical_customer_id
		FROM customer_aliases
		WHERE project_id = $1 AND external_id = $2
	`, projectID, externalID).Scan(&canonicalID)
	if err != nil {
		return persistence.CustomerResolution{}, classifyError("load customer alias", err)
	}
	canonical, err := repository.customerIdentityByID(ctx, projectID, core.CustomerID(canonicalID))
	if err != nil {
		return persistence.CustomerResolution{}, err
	}
	if canonical.mergedInto != nil {
		return persistence.CustomerResolution{}, fmt.Errorf("resolve customer alias: %w", persistence.ErrConflict)
	}
	resolution := persistence.CustomerResolution{
		ProjectID:           projectID,
		ExternalID:          externalID,
		CanonicalCustomerID: canonical.id,
		Match:               persistence.CustomerMatchAlias,
	}
	if err := resolution.Validate(); err != nil {
		return persistence.CustomerResolution{}, fmt.Errorf("validate customer resolution: %w", err)
	}
	return resolution, nil
}

// PutCustomerAlias attaches one external id to a canonical customer idempotently.
func (repository *transaction) PutCustomerAlias(
	ctx context.Context,
	write persistence.CustomerAliasWrite,
) (persistence.CustomerResolution, error) {
	if err := write.Validate(); err != nil {
		return persistence.CustomerResolution{}, err
	}
	if err := repository.lockExternalCustomerID(ctx, write.ProjectID, write.ExternalID); err != nil {
		return persistence.CustomerResolution{}, err
	}
	if err := repository.LockCustomer(ctx, write.ProjectID, write.CanonicalCustomerID); err != nil {
		return persistence.CustomerResolution{}, err
	}
	canonical, err := repository.customerIdentityByID(ctx, write.ProjectID, write.CanonicalCustomerID)
	if err != nil {
		return persistence.CustomerResolution{}, err
	}
	if canonical.mergedInto != nil {
		return persistence.CustomerResolution{}, fmt.Errorf("put customer alias: %w", persistence.ErrConflict)
	}
	if canonical.externalID == write.ExternalID {
		return customerResolution(write.ProjectID, write.ExternalID, canonical.id, persistence.CustomerMatchPrimary), nil
	}

	resolved, err := repository.ResolveCustomer(ctx, write.ProjectID, write.ExternalID)
	if err == nil {
		if resolved.CanonicalCustomerID != write.CanonicalCustomerID {
			return persistence.CustomerResolution{}, fmt.Errorf("put customer alias: %w", persistence.ErrConflict)
		}
		return resolved, nil
	}
	if !errors.Is(err, persistence.ErrNotFound) {
		return persistence.CustomerResolution{}, err
	}

	_, err = repository.tx.Exec(ctx, `
		INSERT INTO customer_aliases (
			project_id, external_id, canonical_customer_id, actor_role, actor_key_id
		) VALUES ($1, $2, $3, $4, $5)
	`, write.ProjectID, write.ExternalID, write.CanonicalCustomerID, write.ActorRole, write.ActorKeyID)
	if err != nil {
		return persistence.CustomerResolution{}, classifyError("put customer alias", err)
	}
	return customerResolution(write.ProjectID, write.ExternalID, write.CanonicalCustomerID, persistence.CustomerMatchAlias), nil
}

// MergeCustomers records one customer merge or returns the stored audit row.
func (repository *transaction) MergeCustomers(
	ctx context.Context,
	write persistence.CustomerMergeWrite,
) (persistence.CustomerMergeResult, error) {
	if err := write.Validate(); err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	if write.CanonicalCustomerID == write.SourceCustomerID {
		return persistence.CustomerMergeResult{}, fmt.Errorf("merge customers: %w", persistence.ErrConflict)
	}
	if err := repository.lockCustomersInOrder(ctx, write.ProjectID, write.CanonicalCustomerID, write.SourceCustomerID); err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	canonical, err := repository.customerIdentityByID(ctx, write.ProjectID, write.CanonicalCustomerID)
	if err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	source, err := repository.customerIdentityByID(ctx, write.ProjectID, write.SourceCustomerID)
	if err != nil {
		return persistence.CustomerMergeResult{}, err
	}

	byKey, err := repository.customerMergeByIdempotencyKey(ctx, write.ProjectID, write.IdempotencyKey)
	if err != nil && !errors.Is(err, persistence.ErrNotFound) {
		return persistence.CustomerMergeResult{}, err
	}
	if err == nil {
		if byKey.CanonicalCustomerID != write.CanonicalCustomerID || byKey.SourceCustomerID != write.SourceCustomerID {
			return persistence.CustomerMergeResult{}, fmt.Errorf("merge customers: %w", persistence.ErrConflict)
		}
		return persistence.CustomerMergeResult{Merge: byKey, Replay: true}, nil
	}

	bySource, err := repository.customerMergeBySource(ctx, write.SourceCustomerID)
	if err != nil && !errors.Is(err, persistence.ErrNotFound) {
		return persistence.CustomerMergeResult{}, err
	}
	if err == nil {
		if bySource.ProjectID != write.ProjectID || bySource.CanonicalCustomerID != write.CanonicalCustomerID {
			return persistence.CustomerMergeResult{}, fmt.Errorf("merge customers: %w", persistence.ErrConflict)
		}
		return persistence.CustomerMergeResult{Merge: bySource, Replay: true}, nil
	}

	if canonical.mergedInto != nil || source.mergedInto != nil {
		return persistence.CustomerMergeResult{}, fmt.Errorf("merge customers: %w", persistence.ErrConflict)
	}
	ownsAlias, err := repository.customerOwnsAlias(ctx, write.ProjectID, write.SourceCustomerID)
	if err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	ownsMerge, err := repository.customerOwnsMerge(ctx, write.ProjectID, write.SourceCustomerID)
	if err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	if ownsAlias || ownsMerge {
		return persistence.CustomerMergeResult{}, fmt.Errorf("merge customers: %w", persistence.ErrConflict)
	}
	conflict, err := repository.customersHaveEntitlementConflict(ctx, write.ProjectID, write.CanonicalCustomerID, write.SourceCustomerID)
	if err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	if conflict {
		return persistence.CustomerMergeResult{}, fmt.Errorf("merge customers: %w", persistence.ErrEntitlementConflict)
	}

	command, err := repository.tx.Exec(ctx, `
		UPDATE customers
		SET merged_into_customer_id = $1, updated_at = now()
		WHERE project_id = $2 AND id = $3 AND merged_into_customer_id IS NULL
	`, write.CanonicalCustomerID, write.ProjectID, write.SourceCustomerID)
	if err != nil {
		return persistence.CustomerMergeResult{}, classifyError("merge customer", err)
	}
	if command.RowsAffected() != 1 {
		return persistence.CustomerMergeResult{}, fmt.Errorf("merge customer: %w", persistence.ErrConflict)
	}

	mergeID, err := newCustomerMergeID()
	if err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	merge := persistence.CustomerMerge{
		ID:                  mergeID,
		ProjectID:           write.ProjectID,
		CanonicalCustomerID: write.CanonicalCustomerID,
		SourceCustomerID:    write.SourceCustomerID,
		IdempotencyKey:      write.IdempotencyKey,
	}
	err = repository.tx.QueryRow(ctx, `
		INSERT INTO customer_merges (
			id, project_id, canonical_customer_id, source_customer_id, idempotency_key, actor_key_id
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at
	`, merge.ID, merge.ProjectID, merge.CanonicalCustomerID, merge.SourceCustomerID, merge.IdempotencyKey, write.ActorKeyID).Scan(&merge.CreatedAt)
	if err != nil {
		return persistence.CustomerMergeResult{}, classifyError("insert customer merge", err)
	}
	if err := merge.Validate(); err != nil {
		return persistence.CustomerMergeResult{}, fmt.Errorf("validate customer merge: %w", err)
	}
	if err := repository.LockCustomerClosure(ctx, write.ProjectID, write.CanonicalCustomerID); err != nil {
		return persistence.CustomerMergeResult{}, err
	}
	return persistence.CustomerMergeResult{Merge: merge, Replay: false}, nil
}

// CustomerInClosure reports whether candidate is the canonical customer or merged directly into it.
func (repository *transaction) CustomerInClosure(
	ctx context.Context,
	projectID core.ProjectID,
	canonicalID core.CustomerID,
	candidateID core.CustomerID,
) (bool, error) {
	if err := errors.Join(projectID.Validate(), canonicalID.Validate(), candidateID.Validate()); err != nil {
		return false, err
	}
	if candidateID == canonicalID {
		_, err := repository.customerIdentityByID(ctx, projectID, canonicalID)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	candidate, err := repository.customerIdentityByID(ctx, projectID, candidateID)
	if errors.Is(err, persistence.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return candidate.mergedInto != nil && core.CustomerID(*candidate.mergedInto) == canonicalID, nil
}

// LockCustomerClosure serializes one canonical customer and every customer merged directly into it.
func (repository *transaction) LockCustomerClosure(
	ctx context.Context,
	projectID core.ProjectID,
	canonicalID core.CustomerID,
) error {
	if err := errors.Join(projectID.Validate(), canonicalID.Validate()); err != nil {
		return err
	}
	rows, err := repository.tx.Query(ctx, `
		SELECT id
		FROM customers
		WHERE project_id = $1 AND (id = $2 OR merged_into_customer_id = $2)
		ORDER BY id
	`, projectID, canonicalID)
	if err != nil {
		return classifyError("list customer closure", err)
	}
	defer rows.Close()
	ids := make([]core.CustomerID, 0)
	for rows.Next() {
		var id core.CustomerID
		if err := rows.Scan(&id); err != nil {
			return classifyError("scan customer closure", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return classifyError("iterate customer closure", err)
	}
	if len(ids) == 0 {
		return fmt.Errorf("lock customer closure: %w", persistence.ErrNotFound)
	}
	for _, id := range ids {
		if err := repository.LockCustomer(ctx, projectID, id); err != nil {
			return err
		}
	}
	return nil
}

// customerClosure returns the canonical id and every customer merged directly into it.
func (repository *transaction) customerClosure(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (core.CustomerID, []string, error) {
	identity, err := repository.customerIdentityByID(ctx, projectID, customerID)
	if errors.Is(err, persistence.ErrNotFound) {
		return customerID, []string{string(customerID)}, nil
	}
	if err != nil {
		return "", nil, err
	}
	canonicalID := identity.id
	if identity.mergedInto != nil {
		target, err := repository.customerIdentityByID(ctx, projectID, core.CustomerID(*identity.mergedInto))
		if err != nil {
			return "", nil, err
		}
		if target.mergedInto != nil {
			return "", nil, fmt.Errorf("resolve customer closure: %w", persistence.ErrConflict)
		}
		canonicalID = target.id
	}
	rows, err := repository.tx.Query(ctx, `
		SELECT id
		FROM customers
		WHERE project_id = $1 AND (id = $2 OR merged_into_customer_id = $2)
		ORDER BY id
	`, projectID, canonicalID)
	if err != nil {
		return "", nil, classifyError("list customer closure", err)
	}
	defer rows.Close()
	members := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", nil, classifyError("scan customer closure", err)
		}
		members = append(members, id)
	}
	if err := rows.Err(); err != nil {
		return "", nil, classifyError("iterate customer closure", err)
	}
	if len(members) == 0 {
		members = []string{string(canonicalID)}
	}
	return canonicalID, members, nil
}

// resolutionFromCustomer follows one merge hop from a customers row.
func (repository *transaction) resolutionFromCustomer(
	ctx context.Context,
	projectID core.ProjectID,
	externalID string,
	identity customerIdentity,
) (persistence.CustomerResolution, error) {
	if identity.mergedInto == nil {
		return customerResolution(projectID, externalID, identity.id, persistence.CustomerMatchPrimary), nil
	}
	target, err := repository.customerIdentityByID(ctx, projectID, core.CustomerID(*identity.mergedInto))
	if err != nil {
		return persistence.CustomerResolution{}, err
	}
	if target.mergedInto != nil {
		return persistence.CustomerResolution{}, fmt.Errorf("resolve merged customer: %w", persistence.ErrConflict)
	}
	return customerResolution(projectID, externalID, target.id, persistence.CustomerMatchMerged), nil
}

// customerResolution builds one validated resolution value.
func customerResolution(
	projectID core.ProjectID,
	externalID string,
	canonicalID core.CustomerID,
	match string,
) persistence.CustomerResolution {
	return persistence.CustomerResolution{
		ProjectID:           projectID,
		ExternalID:          externalID,
		CanonicalCustomerID: canonicalID,
		Match:               match,
	}
}

// customerByExternalIDDirect reads customers without following aliases or merges.
func (repository *transaction) customerByExternalIDDirect(
	ctx context.Context,
	projectID core.ProjectID,
	externalID string,
) (core.Customer, error) {
	identity, err := repository.customerIdentityByExternalID(ctx, projectID, externalID)
	if err != nil {
		return core.Customer{}, err
	}
	return identity.customer(), nil
}

// customerIdentityByExternalID loads one customers row by its stored external id.
func (repository *transaction) customerIdentityByExternalID(
	ctx context.Context,
	projectID core.ProjectID,
	externalID string,
) (customerIdentity, error) {
	identity := customerIdentity{projectID: projectID}
	var id string
	err := repository.tx.QueryRow(ctx, `
		SELECT id, external_id, merged_into_customer_id
		FROM customers
		WHERE project_id = $1 AND external_id = $2
	`, projectID, externalID).Scan(&id, &identity.externalID, &identity.mergedInto)
	if err != nil {
		return customerIdentity{}, classifyError("load customer", err)
	}
	identity.id = core.CustomerID(id)
	if err := identity.customer().Validate(); err != nil {
		return customerIdentity{}, fmt.Errorf("validate stored customer: %w", err)
	}
	return identity, nil
}

// customerIdentityByID loads one customers row without resolving aliases.
func (repository *transaction) customerIdentityByID(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (customerIdentity, error) {
	identity := customerIdentity{projectID: projectID}
	var id string
	err := repository.tx.QueryRow(ctx, `
		SELECT id, external_id, merged_into_customer_id
		FROM customers
		WHERE project_id = $1 AND id = $2
	`, projectID, customerID).Scan(&id, &identity.externalID, &identity.mergedInto)
	if err != nil {
		return customerIdentity{}, classifyError("load customer", err)
	}
	identity.id = core.CustomerID(id)
	if err := identity.customer().Validate(); err != nil {
		return customerIdentity{}, fmt.Errorf("validate stored customer: %w", err)
	}
	return identity, nil
}

// customer converts one identity row into the canonical domain customer.
func (identity customerIdentity) customer() core.Customer {
	return core.Customer{ID: identity.id, ProjectID: identity.projectID, ExternalID: identity.externalID}
}

// customerAliasExists reports whether an external id is already an alias in the project.
func (repository *transaction) customerAliasExists(
	ctx context.Context,
	projectID core.ProjectID,
	externalID string,
) (bool, error) {
	var exists bool
	err := repository.tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM customer_aliases WHERE project_id = $1 AND external_id = $2
		)
	`, projectID, externalID).Scan(&exists)
	if err != nil {
		return false, classifyError("load customer alias", err)
	}
	return exists, nil
}

// customerOwnsAlias reports whether any alias already points at the customer.
func (repository *transaction) customerOwnsAlias(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (bool, error) {
	var exists bool
	err := repository.tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM customer_aliases
			WHERE project_id = $1 AND canonical_customer_id = $2
		)
	`, projectID, customerID).Scan(&exists)
	if err != nil {
		return false, classifyError("load customer alias ownership", err)
	}
	return exists, nil
}

// customerOwnsMerge reports whether any customer is already merged into this customer.
func (repository *transaction) customerOwnsMerge(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (bool, error) {
	var exists bool
	err := repository.tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM customers
			WHERE project_id = $1 AND merged_into_customer_id = $2
		)
	`, projectID, customerID).Scan(&exists)
	if err != nil {
		return false, classifyError("load customer merge ownership", err)
	}
	return exists, nil
}

// customersHaveEntitlementConflict reports dual allowed access for one entitlement id.
func (repository *transaction) customersHaveEntitlementConflict(
	ctx context.Context,
	projectID core.ProjectID,
	canonicalID core.CustomerID,
	sourceID core.CustomerID,
) (bool, error) {
	var conflict bool
	err := repository.tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM customer_entitlements AS canonical
			JOIN customer_entitlements AS source
				ON source.project_id = canonical.project_id
				AND source.entitlement_id = canonical.entitlement_id
			WHERE canonical.project_id = $1
				AND canonical.customer_id = $2
				AND source.customer_id = $3
				AND canonical.access_status = 'allowed'
				AND source.access_status = 'allowed'
		)
	`, projectID, canonicalID, sourceID).Scan(&conflict)
	if err != nil {
		return false, classifyError("compare customer entitlements", err)
	}
	return conflict, nil
}

// customerMergeByIdempotencyKey loads one audit row by its project-scoped key.
func (repository *transaction) customerMergeByIdempotencyKey(
	ctx context.Context,
	projectID core.ProjectID,
	idempotencyKey string,
) (persistence.CustomerMerge, error) {
	return repository.scanCustomerMerge(ctx, `
		SELECT id, project_id, canonical_customer_id, source_customer_id, idempotency_key, created_at
		FROM customer_merges
		WHERE project_id = $1 AND idempotency_key = $2
	`, projectID, idempotencyKey)
}

// customerMergeBySource loads the only audit row for one source customer.
func (repository *transaction) customerMergeBySource(
	ctx context.Context,
	sourceID core.CustomerID,
) (persistence.CustomerMerge, error) {
	return repository.scanCustomerMerge(ctx, `
		SELECT id, project_id, canonical_customer_id, source_customer_id, idempotency_key, created_at
		FROM customer_merges
		WHERE source_customer_id = $1
	`, sourceID)
}

// scanCustomerMerge reads one customer merge audit row.
func (repository *transaction) scanCustomerMerge(
	ctx context.Context,
	query string,
	args ...any,
) (persistence.CustomerMerge, error) {
	merge := persistence.CustomerMerge{}
	err := repository.tx.QueryRow(ctx, query, args...).Scan(
		&merge.ID,
		&merge.ProjectID,
		&merge.CanonicalCustomerID,
		&merge.SourceCustomerID,
		&merge.IdempotencyKey,
		&merge.CreatedAt,
	)
	if err != nil {
		return persistence.CustomerMerge{}, classifyError("load customer merge", err)
	}
	if err := merge.Validate(); err != nil {
		return persistence.CustomerMerge{}, fmt.Errorf("validate stored customer merge: %w", err)
	}
	return merge, nil
}

// lockCustomersInOrder acquires customer write barriers in lexicographic id order.
func (repository *transaction) lockCustomersInOrder(
	ctx context.Context,
	projectID core.ProjectID,
	left core.CustomerID,
	right core.CustomerID,
) error {
	ids := []core.CustomerID{left, right}
	sort.Slice(ids, func(index, other int) bool { return ids[index] < ids[other] })
	for _, id := range ids {
		if err := repository.LockCustomer(ctx, projectID, id); err != nil {
			return err
		}
	}
	return nil
}

// lockExternalCustomerID serializes customer creation and alias creation for one external id.
func (repository *transaction) lockExternalCustomerID(
	ctx context.Context,
	projectID core.ProjectID,
	externalID string,
) error {
	_, err := repository.tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(
			hashtext('iapstack.customer_external'),
			hashtext($1 || E'\x1f' || $2)
		)
	`, string(projectID), externalID)
	return classifyError("lock external customer identity", err)
}

// closureContains reports whether a customer id belongs to a closure.
func closureContains(members []string, customerID string) bool {
	for _, member := range members {
		if member == customerID {
			return true
		}
	}
	return false
}

// newCustomerMergeID returns one identifier-safe merge audit id.
func newCustomerMergeID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate customer merge id: %w", err)
	}
	return "merge-" + hex.EncodeToString(buf[:]), nil
}
