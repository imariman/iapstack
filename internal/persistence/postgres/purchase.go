package postgres

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
)

// referenceIdentity is the non-secret idempotency identity of one protected reference.
type referenceIdentity struct {
	role        core.ReferenceRole
	kind        string
	fingerprint [32]byte
}

// PurchaseContextByReference returns the latest verified customer and product for one protected provider value.
func (repository *transaction) PurchaseContextByReference(
	ctx context.Context,
	lookup persistence.ProviderReferenceLookup,
) (persistence.PurchaseReferenceContext, error) {
	if err := lookup.Validate(); err != nil {
		return persistence.PurchaseReferenceContext{}, err
	}
	result := persistence.PurchaseReferenceContext{}
	err := repository.tx.QueryRow(ctx, `
		SELECT customer.id, customer.project_id, customer.external_id,
			observation.provider_product_id, observation.product_kind
		FROM provider_references AS reference
		JOIN observation_references AS observation_reference
			ON observation_reference.reference_id = reference.id
			AND observation_reference.application_id = reference.application_id
		JOIN purchase_observations AS observation
			ON observation.id = observation_reference.observation_id
			AND observation.application_id = observation_reference.application_id
		JOIN customers AS customer
			ON customer.id = observation.customer_id
			AND customer.project_id = observation.project_id
		WHERE observation.project_id = $1
			AND reference.application_id = $2
			AND reference.role = $3
			AND reference.kind = $4
			AND reference.value_fingerprint = $5
		ORDER BY observation.observed_at DESC, observation.created_at DESC, observation.id DESC
		LIMIT 1
	`, lookup.ProjectID, lookup.ApplicationID, lookup.Role, lookup.Kind, lookup.Fingerprint[:]).Scan(
		&result.Customer.ID,
		&result.Customer.ProjectID,
		&result.Customer.ExternalID,
		&result.ProviderProductID,
		&result.ProductKind,
	)
	if err != nil {
		return persistence.PurchaseReferenceContext{}, classifyError("resolve provider reference context", err)
	}
	if err := result.Validate(); err != nil {
		return persistence.PurchaseReferenceContext{}, fmt.Errorf("validate provider reference context: %w", err)
	}
	return result, nil
}

// SaveEvidence stores client evidence idempotently and returns its durable identity.
func (repository *transaction) SaveEvidence(ctx context.Context, write persistence.EvidenceWrite) (int64, error) {
	if err := write.Validate(); err != nil {
		return 0, err
	}
	receivedAt := normalizeTime(write.ReceivedAt)

	var evidenceID int64
	err := repository.tx.QueryRow(ctx, `
		INSERT INTO purchase_evidence (
			project_id,
			application_id,
			customer_id,
			kind,
			content_type,
			payload_ciphertext,
			payload_fingerprint,
			encryption_key_id,
			received_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (application_id, payload_fingerprint) DO NOTHING
		RETURNING id
	`,
		write.ProjectID,
		write.ApplicationID,
		write.CustomerID,
		write.Kind,
		write.ContentType,
		write.Payload.Ciphertext,
		write.Payload.Fingerprint[:],
		write.Payload.KeyID,
		receivedAt,
	).Scan(&evidenceID)
	if err == nil {
		return evidenceID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, classifyError("save purchase evidence", err)
	}

	var storedProjectID string
	var storedCustomerID string
	var storedKind string
	var storedContentType string
	err = repository.tx.QueryRow(ctx, `
		SELECT id, project_id, customer_id, kind, content_type
		FROM purchase_evidence
		WHERE application_id = $1 AND payload_fingerprint = $2
	`, write.ApplicationID, write.Payload.Fingerprint[:]).Scan(
		&evidenceID,
		&storedProjectID,
		&storedCustomerID,
		&storedKind,
		&storedContentType,
	)
	if err != nil {
		return 0, classifyError("load existing purchase evidence", err)
	}
	if storedProjectID != string(write.ProjectID) ||
		storedCustomerID != string(write.CustomerID) ||
		storedKind != write.Kind ||
		storedContentType != write.ContentType {
		return 0, fmt.Errorf("save purchase evidence: %w", persistence.ErrConflict)
	}
	return evidenceID, nil
}

