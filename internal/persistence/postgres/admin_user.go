package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// adminUserListLimit bounds the administrator accounts returned to one request.
	adminUserListLimit = 200
	// adminUserFailedAttemptsLimit keeps the stored failure count far below integer overflow.
	adminUserFailedAttemptsLimit = 1_000_000
	// adminUserColumns lists the account columns in the order scanAdminUserRecord reads them.
	adminUserColumns = `id, username, created_at, password_changed_at, session_generation,
		password_salt, password_hash, failed_attempts, locked_until`
)

// adminUserScanner reads one administrator account row.
type adminUserScanner interface {
	// Scan copies one row into the supplied account destinations.
	Scan(...any) error
}

// AdminUser returns one administrator account and its password verifier by public identity.
func (repository *transaction) AdminUser(ctx context.Context, id string) (persistence.AdminUserRecord, error) {
	// The id comes from a request path, so a malformed one is the caller's error, not a server failure.
	if err := validateText("administrator account ID", id); err != nil {
		return persistence.AdminUserRecord{}, validation.Wrap(err)
	}
	record, err := scanAdminUserRecord(repository.tx.QueryRow(ctx, `
		SELECT `+adminUserColumns+` FROM admin_users WHERE id = $1
	`, id))
	if err != nil {
		return persistence.AdminUserRecord{}, classifyError("load administrator account", err)
	}
	return record, nil
}

// AdminUserByUsername returns one administrator account and its password verifier by normalized username.
func (repository *transaction) AdminUserByUsername(
	ctx context.Context,
	username string,
) (persistence.AdminUserRecord, error) {
	if err := persistence.ValidateAdminUsername(username); err != nil {
		return persistence.AdminUserRecord{}, validation.Wrap(err)
	}
	record, err := scanAdminUserRecord(repository.tx.QueryRow(ctx, `
		SELECT `+adminUserColumns+` FROM admin_users WHERE username = $1
	`, username))
	if err != nil {
		return persistence.AdminUserRecord{}, classifyError("load administrator account by username", err)
	}
	return record, nil
}

// AdminUsers returns up to 200 administrator accounts, oldest first, without password verifiers.
func (repository *transaction) AdminUsers(ctx context.Context) ([]persistence.AdminUserSummary, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, username, created_at, password_changed_at, session_generation
		FROM admin_users
		ORDER BY created_at, id
		LIMIT $1
	`, adminUserListLimit)
	if err != nil {
		return nil, classifyError("list administrator accounts", err)
	}
	defer rows.Close()

	summaries := make([]persistence.AdminUserSummary, 0)
	for rows.Next() {
		summary, scanErr := scanAdminUserSummary(rows)
		if scanErr != nil {
			return nil, classifyError("scan administrator account", scanErr)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate administrator accounts", err)
	}
	return summaries, nil
}

// HasAdminUser reports whether at least one administrator account exists.
func (repository *transaction) HasAdminUser(ctx context.Context) (bool, error) {
	var exists bool
	if err := repository.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_users)`).Scan(&exists); err != nil {
		return false, classifyError("check administrator accounts", err)
	}
	return exists, nil
}

