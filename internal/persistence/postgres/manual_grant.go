package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// manualGrantSelectSQL reads one stored grant joined to its public entitlement key.
	manualGrantSelectSQL = `
		SELECT mg.id, mg.project_id, mg.customer_id, mg.application_id, mg.product_id,
			mg.entitlement_id, e.key, mg.reason, mg.actor_key_id, mg.actor_channel, mg.idempotency_key,
			mg.effective_starts_at, mg.effective_ends_at, mg.revoked_at,
			mg.revoked_by_key_id, mg.revoked_by_channel, mg.revocation_reason, mg.created_at
		FROM manual_grants AS mg
		JOIN entitlements AS e ON e.id = mg.entitlement_id AND e.project_id = mg.project_id
	`
)

var (
	// manualGrantRepositoryContract verifies that transaction stores manual grants.
	_ persistence.ManualGrantRepository = (*transaction)(nil)
)

// CreateManualGrant stores one audited operator grant for the canonical customer or returns its replay.
// The grant is never written as a purchase observation, evidence row, or provider reference.
func (repository *transaction) CreateManualGrant(
	ctx context.Context,
	write persistence.ManualGrantWrite,
) (persistence.ManualGrantResult, error) {
	if err := write.Validate(); err != nil {
		return persistence.ManualGrantResult{}, err
	}
	canonicalID, members, err := repository.lockCanonicalClosure(ctx, write.ProjectID, write.CustomerID)
	if err != nil {
		return persistence.ManualGrantResult{}, err
	}

	existing, err := scanManualGrantRow(repository.tx.QueryRow(ctx, manualGrantSelectSQL+`
		WHERE mg.project_id = $1 AND mg.idempotency_key = $2
	`, write.ProjectID, write.IdempotencyKey))
	if err != nil && !errors.Is(err, persistence.ErrNotFound) {
		return persistence.ManualGrantResult{}, err
	}
	if err == nil {
		if !manualGrantReplays(existing, write, members) {
			return persistence.ManualGrantResult{}, fmt.Errorf("create manual grant: %w", persistence.ErrConflict)
		}
		return persistence.ManualGrantResult{Grant: existing, CanonicalCustomerID: canonicalID, Replay: true}, nil
	}

	if err := repository.requireManualGrantCatalog(ctx, write); err != nil {
		return persistence.ManualGrantResult{}, err
	}
	grantID, err := newManualGrantID()
	if err != nil {
		return persistence.ManualGrantResult{}, err
	}
	_, err = repository.tx.Exec(ctx, `
		INSERT INTO manual_grants (
			id, project_id, customer_id, application_id, product_id, entitlement_id,
			reason, actor_key_id, actor_channel, idempotency_key, effective_starts_at, effective_ends_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`,
		grantID,
		write.ProjectID,
		canonicalID,
		write.ApplicationID,
		write.ProductID,
		write.EntitlementID,
		write.Reason,
		write.ActorKeyID,
		write.ActorChannel,
		write.IdempotencyKey,
		normalizeTime(write.StartsAt),
		normalizeTime(write.EndsAt),
	)
	if err != nil {
		return persistence.ManualGrantResult{}, classifyError("insert manual grant", err)
	}
	grant, err := repository.manualGrantByID(ctx, write.ProjectID, grantID)
	if err != nil {
		return persistence.ManualGrantResult{}, err
	}
	return persistence.ManualGrantResult{Grant: grant, CanonicalCustomerID: canonicalID}, nil
}

// RevokeManualGrant ends one grant of the customer closure early, or returns the stored revocation.
func (repository *transaction) RevokeManualGrant(
	ctx context.Context,
	revocation persistence.ManualGrantRevocation,
) (persistence.ManualGrantResult, error) {
	if err := revocation.Validate(); err != nil {
		return persistence.ManualGrantResult{}, err
	}
	canonicalID, members, err := repository.lockCanonicalClosure(ctx, revocation.ProjectID, revocation.CustomerID)
	if err != nil {
		return persistence.ManualGrantResult{}, err
	}
	grant, err := repository.manualGrantByID(ctx, revocation.ProjectID, revocation.GrantID)
	if err != nil {
		return persistence.ManualGrantResult{}, err
	}
	if !closureContains(members, string(grant.CustomerID)) {
		return persistence.ManualGrantResult{}, fmt.Errorf("revoke manual grant: %w", persistence.ErrNotFound)
	}
	if grant.RevokedAt != nil {
		return persistence.ManualGrantResult{Grant: grant, CanonicalCustomerID: canonicalID, Replay: true}, nil
	}
	if !revocation.RevokedAt.Before(grant.EndsAt) {
		// An ended grant no longer gives access; recording an early revocation would rewrite history.
		return persistence.ManualGrantResult{}, fmt.Errorf("revoke ended manual grant: %w", persistence.ErrConflict)
	}
	command, err := repository.tx.Exec(ctx, `
		UPDATE manual_grants
		SET revoked_at = GREATEST($1::timestamptz, effective_starts_at),
			revoked_by_key_id = $2,
			revoked_by_channel = $3,
			revocation_reason = $4
		WHERE project_id = $5 AND id = $6 AND revoked_at IS NULL
	`,
		normalizeTime(revocation.RevokedAt),
		revocation.ActorKeyID,
		revocation.ActorChannel,
		revocation.Reason,
		revocation.ProjectID,
		revocation.GrantID,
	)
	if err != nil {
		return persistence.ManualGrantResult{}, classifyError("revoke manual grant", err)
	}
	if command.RowsAffected() != 1 {
		return persistence.ManualGrantResult{}, fmt.Errorf("revoke manual grant: %w", errRetryableTransaction)
	}
	grant, err = repository.manualGrantByID(ctx, revocation.ProjectID, revocation.GrantID)
	if err != nil {
		return persistence.ManualGrantResult{}, err
	}
	return persistence.ManualGrantResult{Grant: grant, CanonicalCustomerID: canonicalID}, nil
}