// SaveArtifact stores one encrypted provider artifact idempotently.
func (repository *transaction) SaveArtifact(ctx context.Context, write persistence.ArtifactWrite) error {
	if err := write.Validate(); err != nil {
		return err
	}

	var artifactID int64
	err := repository.tx.QueryRow(ctx, `
		INSERT INTO verified_artifacts (
			evidence_id,
			kind,
			content_type,
			payload_ciphertext,
			payload_fingerprint,
			encryption_key_id
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (evidence_id, kind, payload_fingerprint) DO NOTHING
		RETURNING id
	`,
		write.EvidenceID,
		write.Kind,
		write.ContentType,
		write.Payload.Ciphertext,
		write.Payload.Fingerprint[:],
		write.Payload.KeyID,
	).Scan(&artifactID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return classifyError("save verified artifact", err)
	}

	var storedContentType string
	err = repository.tx.QueryRow(ctx, `
		SELECT content_type
		FROM verified_artifacts
		WHERE evidence_id = $1 AND kind = $2 AND payload_fingerprint = $3
	`, write.EvidenceID, write.Kind, write.Payload.Fingerprint[:]).Scan(&storedContentType)
	if err != nil {
		return classifyError("load existing verified artifact", err)
	}
	if storedContentType != write.ContentType {
		return fmt.Errorf("save verified artifact: %w", persistence.ErrConflict)
	}
	return nil
}

// SaveObservation stores one immutable observation and its protected references idempotently.
func (repository *transaction) SaveObservation(
	ctx context.Context,
	write persistence.ObservationWrite,
) error {
	if err := write.Validate(); err != nil {
		return err
	}

	inserted, err := repository.insertObservation(ctx, write)
	if err != nil {
		return err
	}
	if !inserted {
		matches, err := repository.observationMatches(ctx, write)
		if err != nil {
			return err
		}
		if !matches {
			return fmt.Errorf("save purchase observation: %w", persistence.ErrConflict)
		}
		return repository.verifyObservationReferences(ctx, write)
	}

	for _, reference := range write.References {
		referenceID, err := repository.saveReference(ctx, write.Observation.ApplicationID, reference)
		if err != nil {
			return err
		}
		_, err = repository.tx.Exec(ctx, `
			INSERT INTO observation_references (observation_id, application_id, reference_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (observation_id, reference_id) DO NOTHING
		`, write.Observation.ID, write.Observation.ApplicationID, referenceID)
		if err != nil {
			return classifyError("link observation reference", err)
		}
	}
	return nil
}

