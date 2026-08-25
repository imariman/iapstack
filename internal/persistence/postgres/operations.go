package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

const (
	// apiKeyListLimit bounds lifecycle metadata returned to one administrator request.
	apiKeyListLimit = 200
	// apiKeyLifecycleLockID serializes revocations that could remove the final active administrator.
	apiKeyLifecycleLockID int64 = 0x4941504b45594144
)

// apiKeySummaryScanner reads one secret-free API key lifecycle row.
type apiKeySummaryScanner interface {
	// Scan copies one row into the supplied lifecycle destinations.
	Scan(...any) error
}

// APIKey returns one non-revoked API key verifier by public identity.
func (repository *transaction) APIKey(ctx context.Context, id string) (persistence.APIKeyRecord, error) {
	if err := validateText("API key ID", id); err != nil {
		return persistence.APIKeyRecord{}, err
	}
	var record persistence.APIKeyRecord
	var projectID, applicationID *string
	var salt, hash []byte
	err := repository.tx.QueryRow(ctx, `
		SELECT id, role, project_id, application_id, secret_salt, secret_hash, created_at
		FROM api_keys WHERE id = $1 AND revoked_at IS NULL
	`, id).Scan(&record.ID, &record.Role, &projectID, &applicationID, &salt, &hash, &record.CreatedAt)
	if err != nil {
		return persistence.APIKeyRecord{}, classifyError("load API key", err)
	}
	if projectID != nil {
		record.ProjectID = core.ProjectID(*projectID)
	}
	if applicationID != nil {
		record.ApplicationID = core.ApplicationID(*applicationID)
	}
	if len(salt) != len(record.SecretSalt) || len(hash) != len(record.SecretHash) {
		return persistence.APIKeyRecord{}, errors.New("stored API key verifier has invalid length")
	}
	copy(record.SecretSalt[:], salt)
	copy(record.SecretHash[:], hash)
	if err := record.Validate(); err != nil {
		return persistence.APIKeyRecord{}, fmt.Errorf("validate stored API key: %w", err)
	}
	return record, nil
}

