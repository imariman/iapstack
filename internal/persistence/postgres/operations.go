package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

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
