package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
)

// Credential returns one protected credential in its project and application scope.
func (repository *transaction) Credential(
	ctx context.Context,
	key persistence.CredentialKey,
) (persistence.CredentialRecord, error) {
	if err := key.Validate(); err != nil {
		return persistence.CredentialRecord{}, err
	}
	return repository.loadCredential(ctx, key)
}

// PutCredential creates or rotates one credential with optimistic revision control.
func (repository *transaction) PutCredential(
	ctx context.Context,
	write persistence.CredentialWrite,
) (persistence.CredentialRecord, error) {
	if err := write.Validate(); err != nil {
		return persistence.CredentialRecord{}, err
	}

	var record persistence.CredentialRecord
	var err error
	if write.ExpectedRevision == 0 {
		record, err = repository.insertCredential(ctx, write)
	} else {
		record, err = repository.updateCredential(ctx, write)
	}
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return persistence.CredentialRecord{}, classifyError("put application credential", err)
	}
	return repository.resolveCredentialWriteMiss(ctx, write)
}

// loadCredential queries and validates one stored protected credential record.
func (repository *transaction) loadCredential(
	ctx context.Context,
	key persistence.CredentialKey,
) (persistence.CredentialRecord, error) {
	row := repository.tx.QueryRow(ctx, `
		SELECT
			project_id,
			application_id,
			kind,
			content_type,
			schema_version,
			payload_ciphertext,
			payload_fingerprint,
			encryption_key_id,
			revision,
			created_at,
			updated_at
		FROM application_credentials
		WHERE project_id = $1 AND application_id = $2 AND kind = $3
	`, key.ProjectID, key.ApplicationID, key.Kind)
	record, err := scanCredential(row)
	if err != nil {
		return persistence.CredentialRecord{}, classifyError("load application credential", err)
	}
	return record, nil
}

// insertCredential creates the first revision and returns no rows on an existing identity.
func (repository *transaction) insertCredential(
	ctx context.Context,
	write persistence.CredentialWrite,
) (persistence.CredentialRecord, error) {
	row := repository.tx.QueryRow(ctx, `
		INSERT INTO application_credentials (
			project_id,
			application_id,
			kind,
			content_type,
			schema_version,
			payload_ciphertext,
			payload_fingerprint,
			encryption_key_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (application_id, kind) DO NOTHING
		RETURNING
			project_id,
			application_id,
			kind,
			content_type,
			schema_version,
			payload_ciphertext,
			payload_fingerprint,
			encryption_key_id,
			revision,
			created_at,
			updated_at
	`,
		write.ProjectID,
		write.ApplicationID,
		write.Kind,
		write.ContentType,
		write.SchemaVersion,
		write.Payload.Ciphertext,
		write.Payload.Fingerprint[:],
		write.Payload.KeyID,
	)
	return scanCredential(row)
}

// updateCredential rotates a matching revision and increments its durable version.
func (repository *transaction) updateCredential(
	ctx context.Context,
	write persistence.CredentialWrite,
) (persistence.CredentialRecord, error) {
	row := repository.tx.QueryRow(ctx, `
		UPDATE application_credentials
		SET
			content_type = $4,
			schema_version = $5,
			payload_ciphertext = $6,
			payload_fingerprint = $7,
			encryption_key_id = $8,
			revision = revision + 1,
			updated_at = now()
		WHERE project_id = $1
			AND application_id = $2
			AND kind = $3
			AND revision = $9
		RETURNING
			project_id,
			application_id,
			kind,
			content_type,
			schema_version,
			payload_ciphertext,
			payload_fingerprint,
			encryption_key_id,
			revision,
			created_at,
			updated_at
	`,
		write.ProjectID,
		write.ApplicationID,
		write.Kind,
		write.ContentType,
		write.SchemaVersion,
		write.Payload.Ciphertext,
		write.Payload.Fingerprint[:],
		write.Payload.KeyID,
		write.ExpectedRevision,
	)
	return scanCredential(row)
}

// resolveCredentialWriteMiss distinguishes an idempotent retry, missing record, and conflict.
func (repository *transaction) resolveCredentialWriteMiss(
	ctx context.Context,
	write persistence.CredentialWrite,
) (persistence.CredentialRecord, error) {
	record, err := repository.loadCredential(ctx, write.CredentialKey)
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) && write.ExpectedRevision == 0 {
			return persistence.CredentialRecord{}, fmt.Errorf("put application credential: %w", persistence.ErrConflict)
		}
		return persistence.CredentialRecord{}, err
	}
	if credentialWriteMatches(record, write) {
		return record, nil
	}
	return persistence.CredentialRecord{}, fmt.Errorf("put application credential: %w", persistence.ErrConflict)
}

// scanCredential converts one PostgreSQL row into validated protected credential metadata.
func scanCredential(row pgx.Row) (persistence.CredentialRecord, error) {
	var record persistence.CredentialRecord
	var ciphertext []byte
	var fingerprint []byte
	err := row.Scan(
		&record.ProjectID,
		&record.ApplicationID,
		&record.Kind,
		&record.ContentType,
		&record.SchemaVersion,
		&ciphertext,
		&fingerprint,
		&record.Payload.KeyID,
		&record.Revision,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return persistence.CredentialRecord{}, err
	}
	if len(fingerprint) != len(record.Payload.Fingerprint) {
		return persistence.CredentialRecord{}, errors.New("stored credential fingerprint has invalid length")
	}
	record.Payload.Ciphertext = append([]byte(nil), ciphertext...)
	copy(record.Payload.Fingerprint[:], fingerprint)
	if err := record.Validate(); err != nil {
		return persistence.CredentialRecord{}, fmt.Errorf("validate stored application credential: %w", err)
	}
	return record, nil
}

// credentialWriteMatches reports whether a stored record represents the same logical write.
func credentialWriteMatches(record persistence.CredentialRecord, write persistence.CredentialWrite) bool {
	return record.CredentialKey == write.CredentialKey &&
		record.ContentType == write.ContentType &&
		record.SchemaVersion == write.SchemaVersion &&
		protectedFingerprintMatches(record.Payload.Fingerprint[:], write.Payload.Fingerprint)
}
