package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/jackc/pgx/v5"
)

// SaveInboxMessage stores one protected provider notification idempotently.
func (repository *transaction) SaveInboxMessage(ctx context.Context, message persistence.InboxMessage) (string, error) {
	if err := message.Validate(); err != nil {
		return "", err
	}
	command, err := repository.tx.Exec(ctx, `
		INSERT INTO inbox_messages (
			id, project_id, application_id, provider, kind, content_type,
			payload_ciphertext, payload_fingerprint, encryption_key_id, received_at, available_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (application_id, payload_fingerprint) DO NOTHING
	`, message.ID, message.ProjectID, message.ApplicationID, message.Provider, message.Kind, message.ContentType,
		message.Payload.Ciphertext, message.Payload.Fingerprint[:], message.Payload.KeyID,
		message.ReceivedAt, message.AvailableAt)
	if err != nil {
		return "", classifyError("save inbox message", err)
	}
	if command.RowsAffected() == 1 {
		return message.ID, nil
	}
	var existingID string
	err = repository.tx.QueryRow(ctx, `
		SELECT id FROM inbox_messages WHERE application_id = $1 AND payload_fingerprint = $2
	`, message.ApplicationID, message.Payload.Fingerprint[:]).Scan(&existingID)
	if err != nil {
		return "", classifyError("resolve inbox message", err)
	}
	return existingID, nil
}

// SaveReconciliationJob stores one protected provider query idempotently.
func (repository *transaction) SaveReconciliationJob(
	ctx context.Context,
	job persistence.ReconciliationJob,
) (string, error) {
	if err := job.Validate(); err != nil {
		return "", err
	}
	command, err := repository.tx.Exec(ctx, `
		INSERT INTO reconciliation_jobs (
			id, project_id, application_id, customer_id, payload_ciphertext,
			payload_fingerprint, encryption_key_id, available_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (application_id, customer_id, payload_fingerprint) DO NOTHING
	`, job.ID, job.ProjectID, job.ApplicationID, job.CustomerID, job.Payload.Ciphertext,
		job.Payload.Fingerprint[:], job.Payload.KeyID, job.AvailableAt)
	if err != nil {
		return "", classifyError("save reconciliation job", err)
	}
	if command.RowsAffected() == 1 {
		return job.ID, nil
	}
	var existingID string
	err = repository.tx.QueryRow(ctx, `
		SELECT id FROM reconciliation_jobs
		WHERE application_id = $1 AND customer_id = $2 AND payload_fingerprint = $3
	`, job.ApplicationID, job.CustomerID, job.Payload.Fingerprint[:]).Scan(&existingID)
	if err != nil {
		return "", classifyError("resolve reconciliation job", err)
	}
	return existingID, nil
}

// ClaimQueue atomically recovers stale locks and leases available records to one worker.
func (repository *transaction) ClaimQueue(
	ctx context.Context,
	claim persistence.QueueClaim,
) ([]persistence.QueueMessage, error) {
	if err := claim.Validate(); err != nil {
		return nil, err
	}
	if _, err := repository.RecoverQueueLocks(ctx, claim.Queue, claim.StaleBefore); err != nil {
		return nil, err
	}
	switch claim.Queue {
	case persistence.QueueInbox:
		return repository.claimInbox(ctx, claim)
	case persistence.QueueOutbox:
		return repository.claimOutbox(ctx, claim)
	case persistence.QueueReconciliation:
		return repository.claimReconciliation(ctx, claim)
	default:
		return nil, fmt.Errorf("claim queue: unsupported queue %q", claim.Queue)
	}
}