// PutAdminUser creates one administrator account and rejects a duplicate identity or username.
func (repository *transaction) PutAdminUser(ctx context.Context, record persistence.AdminUserRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	_, err := repository.tx.Exec(ctx, `
		INSERT INTO admin_users (
			id, username, password_salt, password_hash, password_changed_at, created_at, session_generation
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, record.ID, record.Username, record.PasswordSalt[:], record.PasswordHash[:],
		record.PasswordChangedAt.UTC(), record.CreatedAt.UTC(), record.SessionGeneration)
	return classifyError("put administrator account", err)
}

// SetAdminUserPassword replaces one account's password verifier, advances its session generation,
// and clears its failed sign-in state.
func (repository *transaction) SetAdminUserPassword(
	ctx context.Context,
	write persistence.AdminUserPasswordWrite,
) (persistence.AdminUserSummary, error) {
	if err := write.Validate(); err != nil {
		return persistence.AdminUserSummary{}, err
	}
	summary, err := scanAdminUserSummary(repository.tx.QueryRow(ctx, `
		UPDATE admin_users
		SET password_salt = $2, password_hash = $3, password_changed_at = $4,
			session_generation = session_generation + 1,
			failed_attempts = 0, locked_until = NULL
		WHERE id = $1
		RETURNING id, username, created_at, password_changed_at, session_generation
	`, write.ID, write.PasswordSalt[:], write.PasswordHash[:], write.ChangedAt.UTC()))
	if err != nil {
		return persistence.AdminUserSummary{}, classifyError("set administrator password", err)
	}
	return summary, nil
}

// RecordAdminUserSignInFailure counts one failed sign-in and locks the account once the count reaches
// the threshold. It returns the lock expiry, or nil while the account stays unlocked.
func (repository *transaction) RecordAdminUserSignInFailure(
	ctx context.Context,
	failure persistence.AdminUserSignInFailure,
) (*time.Time, error) {
	if err := failure.Validate(); err != nil {
		return nil, err
	}
	// RETURNING reads the updated row, so failed_attempts there already includes this failure.
	var lockedUntil *time.Time
	err := repository.tx.QueryRow(ctx, `
		UPDATE admin_users
		SET failed_attempts = LEAST(failed_attempts + 1, $4),
			locked_until = CASE WHEN failed_attempts + 1 >= $2 THEN $3 ELSE locked_until END
		WHERE id = $1
		RETURNING CASE WHEN failed_attempts >= $2 THEN locked_until END
	`, failure.ID, failure.Threshold, failure.LockedUntil.UTC(), adminUserFailedAttemptsLimit).Scan(&lockedUntil)
	if err != nil {
		return nil, classifyError("record administrator sign-in failure", err)
	}
	if lockedUntil != nil {
		expiry := lockedUntil.UTC()
		lockedUntil = &expiry
	}
	return lockedUntil, nil
}

// ClearAdminUserSignInFailures resets the failed sign-in count and lock of one account.
func (repository *transaction) ClearAdminUserSignInFailures(ctx context.Context, id string) error {
	if err := validateText("administrator account ID", id); err != nil {
		return err
	}
	_, err := repository.tx.Exec(ctx, `
		UPDATE admin_users
		SET failed_attempts = 0, locked_until = NULL
		WHERE id = $1 AND (failed_attempts <> 0 OR locked_until IS NOT NULL)
	`, id)
	return classifyError("clear administrator sign-in failures", err)
}

// DeleteAdminUser removes one administrator account. It refuses to remove the final account
// while no active administrator key exists.
func (repository *transaction) DeleteAdminUser(ctx context.Context, id string) error {
	if err := validateText("administrator account ID", id); err != nil {
		return validation.Wrap(err)
	}
	// Key revocation takes the same lock, so the two cannot remove the last administrator between them.
	if _, err := repository.tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, apiKeyLifecycleLockID); err != nil {
		return classifyError("lock administrator lifecycle", err)
	}
	command, err := repository.tx.Exec(ctx, `DELETE FROM admin_users WHERE id = $1`, id)
	if err != nil {
		return classifyError("delete administrator account", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("delete administrator account: %w", persistence.ErrNotFound)
	}
	var administratorRemains bool
	if err := repository.tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM admin_users)
			OR EXISTS(SELECT 1 FROM api_keys WHERE role = 'admin' AND revoked_at IS NULL)
	`).Scan(&administratorRemains); err != nil {
		return classifyError("check remaining administrators", err)
	}
	if !administratorRemains {
		return fmt.Errorf("delete final administrator account: %w", persistence.ErrConflict)
	}
	return nil
}

// scanAdminUserSummary reads and validates one account row without its password verifier.
func scanAdminUserSummary(scanner adminUserScanner) (persistence.AdminUserSummary, error) {
	var summary persistence.AdminUserSummary
	if err := scanner.Scan(
		&summary.ID, &summary.Username, &summary.CreatedAt, &summary.PasswordChangedAt,
		&summary.SessionGeneration,
	); err != nil {
		return persistence.AdminUserSummary{}, err
	}
	summary.CreatedAt = summary.CreatedAt.UTC()
	summary.PasswordChangedAt = summary.PasswordChangedAt.UTC()
	if err := summary.Validate(); err != nil {
		return persistence.AdminUserSummary{}, fmt.Errorf("validate stored administrator account: %w", err)
	}
	return summary, nil
}

// scanAdminUserRecord reads and validates one account row with its password verifier.
func scanAdminUserRecord(scanner adminUserScanner) (persistence.AdminUserRecord, error) {
	var record persistence.AdminUserRecord
	var salt, hash []byte
	if err := scanner.Scan(
		&record.ID, &record.Username, &record.CreatedAt, &record.PasswordChangedAt,
		&record.SessionGeneration, &salt, &hash, &record.FailedAttempts, &record.LockedUntil,
	); err != nil {
		return persistence.AdminUserRecord{}, err
	}
	if len(salt) != len(record.PasswordSalt) || len(hash) != len(record.PasswordHash) {
		return persistence.AdminUserRecord{}, errors.New("stored administrator password verifier has invalid length")
	}
	copy(record.PasswordSalt[:], salt)
	copy(record.PasswordHash[:], hash)
	record.CreatedAt = record.CreatedAt.UTC()
	record.PasswordChangedAt = record.PasswordChangedAt.UTC()
	if record.LockedUntil != nil {
		expiry := record.LockedUntil.UTC()
		record.LockedUntil = &expiry
	}
	if err := record.Validate(); err != nil {
		return persistence.AdminUserRecord{}, fmt.Errorf("validate stored administrator account: %w", err)
	}
	return record, nil
}