// insertObservation inserts one normalized observation and reports whether it was new.
func (repository *transaction) insertObservation(
	ctx context.Context,
	write persistence.ObservationWrite,
) (bool, error) {
	observation := write.Observation
	var insertedID string
	err := repository.tx.QueryRow(ctx, `
		INSERT INTO purchase_observations (
			id,
			evidence_id,
			project_id,
			application_id,
			customer_id,
			product_id,
			provider_product_id,
			product_kind,
			lifecycle_state,
			provider_state,
			access_status,
			access_reason,
			ownership,
			quantity,
			occurred_at,
			observed_at,
			effective_starts_at,
			effective_ends_at,
			renewal_mode,
			renewal_status,
			next_provider_product_id,
			next_renewal_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
		)
		ON CONFLICT (id) DO NOTHING
		RETURNING id
	`,
		observation.ID,
		write.EvidenceID,
		write.ProjectID,
		observation.ApplicationID,
		write.CustomerID,
		write.ProductID,
		observation.ProductID,
		observation.ProductKind,
		observation.State,
		observation.ProviderState,
		observation.Access,
		observation.AccessReason,
		observation.Ownership,
		observation.Quantity,
		nullableTime(observation.OccurredAt),
		normalizeTime(observation.ObservedAt),
		nullableTime(observation.EffectivePeriod.StartsAt),
		nullableTimePointer(observation.EffectivePeriod.EndsAt),
		observation.Renewal.Mode,
		observation.Renewal.Status,
		nullableString(string(observation.Renewal.NextProductID)),
		nullableTimePointer(observation.Renewal.NextRenewalAt),
	).Scan(&insertedID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, classifyError("save purchase observation", err)
}

// observationMatches checks the immutable logical snapshot independently of receipt evidence and observation time.
func (repository *transaction) observationMatches(
	ctx context.Context,
	write persistence.ObservationWrite,
) (bool, error) {
	observation := write.Observation
	var matches bool
	err := repository.tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM purchase_observations
			WHERE id = $1
				AND project_id = $2
				AND application_id = $3
				AND customer_id = $4
				AND product_id = $5
				AND provider_product_id = $6
				AND product_kind = $7
				AND lifecycle_state = $8
				AND provider_state = $9
				AND access_status = $10
				AND access_reason = $11
				AND ownership = $12
				AND quantity = $13
				AND occurred_at IS NOT DISTINCT FROM $14::timestamptz
				AND effective_starts_at IS NOT DISTINCT FROM $15::timestamptz
				AND effective_ends_at IS NOT DISTINCT FROM $16::timestamptz
				AND renewal_mode = $17
				AND renewal_status = $18
				AND next_provider_product_id IS NOT DISTINCT FROM $19::text
				AND next_renewal_at IS NOT DISTINCT FROM $20::timestamptz
		)
	`,
		observation.ID,
		write.ProjectID,
		observation.ApplicationID,
		write.CustomerID,
		write.ProductID,
		observation.ProductID,
		observation.ProductKind,
		observation.State,
		observation.ProviderState,
		observation.Access,
		observation.AccessReason,
		observation.Ownership,
		observation.Quantity,
		nullableTime(observation.OccurredAt),
		nullableTime(observation.EffectivePeriod.StartsAt),
		nullableTimePointer(observation.EffectivePeriod.EndsAt),
		observation.Renewal.Mode,
		observation.Renewal.Status,
		nullableString(string(observation.Renewal.NextProductID)),
		nullableTimePointer(observation.Renewal.NextRenewalAt),
	).Scan(&matches)
	if err != nil {
		return false, classifyError("compare purchase observation", err)
	}
	return matches, nil
}

// saveReference stores one protected provider reference and returns its durable identity.
func (repository *transaction) saveReference(
	ctx context.Context,
	applicationID core.ApplicationID,
	reference persistence.ProtectedReference,
) (int64, error) {
	var referenceID int64
	err := repository.tx.QueryRow(ctx, `
		INSERT INTO provider_references (
			application_id,
			role,
			kind,
			value_ciphertext,
			value_fingerprint,
			encryption_key_id
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (application_id, role, kind, value_fingerprint)
		DO UPDATE SET application_id = EXCLUDED.application_id
		RETURNING id
	`,
		applicationID,
		reference.Role,
		reference.Kind,
		reference.Value.Ciphertext,
		reference.Value.Fingerprint[:],
		reference.Value.KeyID,
	).Scan(&referenceID)
	if err != nil {
		return 0, classifyError("save provider reference", err)
	}
	return referenceID, nil
}

// verifyObservationReferences ensures an existing observation has the same immutable reference set.
func (repository *transaction) verifyObservationReferences(
	ctx context.Context,
	write persistence.ObservationWrite,
) error {
	rows, err := repository.tx.Query(ctx, `
		SELECT pr.role, pr.kind, pr.value_fingerprint
		FROM observation_references AS observation_reference
		JOIN provider_references AS pr
			ON pr.id = observation_reference.reference_id
			AND pr.application_id = observation_reference.application_id
		WHERE observation_reference.observation_id = $1
	`, write.Observation.ID)
	if err != nil {
		return classifyError("load observation references", err)
	}
	defer rows.Close()

	existing := make(map[string]struct{}, len(write.References))
	for rows.Next() {
		var role string
		var kind string
		var fingerprint []byte
		if err := rows.Scan(&role, &kind, &fingerprint); err != nil {
			return classifyError("scan observation reference", err)
		}
		identity, err := newReferenceIdentity(core.ReferenceRole(role), kind, fingerprint)
		if err != nil {
			return err
		}
		existing[identity.key()] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return classifyError("iterate observation references", err)
	}
	if len(existing) != len(write.References) {
		return fmt.Errorf("save purchase observation references: %w", persistence.ErrConflict)
	}
	for _, reference := range write.References {
		identity := referenceIdentity{
			role:        reference.Role,
			kind:        reference.Kind,
			fingerprint: reference.Value.Fingerprint,
		}
		if _, exists := existing[identity.key()]; !exists {
			return fmt.Errorf("save purchase observation references: %w", persistence.ErrConflict)
		}
	}
	return nil
}

// newReferenceIdentity converts a database fingerprint into a fixed-size safe identity.
func newReferenceIdentity(
	role core.ReferenceRole,
	kind string,
	fingerprint []byte,
) (referenceIdentity, error) {
	if len(fingerprint) != 32 {
		return referenceIdentity{}, errors.New("stored provider reference fingerprint has invalid length")
	}
	identity := referenceIdentity{role: role, kind: kind}
	copy(identity.fingerprint[:], fingerprint)
	return identity, nil
}

// key returns a non-secret map key for an encrypted reference identity.
func (identity referenceIdentity) key() string {
	return string(identity.role) + "\x00" + identity.kind + "\x00" + hex.EncodeToString(identity.fingerprint[:])
}

// normalizeTime matches PostgreSQL timestamptz microsecond precision in idempotency comparisons.
func normalizeTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

// nullableTime converts a zero timestamp into SQL NULL.
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return normalizeTime(value)
}

// nullableTimePointer converts an optional timestamp into a normalized SQL value.
func nullableTimePointer(value *time.Time) any {
	if value == nil {
		return nil
	}
	return normalizeTime(*value)
}

// nullableString converts an empty string into SQL NULL.
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// protectedFingerprintMatches compares a stored fingerprint without leaking it.
func protectedFingerprintMatches(stored []byte, expected [32]byte) bool {
	return hmac.Equal(stored, expected[:])
}