// CompleteQueue marks one worker-owned record delivered or processed.
func (repository *transaction) CompleteQueue(ctx context.Context, transition persistence.QueueTransition) error {
	if err := transition.Validate(); err != nil {
		return err
	}
	table, completedColumn, terminalState, err := queueCompletion(transition.Queue)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
		UPDATE %s SET state = $1, %s = $2, locked_at = NULL, locked_by = NULL,
			last_error_code = NULL, updated_at = $2
		WHERE id = $3 AND state = 'processing' AND locked_by = $4
	`, table, completedColumn)
	return repository.changeQueueState(ctx, query, terminalState, transition.CompletedAt, transition.ID, transition.WorkerID)
}

// RetryQueue returns one worker-owned record to pending with a future schedule.
func (repository *transaction) RetryQueue(ctx context.Context, transition persistence.QueueTransition) error {
	if err := transition.Validate(); err != nil {
		return err
	}
	if transition.AvailableAt.IsZero() || transition.AvailableAt.Before(transition.CompletedAt) {
		return errors.New("queue retry availability must not precede completion")
	}
	table, err := queueTable(transition.Queue)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
		UPDATE %s SET state = 'pending', available_at = $1, locked_at = NULL, locked_by = NULL,
			last_error_code = $2, updated_at = $3
		WHERE id = $4 AND state = 'processing' AND locked_by = $5
	`, table)
	return repository.changeQueueState(ctx, query, transition.AvailableAt, transition.ErrorCode,
		transition.CompletedAt, transition.ID, transition.WorkerID)
}

// FailQueue moves one worker-owned record to its terminal failed state.
func (repository *transaction) FailQueue(ctx context.Context, transition persistence.QueueTransition) error {
	if err := transition.Validate(); err != nil {
		return err
	}
	table, err := queueTable(transition.Queue)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
		UPDATE %s SET state = 'failed', locked_at = NULL, locked_by = NULL,
			last_error_code = $1, updated_at = $2
		WHERE id = $3 AND state = 'processing' AND locked_by = $4
	`, table)
	return repository.changeQueueState(ctx, query, transition.ErrorCode,
		transition.CompletedAt, transition.ID, transition.WorkerID)
}

// RecoverQueueLocks returns stale processing records to pending.
func (repository *transaction) RecoverQueueLocks(
	ctx context.Context,
	queue persistence.QueueName,
	staleBefore time.Time,
) (int64, error) {
	var timestampError error
	if staleBefore.IsZero() {
		timestampError = errors.New("queue stale-before time is required")
	}
	if err := errors.Join(queue.Validate(), timestampError); err != nil {
		return 0, err
	}
	table, err := queueTable(queue)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`
		UPDATE %s SET state = 'pending', locked_at = NULL, locked_by = NULL,
			available_at = LEAST(available_at, $1), last_error_code = 'stale_lock', updated_at = $1
		WHERE state = 'processing' AND locked_at < $1
	`, table)
	command, err := repository.tx.Exec(ctx, query, staleBefore)
	if err != nil {
		return 0, classifyError("recover queue locks", err)
	}
	return command.RowsAffected(), nil
}

// QueueDepth returns current counts grouped by durable state.
func (repository *transaction) QueueDepth(
	ctx context.Context,
	queue persistence.QueueName,
) (map[string]int64, error) {
	if err := queue.Validate(); err != nil {
		return nil, err
	}
	table, err := queueTable(queue)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT state, count(*) FROM %s GROUP BY state`, table)
	rows, err := repository.tx.Query(ctx, query)
	if err != nil {
		return nil, classifyError("query queue depth", err)
	}
	defer rows.Close()
	depth := make(map[string]int64)
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return nil, classifyError("scan queue depth", err)
		}
		depth[state] = count
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate queue depth", err)
	}
	return depth, nil
}