// APIKeys returns bounded lifecycle metadata without bearer secrets or verifiers.
func (repository *transaction) APIKeys(ctx context.Context) ([]persistence.APIKeySummary, error) {
	rows, err := repository.tx.Query(ctx, `
		SELECT id, role, project_id, application_id, created_at, revoked_at
		FROM api_keys
		ORDER BY created_at DESC, id
		LIMIT $1
	`, apiKeyListLimit)
	if err != nil {
		return nil, classifyError("list API keys", err)
	}
	defer rows.Close()

	summaries := make([]persistence.APIKeySummary, 0)
	for rows.Next() {
		summary, scanErr := scanAPIKeySummary(rows)
		if scanErr != nil {
			return nil, classifyError("scan API key", scanErr)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate API keys", err)
	}
	return summaries, nil
}

// PutAPIKey creates one API key verifier without storing its bearer secret.
func (repository *transaction) PutAPIKey(ctx context.Context, record persistence.APIKeyRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	var projectID any
	var applicationID any
	if record.Role == persistence.APIKeyRoleApplication {
		projectID = record.ProjectID
		applicationID = record.ApplicationID
	}
	_, err := repository.tx.Exec(ctx, `
		INSERT INTO api_keys (id, role, project_id, application_id, secret_salt, secret_hash, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, record.ID, record.Role, projectID, applicationID, record.SecretSalt[:], record.SecretHash[:], record.CreatedAt)
	return classifyError("put API key", err)
}

// RevokeAPIKey idempotently revokes one key and atomically preserves an active administrator.
func (repository *transaction) RevokeAPIKey(
	ctx context.Context,
	id string,
	revokedAt time.Time,
) (persistence.APIKeySummary, error) {
	if err := validateText("API key ID", id); err != nil {
		return persistence.APIKeySummary{}, err
	}
	if revokedAt.IsZero() {
		return persistence.APIKeySummary{}, errors.New("API key revocation time is required")
	}
	if _, err := repository.tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, apiKeyLifecycleLockID); err != nil {
		return persistence.APIKeySummary{}, classifyError("lock API key lifecycle", err)
	}
	summary, err := scanAPIKeySummary(repository.tx.QueryRow(ctx, `
		SELECT id, role, project_id, application_id, created_at, revoked_at
		FROM api_keys
		WHERE id = $1
		FOR UPDATE
	`, id))
	if err != nil {
		return persistence.APIKeySummary{}, classifyError("load API key lifecycle", err)
	}
	if summary.RevokedAt != nil {
		return summary, nil
	}
	if summary.Role == persistence.APIKeyRoleAdmin {
		var activeAdministrators int64
		if err := repository.tx.QueryRow(ctx, `
			SELECT count(*) FROM api_keys WHERE role = 'admin' AND revoked_at IS NULL
		`).Scan(&activeAdministrators); err != nil {
			return persistence.APIKeySummary{}, classifyError("count active administrator API keys", err)
		}
		if activeAdministrators <= 1 {
			return persistence.APIKeySummary{}, fmt.Errorf("revoke final administrator API key: %w", persistence.ErrConflict)
		}
	}
	revokedAt = revokedAt.UTC()
	command, err := repository.tx.Exec(ctx, `
		UPDATE api_keys SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL
	`, id, revokedAt)
	if err != nil {
		return persistence.APIKeySummary{}, classifyError("revoke API key", err)
	}
	if command.RowsAffected() != 1 {
		return persistence.APIKeySummary{}, fmt.Errorf("revoke API key: %w", persistence.ErrConflict)
	}
	summary.RevokedAt = &revokedAt
	if err := summary.Validate(); err != nil {
		return persistence.APIKeySummary{}, fmt.Errorf("validate revoked API key: %w", err)
	}
	return summary, nil
}

// PutWebhookEndpoint creates or rotates one application webhook with optimistic revision control.
func (repository *transaction) PutWebhookEndpoint(
	ctx context.Context,
	write persistence.WebhookEndpointWrite,
) (persistence.WebhookEndpointRecord, error) {
	if err := write.Validate(); err != nil {
		return persistence.WebhookEndpointRecord{}, err
	}
	if write.ExpectedRevision == 0 {
		_, err := repository.tx.Exec(ctx, `
			INSERT INTO webhook_endpoints (
				project_id, application_id, url, secret_ciphertext, secret_fingerprint, encryption_key_id
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (application_id) DO NOTHING
		`, write.ProjectID, write.ApplicationID, write.URL, write.Secret.Ciphertext, write.Secret.Fingerprint[:], write.Secret.KeyID)
		if err != nil {
			return persistence.WebhookEndpointRecord{}, classifyError("create webhook endpoint", err)
		}
	} else {
		command, err := repository.tx.Exec(ctx, `
			UPDATE webhook_endpoints
			SET url = $1, secret_ciphertext = $2, secret_fingerprint = $3,
				encryption_key_id = $4, revision = revision + 1, updated_at = now()
			WHERE project_id = $5 AND application_id = $6 AND revision = $7
		`, write.URL, write.Secret.Ciphertext, write.Secret.Fingerprint[:], write.Secret.KeyID,
			write.ProjectID, write.ApplicationID, write.ExpectedRevision)
		if err != nil {
			return persistence.WebhookEndpointRecord{}, classifyError("rotate webhook endpoint", err)
		}
		if command.RowsAffected() != 1 {
			return persistence.WebhookEndpointRecord{}, fmt.Errorf("rotate webhook endpoint: %w", persistence.ErrConflict)
		}
	}
	record, err := repository.WebhookEndpoint(ctx, write.ProjectID, write.ApplicationID)
	if err != nil {
		return persistence.WebhookEndpointRecord{}, err
	}
	if write.ExpectedRevision == 0 && (record.URL != write.URL || record.Secret.Fingerprint != write.Secret.Fingerprint) {
		return persistence.WebhookEndpointRecord{}, fmt.Errorf("create webhook endpoint: %w", persistence.ErrConflict)
	}
	return record, nil
}

// WebhookEndpoint returns protected delivery configuration for one application.
func (repository *transaction) WebhookEndpoint(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) (persistence.WebhookEndpointRecord, error) {
	if err := errors.Join(projectID.Validate(), applicationID.Validate()); err != nil {
		return persistence.WebhookEndpointRecord{}, err
	}
	var record persistence.WebhookEndpointRecord
	var ciphertext, fingerprint []byte
	err := repository.tx.QueryRow(ctx, `
		SELECT project_id, application_id, url, secret_ciphertext, secret_fingerprint,
			encryption_key_id, revision, created_at, updated_at
		FROM webhook_endpoints WHERE project_id = $1 AND application_id = $2
	`, projectID, applicationID).Scan(
		&record.ProjectID, &record.ApplicationID, &record.URL, &ciphertext, &fingerprint,
		&record.Secret.KeyID, &record.Revision, &record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		return persistence.WebhookEndpointRecord{}, classifyError("load webhook endpoint", err)
	}
	if len(fingerprint) != len(record.Secret.Fingerprint) {
		return persistence.WebhookEndpointRecord{}, errors.New("stored webhook fingerprint has invalid length")
	}
	record.Secret.Ciphertext = append([]byte(nil), ciphertext...)
	copy(record.Secret.Fingerprint[:], fingerprint)
	if err := record.Validate(); err != nil {
		return persistence.WebhookEndpointRecord{}, fmt.Errorf("validate stored webhook endpoint: %w", err)
	}
	return record, nil
}

// scanAPIKeySummary converts nullable scope and lifecycle columns into a validated public summary.
func scanAPIKeySummary(scanner apiKeySummaryScanner) (persistence.APIKeySummary, error) {
	var summary persistence.APIKeySummary
	var projectID, applicationID *string
	if err := scanner.Scan(
		&summary.ID,
		&summary.Role,
		&projectID,
		&applicationID,
		&summary.CreatedAt,
		&summary.RevokedAt,
	); err != nil {
		return persistence.APIKeySummary{}, err
	}
	if projectID != nil {
		summary.ProjectID = core.ProjectID(*projectID)
	}
	if applicationID != nil {
		summary.ApplicationID = core.ApplicationID(*applicationID)
	}
	if err := summary.Validate(); err != nil {
		return persistence.APIKeySummary{}, err
	}
	return summary, nil
}
