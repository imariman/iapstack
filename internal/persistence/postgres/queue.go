package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/riverqueue/river"
)

// SaveInboxMessage stores one protected provider notification and enqueues its identifier atomically.
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
	messageID := message.ID
	if command.RowsAffected() == 0 {
		err = repository.tx.QueryRow(ctx, `
			SELECT id FROM inbox_messages WHERE application_id = $1 AND payload_fingerprint = $2
		`, message.ApplicationID, message.Payload.Fingerprint[:]).Scan(&messageID)
		if err != nil {
			return "", classifyError("resolve inbox message", err)
		}
		if err := repository.requeueFailedInbox(ctx, messageID, message.AvailableAt); err != nil {
			return "", err
		}
	}
	if err := repository.ensureRiverJob(ctx, persistence.QueueInbox, messageID, message.AvailableAt); err != nil {
		return "", err
	}
	return messageID, nil
}

// SaveReconciliationJob stores one protected provider query and enqueues its identifier atomically.
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
	jobID := job.ID
	if command.RowsAffected() == 0 {
		err = repository.tx.QueryRow(ctx, `
			SELECT id FROM reconciliation_jobs
			WHERE application_id = $1 AND customer_id = $2 AND payload_fingerprint = $3
		`, job.ApplicationID, job.CustomerID, job.Payload.Fingerprint[:]).Scan(&jobID)
		if err != nil {
			return "", classifyError("resolve reconciliation job", err)
		}
	}
	if err := repository.ensureRiverJob(ctx, persistence.QueueReconciliation, jobID, job.AvailableAt); err != nil {
		return "", err
	}
	return jobID, nil
}

// QueueMessage returns one durable payload and terminal audit state by identity.
func (repository *transaction) QueueMessage(
	ctx context.Context,
	queue persistence.QueueName,
	id string,
) (persistence.QueueMessage, error) {
	if err := errors.Join(queue.Validate(), validateText("queue record ID", id)); err != nil {
		return persistence.QueueMessage{}, err
	}
	switch queue {
	case persistence.QueueInbox:
		return repository.inboxMessage(ctx, id)
	case persistence.QueueOutbox:
		return repository.outboxMessage(ctx, id)
	case persistence.QueueReconciliation:
		return repository.reconciliationMessage(ctx, id)
	default:
		return persistence.QueueMessage{}, fmt.Errorf("load queue message: unsupported queue %q", queue)
	}
}