// PruneQueue deletes a bounded batch of terminal records older than one retention boundary.
func (repository *transaction) PruneQueue(
	ctx context.Context,
	prune persistence.QueuePrune,
) (int64, error) {
	if err := prune.Validate(); err != nil {
		return 0, err
	}
	table, completedColumn, terminalState, err := queueCompletion(prune.Queue)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf(`
		WITH candidates AS (
			SELECT id FROM %s
			WHERE state IN ($1, 'failed') AND COALESCE(%s, updated_at) < $2
			ORDER BY COALESCE(%s, updated_at), id
			FOR UPDATE SKIP LOCKED LIMIT $3
		)
		DELETE FROM %s AS record USING candidates
		WHERE record.id = candidates.id
	`, table, completedColumn, completedColumn, table)
	command, err := repository.tx.Exec(ctx, query, terminalState, prune.Before, prune.Limit)
	if err != nil {
		return 0, classifyError("prune queue", err)
	}
	return command.RowsAffected(), nil
}

// claimInbox leases protected provider notification records.
func (repository *transaction) claimInbox(
	ctx context.Context,
	claim persistence.QueueClaim,
) ([]persistence.QueueMessage, error) {
	rows, err := repository.tx.Query(ctx, `
		WITH candidates AS (
			SELECT id FROM inbox_messages
			WHERE state = 'pending' AND available_at <= $1
			ORDER BY available_at, received_at
			FOR UPDATE SKIP LOCKED LIMIT $2
		)
		UPDATE inbox_messages AS message
		SET state = 'processing', attempts = attempts + 1, locked_at = $1, locked_by = $3, updated_at = $1
		FROM candidates WHERE message.id = candidates.id
		RETURNING message.id, message.project_id, message.application_id, message.provider,
			message.kind, message.content_type, message.payload_ciphertext, message.payload_fingerprint,
			message.encryption_key_id, message.attempts, message.received_at
	`, claim.Now, claim.Limit, claim.WorkerID)
	if err != nil {
		return nil, classifyError("claim inbox", err)
	}
	defer rows.Close()
	return scanProtectedQueueRows(rows, persistence.QueueInbox, true)
}