// lockCanonicalClosure resolves one customer to its canonical owner, locks that closure, and returns its members.
// It reuses customerClosure, so the one-hop merge rule lives in one place. An unknown customer is not found.
func (repository *transaction) lockCanonicalClosure(
	ctx context.Context,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (core.CustomerID, []string, error) {
	canonicalID, members, err := repository.customerClosure(ctx, projectID, customerID)
	if err != nil {
		return "", nil, err
	}
	if err := repository.LockCustomerClosure(ctx, projectID, canonicalID); err != nil {
		return "", nil, err
	}
	return canonicalID, members, nil
}

// manualGrantReplays reports whether a stored grant answers a repeated request with the same key.
// The customer may have been merged since, so any closure member matches. The period matches when the
// requested duration or the requested absolute end equals the stored one.
func manualGrantReplays(existing persistence.ManualGrant, write persistence.ManualGrantWrite, members []string) bool {
	if !closureContains(members, string(existing.CustomerID)) || existing.ApplicationID != write.ApplicationID ||
		existing.ProductID != write.ProductID || existing.EntitlementID != write.EntitlementID ||
		existing.Reason != write.Reason {
		return false
	}
	storedDuration := normalizeTime(existing.EndsAt).Sub(normalizeTime(existing.StartsAt))
	requestedDuration := normalizeTime(write.EndsAt).Sub(normalizeTime(write.StartsAt))
	return storedDuration == requestedDuration || normalizeTime(existing.EndsAt).Equal(normalizeTime(write.EndsAt))
}

// requireManualGrantCatalog confirms the application and the product's entitlement grant exist in the project.
func (repository *transaction) requireManualGrantCatalog(ctx context.Context, write persistence.ManualGrantWrite) error {
	var applicationFound, entitlementFound, mappingFound, storeMappingFound bool
	var productKind pgtype.Text
	err := repository.tx.QueryRow(ctx, `
		SELECT
			EXISTS (SELECT 1 FROM applications WHERE project_id = $1 AND id = $2),
			(SELECT kind FROM products WHERE project_id = $1 AND id = $3),
			EXISTS (SELECT 1 FROM entitlements WHERE project_id = $1 AND id = $4),
			EXISTS (
				SELECT 1 FROM product_entitlements
				WHERE project_id = $1 AND product_id = $3 AND entitlement_id = $4
			),
			EXISTS (
				SELECT 1 FROM store_products
				WHERE project_id = $1 AND application_id = $2 AND product_id = $3
			)
	`, write.ProjectID, write.ApplicationID, write.ProductID, write.EntitlementID).Scan(
		&applicationFound, &productKind, &entitlementFound, &mappingFound, &storeMappingFound,
	)
	if err != nil {
		return classifyError("check manual grant catalog", err)
	}
	if !applicationFound || !productKind.Valid || !entitlementFound {
		return fmt.Errorf("manual grant catalog: %w", persistence.ErrNotFound)
	}
	if !mappingFound {
		return validation.Wrap(errors.New("the product does not grant the entitlement"))
	}
	if core.ProductKind(productKind.String) == core.ProductKindConsumable {
		return validation.Wrap(errors.New("a consumable product cannot back a manual grant"))
	}
	if !storeMappingFound {
		return fmt.Errorf("manual grant catalog: %w", persistence.ErrManualGrantProductNotMapped)
	}
	return nil
}

// manualGrantByID loads one stored grant inside its project.
func (repository *transaction) manualGrantByID(
	ctx context.Context,
	projectID core.ProjectID,
	grantID string,
) (persistence.ManualGrant, error) {
	return scanManualGrantRow(repository.tx.QueryRow(ctx, manualGrantSelectSQL+`
		WHERE mg.project_id = $1 AND mg.id = $2
	`, projectID, grantID))
}

// scanManualGrantRow converts one manualGrantSelectSQL row into a validated grant.
func scanManualGrantRow(row pgx.Row) (persistence.ManualGrant, error) {
	var grant persistence.ManualGrant
	var revokedAt pgtype.Timestamptz
	var revokedBy pgtype.Text
	var revokedByChannel pgtype.Text
	var revocationReason pgtype.Text
	if err := row.Scan(
		&grant.ID,
		&grant.ProjectID,
		&grant.CustomerID,
		&grant.ApplicationID,
		&grant.ProductID,
		&grant.EntitlementID,
		&grant.EntitlementKey,
		&grant.Reason,
		&grant.ActorKeyID,
		&grant.ActorChannel,
		&grant.IdempotencyKey,
		&grant.StartsAt,
		&grant.EndsAt,
		&revokedAt,
		&revokedBy,
		&revokedByChannel,
		&revocationReason,
		&grant.CreatedAt,
	); err != nil {
		return persistence.ManualGrant{}, classifyError("load manual grant", err)
	}
	grant.RevokedAt = timestampPointer(revokedAt)
	grant.RevokedByKeyID = textValue(revokedBy)
	grant.RevokedByChannel = persistence.ActorChannel(textValue(revokedByChannel))
	grant.RevocationReason = textValue(revocationReason)
	if err := grant.Validate(); err != nil {
		return persistence.ManualGrant{}, fmt.Errorf("validate stored manual grant: %w", err)
	}
	return grant, nil
}

// newManualGrantID returns one random operator grant id that cannot collide with observation ids.
func newManualGrantID() (string, error) {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", fmt.Errorf("generate manual grant id: %w", err)
	}
	return "mgr-" + hex.EncodeToString(buffer[:]), nil
}