// CompleteQueue records successful processing or delivery idempotently.
func (repository *transaction) CompleteQueue(ctx context.Context, completion persistence.QueueCompletion) error {
	if err := completion.Validate(); err != nil {
		return err
	}
	table, completedColumn, err := queueCompletion(completion.Queue)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
		UPDATE %s SET %s = COALESCE(%s, $1), failed_at = NULL,
			last_error_code = NULL, updated_at = GREATEST(updated_at, $1)
		WHERE id = $2 AND failed_at IS NULL
	`, table, completedColumn, completedColumn)
	return repository.changeQueueOutcome(ctx, query, completion.CompletedAt, completion.ID)
}

// FailQueue records one permanent processing or delivery failure idempotently.
func (repository *transaction) FailQueue(ctx context.Context, failure persistence.QueueFailure) error {
	if err := failure.Validate(); err != nil {
		return err
	}
	table, completedColumn, err := queueCompletion(failure.Queue)
	if err != nil {
		return err
	}
	query := fmt.Sprintf(`
		UPDATE %s SET failed_at = COALESCE(failed_at, $1), last_error_code = COALESCE(last_error_code, $2),
			updated_at = GREATEST(updated_at, $1)
		WHERE id = $3 AND %s IS NULL
	`, table, completedColumn)
	return repository.changeQueueOutcome(ctx, query, failure.FailedAt, failure.ErrorCode, failure.ID)
}

// PurgeTerminalQueueRecords removes expired queue records after their River jobs become terminal or disappear.
func (repository *transaction) PurgeTerminalQueueRecords(
	ctx context.Context,
	before time.Time,
	batchSize int,
) (int64, error) {
	if before.IsZero() {
		return 0, errors.New("terminal queue retention cutoff is required")
	}
	if batchSize <= 0 {
		return 0, errors.New("terminal queue retention batch size must be positive")
	}
	var deleted int64
	err := repository.tx.QueryRow(ctx, `
		WITH expired_inbox AS (
			SELECT record.id FROM inbox_messages AS record
			WHERE (record.processed_at < $1 OR record.failed_at < $1)
				AND NOT EXISTS (
					SELECT 1 FROM river_job AS job
					WHERE job.id = record.river_job_id
						AND job.state NOT IN ('cancelled', 'completed', 'discarded')
				)
			ORDER BY COALESCE(record.processed_at, record.failed_at), record.id
			LIMIT $2
			FOR UPDATE OF record SKIP LOCKED
		), deleted_inbox AS (
			DELETE FROM inbox_messages AS record USING expired_inbox AS expired
			WHERE record.id = expired.id
			RETURNING 1
		), expired_reconciliation AS (
			SELECT record.id FROM reconciliation_jobs AS record
			WHERE (record.processed_at < $1 OR record.failed_at < $1)
				AND NOT EXISTS (
					SELECT 1 FROM river_job AS job
					WHERE job.id = record.river_job_id
						AND job.state NOT IN ('cancelled', 'completed', 'discarded')
				)
			ORDER BY COALESCE(record.processed_at, record.failed_at), record.id
			LIMIT $2
			FOR UPDATE OF record SKIP LOCKED
		), deleted_reconciliation AS (
			DELETE FROM reconciliation_jobs AS record USING expired_reconciliation AS expired
			WHERE record.id = expired.id
			RETURNING 1
		), expired_outbox AS (
			SELECT record.id FROM outbox_events AS record
			WHERE (record.delivered_at < $1 OR record.failed_at < $1)
				AND NOT EXISTS (
					SELECT 1 FROM river_job AS job
					WHERE job.id = record.river_job_id
						AND job.state NOT IN ('cancelled', 'completed', 'discarded')
				)
			ORDER BY COALESCE(record.delivered_at, record.failed_at), record.id
			LIMIT $2
			FOR UPDATE OF record SKIP LOCKED
		), deleted_outbox AS (
			DELETE FROM outbox_events AS record USING expired_outbox AS expired
			WHERE record.id = expired.id
			RETURNING 1
		)
		SELECT (SELECT count(*) FROM deleted_inbox)
			+ (SELECT count(*) FROM deleted_reconciliation)
			+ (SELECT count(*) FROM deleted_outbox)
	`, before.UTC(), batchSize).Scan(&deleted)
	if err != nil {
		return 0, classifyError("purge terminal queue records", err)
	}
	return deleted, nil
}

// requeueFailedInbox clears only a terminal failure so an explicit provider redelivery can run again.
func (repository *transaction) requeueFailedInbox(
	ctx context.Context,
	messageID string,
	availableAt time.Time,
) error {
	command, err := repository.tx.Exec(ctx, `
		UPDATE inbox_messages
		SET failed_at = NULL,
			last_error_code = NULL,
			river_job_id = NULL,
			available_at = GREATEST(received_at, $2),
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
			AND failed_at IS NOT NULL
			AND processed_at IS NULL
	`, messageID, normalizeTime(availableAt))
	if err != nil {
		return classifyError("requeue failed inbox message", err)
	}
	if command.RowsAffected() > 1 {
		return fmt.Errorf("requeue failed inbox message: %w", persistence.ErrConflict)
	}
	return nil
}

// ensureRiverJob creates exactly one River job for a durable record inside the caller transaction.
func (repository *transaction) ensureRiverJob(
	ctx context.Context,
	queue persistence.QueueName,
	recordID string,
	availableAt time.Time,
) error {
	if repository.river == nil {
		return errors.New("river insert client is not initialized")
	}
	table, err := queueTable(queue)
	if err != nil {
		return err
	}
	var existingJobID *int64
	query := fmt.Sprintf(`SELECT river_job_id FROM %s WHERE id = $1 FOR UPDATE`, table)
	if err := repository.tx.QueryRow(ctx, query, recordID).Scan(&existingJobID); err != nil {
		return classifyError("lock durable job record", err)
	}
	if existingJobID != nil {
		return nil
	}
	var resultJobID int64
	options := &river.InsertOpts{Queue: riverQueue(queue), ScheduledAt: availableAt}
	switch queue {
	case persistence.QueueInbox:
		result, insertErr := repository.river.InsertTx(ctx, repository.tx, jobs.InboxArgs{MessageID: recordID}, options)
		if insertErr != nil {
			return fmt.Errorf("enqueue inbox River job: %w", insertErr)
		}
		resultJobID = result.Job.ID
	case persistence.QueueOutbox:
		result, insertErr := repository.river.InsertTx(ctx, repository.tx, jobs.OutboxArgs{EventID: recordID}, options)
		if insertErr != nil {
			return fmt.Errorf("enqueue outbox River job: %w", insertErr)
		}
		resultJobID = result.Job.ID
	case persistence.QueueReconciliation:
		result, insertErr := repository.river.InsertTx(ctx, repository.tx, jobs.ReconciliationArgs{JobID: recordID}, options)
		if insertErr != nil {
			return fmt.Errorf("enqueue reconciliation River job: %w", insertErr)
		}
		resultJobID = result.Job.ID
	default:
		return fmt.Errorf("enqueue River job: unsupported queue %q", queue)
	}
	update := fmt.Sprintf(`UPDATE %s SET river_job_id = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, table)
	command, err := repository.tx.Exec(ctx, update, resultJobID, recordID)
	if err != nil {
		return classifyError("link River job", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("link River job: %w", persistence.ErrConflict)
	}
	return nil
}