// claimOutbox leases JSON application webhook events.
func (repository *transaction) claimOutbox(
	ctx context.Context,
	claim persistence.QueueClaim,
) ([]persistence.QueueMessage, error) {
	rows, err := repository.tx.Query(ctx, `
		WITH candidates AS (
			SELECT id FROM outbox_events
			WHERE state = 'pending' AND available_at <= $1
			ORDER BY available_at, occurred_at
			FOR UPDATE SKIP LOCKED LIMIT $2
		)
		UPDATE outbox_events AS event
		SET state = 'processing', attempts = attempts + 1, locked_at = $1, locked_by = $3, updated_at = $1
		FROM candidates WHERE event.id = candidates.id
		RETURNING event.id, event.project_id, event.application_id, event.event_type,
			event.payload, event.attempts, event.occurred_at
	`, claim.Now, claim.Limit, claim.WorkerID)
	if err != nil {
		return nil, classifyError("claim outbox", err)
	}
	defer rows.Close()
	messages := make([]persistence.QueueMessage, 0)
	for rows.Next() {
		var message persistence.QueueMessage
		message.Queue = persistence.QueueOutbox
		if err := rows.Scan(&message.ID, &message.ProjectID, &message.ApplicationID, &message.Kind,
			&message.JSONPayload, &message.Attempts, &message.OccurredAt); err != nil {
			return nil, classifyError("scan outbox claim", err)
		}
		if !json.Valid(message.JSONPayload) {
			return nil, errors.New("claimed outbox payload is invalid JSON")
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate outbox claim", err)
	}
	return messages, nil
}

// claimReconciliation leases protected provider reconciliation records.
func (repository *transaction) claimReconciliation(
	ctx context.Context,
	claim persistence.QueueClaim,
) ([]persistence.QueueMessage, error) {
	rows, err := repository.tx.Query(ctx, `
		WITH candidates AS (
			SELECT id FROM reconciliation_jobs
			WHERE state = 'pending' AND available_at <= $1
			ORDER BY available_at, created_at
			FOR UPDATE SKIP LOCKED LIMIT $2
		)
		UPDATE reconciliation_jobs AS job
		SET state = 'processing', attempts = attempts + 1, locked_at = $1, locked_by = $3, updated_at = $1
		FROM candidates WHERE job.id = candidates.id
		RETURNING job.id, job.project_id, job.application_id, job.customer_id,
			job.payload_ciphertext, job.payload_fingerprint, job.encryption_key_id,
			job.attempts, job.created_at
	`, claim.Now, claim.Limit, claim.WorkerID)
	if err != nil {
		return nil, classifyError("claim reconciliation", err)
	}
	defer rows.Close()
	return scanProtectedQueueRows(rows, persistence.QueueReconciliation, false)
}

// scanProtectedQueueRows converts inbox or reconciliation rows into safe queue messages.
func scanProtectedQueueRows(
	rows pgx.Rows,
	queue persistence.QueueName,
	inbox bool,
) ([]persistence.QueueMessage, error) {
	messages := make([]persistence.QueueMessage, 0)
	for rows.Next() {
		message := persistence.QueueMessage{Queue: queue}
		var ciphertext, fingerprint []byte
		if inbox {
			if err := rows.Scan(&message.ID, &message.ProjectID, &message.ApplicationID, &message.Provider,
				&message.Kind, &message.ContentType, &ciphertext, &fingerprint,
				&message.ProtectedPayload.KeyID, &message.Attempts, &message.OccurredAt); err != nil {
				return nil, classifyError("scan inbox claim", err)
			}
		} else {
			if err := rows.Scan(&message.ID, &message.ProjectID, &message.ApplicationID, &message.CustomerID,
				&ciphertext, &fingerprint, &message.ProtectedPayload.KeyID,
				&message.Attempts, &message.OccurredAt); err != nil {
				return nil, classifyError("scan reconciliation claim", err)
			}
		}
		if len(fingerprint) != len(message.ProtectedPayload.Fingerprint) {
			return nil, errors.New("claimed protected payload fingerprint has invalid length")
		}
		message.ProtectedPayload.Ciphertext = append([]byte(nil), ciphertext...)
		copy(message.ProtectedPayload.Fingerprint[:], fingerprint)
		if err := message.ProtectedPayload.Validate(); err != nil {
			return nil, fmt.Errorf("validate claimed protected payload: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate protected queue claim", err)
	}
	return messages, nil
}

// changeQueueState executes one worker-owned transition and rejects lost leases.
func (repository *transaction) changeQueueState(ctx context.Context, query string, arguments ...any) error {
	command, err := repository.tx.Exec(ctx, query, arguments...)
	if err != nil {
		return classifyError("change queue state", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("change queue state: %w", persistence.ErrConflict)
	}
	return nil
}

// queueTable maps a closed queue name to its SQL table.
func queueTable(queue persistence.QueueName) (string, error) {
	switch queue {
	case persistence.QueueInbox:
		return "inbox_messages", nil
	case persistence.QueueOutbox:
		return "outbox_events", nil
	case persistence.QueueReconciliation:
		return "reconciliation_jobs", nil
	default:
		return "", fmt.Errorf("unsupported queue %q", queue)
	}
}

// queueCompletion maps a queue to its successful state and timestamp column.
func queueCompletion(queue persistence.QueueName) (string, string, string, error) {
	table, err := queueTable(queue)
	if err != nil {
		return "", "", "", err
	}
	if queue == persistence.QueueOutbox {
		return table, "delivered_at", "delivered", nil
	}
	return table, "processed_at", "processed", nil
}

// protectedValue constructs one protected payload from durable byte fields.
func protectedValue(ciphertext, fingerprint []byte, keyID string) (protection.Value, error) {
	value := protection.Value{Ciphertext: append([]byte(nil), ciphertext...), KeyID: keyID}
	if len(fingerprint) != len(value.Fingerprint) {
		return protection.Value{}, errors.New("protected fingerprint has invalid length")
	}
	copy(value.Fingerprint[:], fingerprint)
	return value, value.Validate()
}
