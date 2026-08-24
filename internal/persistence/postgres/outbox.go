package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
)

// outboxIdentity contains the non-payload fields used to validate an event ID collision.
type outboxIdentity struct {
	projectID          string
	applicationID      string
	eventType          string
	aggregateType      string
	aggregateID        string
	payloadFingerprint []byte
}

// SaveOutboxEvent stores one logical event idempotently and returns its durable identity.
func (repository *transaction) SaveOutboxEvent(
	ctx context.Context,
	event persistence.OutboxEvent,
) (string, error) {
	if err := event.Validate(); err != nil {
		return "", err
	}

	var eventID string
	err := repository.tx.QueryRow(ctx, `
		INSERT INTO outbox_events (
			id,
			project_id,
			application_id,
			event_type,
			aggregate_type,
			aggregate_id,
			payload,
			payload_fingerprint,
			occurred_at,
			available_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT DO NOTHING
		RETURNING id
	`,
		event.ID,
		event.ProjectID,
		event.ApplicationID,
		event.EventType,
		event.AggregateType,
		event.AggregateID,
		event.Payload,
		event.PayloadFingerprint[:],
		normalizeTime(event.OccurredAt),
		normalizeTime(event.AvailableAt),
	).Scan(&eventID)
	if err == nil {
		if err := repository.ensureRiverJob(ctx, persistence.QueueOutbox, eventID, event.AvailableAt); err != nil {
			return "", err
		}
		return eventID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", classifyError("save outbox event", err)
	}

	identity, err := repository.loadOutboxIdentity(ctx, event.ID)
	if err == nil {
		if !outboxIdentityMatches(identity, event) {
			return "", fmt.Errorf("save outbox event: %w", persistence.ErrConflict)
		}
		eventID = event.ID
		if err := repository.ensureRiverJob(ctx, persistence.QueueOutbox, eventID, event.AvailableAt); err != nil {
			return "", err
		}
		return eventID, nil
	}
	if !errors.Is(err, persistence.ErrNotFound) {
		return "", err
	}

	err = repository.tx.QueryRow(ctx, `
		SELECT id
		FROM outbox_events
		WHERE application_id = $1
			AND event_type = $2
			AND aggregate_type = $3
			AND aggregate_id = $4
			AND payload_fingerprint = $5
	`,
		event.ApplicationID,
		event.EventType,
		event.AggregateType,
		event.AggregateID,
		event.PayloadFingerprint[:],
	).Scan(&eventID)
	if err != nil {
		return "", classifyError("load existing logical outbox event", err)
	}
	if err := repository.ensureRiverJob(ctx, persistence.QueueOutbox, eventID, event.AvailableAt); err != nil {
		return "", err
	}
	return eventID, nil
}

// loadOutboxIdentity loads the safe identity fields for one durable event ID.
func (repository *transaction) loadOutboxIdentity(
	ctx context.Context,
	eventID string,
) (outboxIdentity, error) {
	identity := outboxIdentity{}
	err := repository.tx.QueryRow(ctx, `
		SELECT
			project_id,
			application_id,
			event_type,
			aggregate_type,
			aggregate_id,
			payload_fingerprint
		FROM outbox_events
		WHERE id = $1
	`, eventID).Scan(
		&identity.projectID,
		&identity.applicationID,
		&identity.eventType,
		&identity.aggregateType,
		&identity.aggregateID,
		&identity.payloadFingerprint,
	)
	if err != nil {
		return outboxIdentity{}, classifyError("load outbox event identity", err)
	}
	return identity, nil
}

// outboxIdentityMatches checks whether an event ID describes the same logical event.
func outboxIdentityMatches(identity outboxIdentity, event persistence.OutboxEvent) bool {
	return identity.projectID == string(event.ProjectID) &&
		identity.applicationID == string(event.ApplicationID) &&
		identity.eventType == event.EventType &&
		identity.aggregateType == event.AggregateType &&
		identity.aggregateID == event.AggregateID &&
		protectedFingerprintMatches(identity.payloadFingerprint, event.PayloadFingerprint)
}