// inboxMessage loads one protected provider notification audit record.
func (repository *transaction) inboxMessage(ctx context.Context, id string) (persistence.QueueMessage, error) {
	message := persistence.QueueMessage{Queue: persistence.QueueInbox}
	var ciphertext, fingerprint []byte
	var completedAt, failedAt *time.Time
	err := repository.tx.QueryRow(ctx, `
		SELECT id, project_id, application_id, provider, kind, content_type,
			payload_ciphertext, payload_fingerprint, encryption_key_id, received_at,
			processed_at, failed_at
		FROM inbox_messages WHERE id = $1
	`, id).Scan(&message.ID, &message.ProjectID, &message.ApplicationID, &message.Provider,
		&message.Kind, &message.ContentType, &ciphertext, &fingerprint,
		&message.ProtectedPayload.KeyID, &message.OccurredAt, &completedAt, &failedAt)
	if err != nil {
		return persistence.QueueMessage{}, classifyError("load inbox message", err)
	}
	value, err := protectedValue(ciphertext, fingerprint, message.ProtectedPayload.KeyID)
	if err != nil {
		return persistence.QueueMessage{}, fmt.Errorf("validate inbox protected payload: %w", err)
	}
	message.ProtectedPayload = value
	message.Completed = completedAt != nil
	message.Failed = failedAt != nil
	return message, nil
}

// outboxMessage loads one immutable application event audit record.
func (repository *transaction) outboxMessage(ctx context.Context, id string) (persistence.QueueMessage, error) {
	message := persistence.QueueMessage{Queue: persistence.QueueOutbox}
	var completedAt, failedAt *time.Time
	err := repository.tx.QueryRow(ctx, `
		SELECT id, project_id, application_id, event_type, payload, occurred_at,
			delivered_at, failed_at
		FROM outbox_events WHERE id = $1
	`, id).Scan(&message.ID, &message.ProjectID, &message.ApplicationID, &message.Kind,
		&message.JSONPayload, &message.OccurredAt, &completedAt, &failedAt)
	if err != nil {
		return persistence.QueueMessage{}, classifyError("load outbox message", err)
	}
	if !json.Valid(message.JSONPayload) {
		return persistence.QueueMessage{}, errors.New("durable outbox payload is invalid JSON")
	}
	message.Completed = completedAt != nil
	message.Failed = failedAt != nil
	return message, nil
}

// reconciliationMessage loads one protected provider refresh audit record.
func (repository *transaction) reconciliationMessage(ctx context.Context, id string) (persistence.QueueMessage, error) {
	message := persistence.QueueMessage{Queue: persistence.QueueReconciliation}
	var ciphertext, fingerprint []byte
	var completedAt, failedAt *time.Time
	err := repository.tx.QueryRow(ctx, `
		SELECT id, project_id, application_id, customer_id,
			payload_ciphertext, payload_fingerprint, encryption_key_id, created_at,
			processed_at, failed_at
		FROM reconciliation_jobs WHERE id = $1
	`, id).Scan(&message.ID, &message.ProjectID, &message.ApplicationID, &message.CustomerID,
		&ciphertext, &fingerprint, &message.ProtectedPayload.KeyID, &message.OccurredAt,
		&completedAt, &failedAt)
	if err != nil {
		return persistence.QueueMessage{}, classifyError("load reconciliation message", err)
	}
	value, err := protectedValue(ciphertext, fingerprint, message.ProtectedPayload.KeyID)
	if err != nil {
		return persistence.QueueMessage{}, fmt.Errorf("validate reconciliation protected payload: %w", err)
	}
	message.ProtectedPayload = value
	message.Completed = completedAt != nil
	message.Failed = failedAt != nil
	return message, nil
}

// changeQueueOutcome executes one idempotent terminal audit update.
func (repository *transaction) changeQueueOutcome(ctx context.Context, query string, arguments ...any) error {
	command, err := repository.tx.Exec(ctx, query, arguments...)
	if err != nil {
		return classifyError("change queue outcome", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("change queue outcome: %w", persistence.ErrConflict)
	}
	return nil
}

// queueTable maps a closed queue name to its audit table.
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

// queueCompletion maps a queue to its successful terminal timestamp.
func queueCompletion(queue persistence.QueueName) (string, string, error) {
	table, err := queueTable(queue)
	if err != nil {
		return "", "", err
	}
	if queue == persistence.QueueOutbox {
		return table, "delivered_at", nil
	}
	return table, "processed_at", nil
}

// riverQueue maps one persistence queue to its bounded River queue name.
func riverQueue(queue persistence.QueueName) string {
	switch queue {
	case persistence.QueueInbox:
		return jobs.QueueInbox
	case persistence.QueueOutbox:
		return jobs.QueueOutbox
	case persistence.QueueReconciliation:
		return jobs.QueueReconciliation
	default:
		return ""
	}
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
