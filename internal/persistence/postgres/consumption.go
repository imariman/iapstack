package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

const (
	// maximumDeliveredPurchaseCustomers bounds the customers returned for one reference.
	maximumDeliveredPurchaseCustomers = 16
)

// AppleConsumptionSettings returns one application's saved consumption settings.
func (repository *transaction) AppleConsumptionSettings(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) (persistence.AppleConsumptionSettings, error) {
	if err := errors.Join(projectID.Validate(), applicationID.Validate()); err != nil {
		return persistence.AppleConsumptionSettings{}, err
	}
	var settings persistence.AppleConsumptionSettings
	var actor *string
	err := repository.tx.QueryRow(ctx, `
		SELECT project_id, application_id, enabled, refund_preference,
			sample_content_provided, revision, updated_by_key_id, created_at, updated_at
		FROM apple_consumption_settings
		WHERE project_id = $1 AND application_id = $2
	`, projectID, applicationID).Scan(
		&settings.ProjectID, &settings.ApplicationID, &settings.Enabled, &settings.RefundPreference, &settings.SampleContentProvided, &settings.Revision, &actor,
		&settings.CreatedAt, &settings.UpdatedAt,
	)
	if err != nil {
		return persistence.AppleConsumptionSettings{}, classifyError("load Apple consumption settings", err)
	}
	if actor != nil {
		settings.UpdatedByKeyID = *actor
	}
	return settings, nil
}

// PutAppleConsumptionSettings creates settings at revision one or updates the expected revision.
// An identical retry of the last successful write returns the stored row instead of a conflict.
func (repository *transaction) PutAppleConsumptionSettings(
	ctx context.Context,
	write persistence.AppleConsumptionSettingsWrite,
) (persistence.AppleConsumptionSettings, error) {
	if err := write.Validate(); err != nil {
		return persistence.AppleConsumptionSettings{}, err
	}
	var affected int64
	if write.ExpectedRevision == 0 {
		command, err := repository.tx.Exec(ctx, `
			INSERT INTO apple_consumption_settings (
				project_id, application_id, enabled, refund_preference,
				sample_content_provided, updated_by_key_id
			) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (application_id) DO NOTHING
		`, write.ProjectID, write.ApplicationID, write.Enabled, write.RefundPreference,
			write.SampleContentProvided, nullableString(write.ActorKeyID))
		if err != nil {
			return persistence.AppleConsumptionSettings{}, classifyError("create Apple consumption settings", err)
		}
		affected = command.RowsAffected()
	} else {
		command, err := repository.tx.Exec(ctx, `
			UPDATE apple_consumption_settings
			SET enabled = $3, refund_preference = $4, sample_content_provided = $5,
				updated_by_key_id = $6, revision = revision + 1, updated_at = GREATEST(now(), created_at)
			WHERE project_id = $1 AND application_id = $2 AND revision = $7
		`, write.ProjectID, write.ApplicationID, write.Enabled, write.RefundPreference,
			write.SampleContentProvided, nullableString(write.ActorKeyID), write.ExpectedRevision)
		if err != nil {
			return persistence.AppleConsumptionSettings{}, classifyError("update Apple consumption settings", err)
		}
		affected = command.RowsAffected()
	}
	settings, err := repository.AppleConsumptionSettings(ctx, write.ProjectID, write.ApplicationID)
	if errors.Is(err, persistence.ErrNotFound) && write.ExpectedRevision > 0 {
		return persistence.AppleConsumptionSettings{}, fmt.Errorf("update Apple consumption settings: %w", persistence.ErrConflict)
	}
	if err != nil {
		return persistence.AppleConsumptionSettings{}, err
	}
	if affected == 1 {
		return settings, nil
	}
	if settings.Revision == write.ExpectedRevision+1 && sameConsumptionSettings(settings, write) {
		return settings, nil
	}
	return persistence.AppleConsumptionSettings{}, fmt.Errorf("write Apple consumption settings: %w", persistence.ErrConflict)
}

// AppleConsumptionConsent returns the latest consent decision recorded for one canonical customer.
// Decisions recorded before a merge stay on the source customer, so customers merged directly into it count
// too. The newest decision across them wins.
func (repository *transaction) AppleConsumptionConsent(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	customerID core.CustomerID,
) (persistence.AppleConsumptionConsent, error) {
	if err := errors.Join(projectID.Validate(), applicationID.Validate(), customerID.Validate()); err != nil {
		return persistence.AppleConsumptionConsent{}, err
	}
	var consent persistence.AppleConsumptionConsent
	err := repository.tx.QueryRow(ctx, `
		SELECT project_id, application_id, customer_id, consented, actor_key_id, created_at, updated_at
		FROM apple_consumption_consents AS consent
		WHERE consent.project_id = $1 AND consent.application_id = $2
			AND (consent.customer_id = $3 OR consent.customer_id IN (
				SELECT customer.id FROM customers AS customer
				WHERE customer.project_id = $1 AND customer.merged_into_customer_id = $3
			))
		ORDER BY consent.updated_at DESC, consent.customer_id = $3 DESC
		LIMIT 1
	`, projectID, applicationID, customerID).Scan(
		&consent.ProjectID, &consent.ApplicationID, &consent.CustomerID, &consent.Consented,
		&consent.ActorKeyID, &consent.CreatedAt, &consent.UpdatedAt,
	)
	if err != nil {
		return persistence.AppleConsumptionConsent{}, classifyError("load Apple consumption consent", err)
	}
	return consent, nil
}

// PutAppleConsumptionConsent stores the latest consent decision. The newest write wins.
func (repository *transaction) PutAppleConsumptionConsent(
	ctx context.Context,
	write persistence.AppleConsumptionConsentWrite,
) (persistence.AppleConsumptionConsent, error) {
	if err := write.Validate(); err != nil {
		return persistence.AppleConsumptionConsent{}, err
	}
	var consent persistence.AppleConsumptionConsent
	err := repository.tx.QueryRow(ctx, `
		INSERT INTO apple_consumption_consents (project_id, application_id, customer_id, consented, actor_key_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (application_id, customer_id) DO UPDATE
		SET consented = EXCLUDED.consented, actor_key_id = EXCLUDED.actor_key_id,
			updated_at = GREATEST(now(), apple_consumption_consents.created_at)
		WHERE apple_consumption_consents.project_id = EXCLUDED.project_id
		RETURNING project_id, application_id, customer_id, consented, actor_key_id, created_at, updated_at
	`, write.ProjectID, write.ApplicationID, write.CustomerID, write.Consented, write.ActorKeyID).Scan(
		&consent.ProjectID, &consent.ApplicationID, &consent.CustomerID, &consent.Consented,
		&consent.ActorKeyID, &consent.CreatedAt, &consent.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.AppleConsumptionConsent{}, fmt.Errorf("record Apple consumption consent: %w", persistence.ErrConflict)
	}
	if err != nil {
		return persistence.AppleConsumptionConsent{}, classifyError("record Apple consumption consent", err)
	}
	return consent, nil
}

// SaveAppleConsumptionRequest stores one request and its River job in the caller transaction.
func (repository *transaction) SaveAppleConsumptionRequest(
	ctx context.Context,
	request persistence.AppleConsumptionRequest,
) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	_, err := repository.tx.Exec(ctx, `
		INSERT INTO apple_consumption_requests (
			id, project_id, application_id, customer_id, provider_product_id, product_type, customer_reason,
			payload_ciphertext, payload_fingerprint, encryption_key_id,
			signed_at, received_at, deadline_at, available_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO NOTHING
	`, request.ID, request.ProjectID, request.ApplicationID, nullableString(string(request.CustomerID)),
		request.ProviderProductID, request.ProductType, nullableString(request.CustomerReason),
		request.Payload.Ciphertext, request.Payload.Fingerprint[:], request.Payload.KeyID,
		normalizeTime(request.SignedAt), normalizeTime(request.ReceivedAt), normalizeTime(request.DeadlineAt),
		normalizeTime(request.AvailableAt))
	if err != nil {
		return "", classifyError("save Apple consumption request", err)
	}
	if err := repository.ensureAppleConsumptionJob(ctx, request.ID); err != nil {
		return "", err
	}
	return request.ID, nil
}

// AppleConsumptionRequest loads one request, its protected payload, and its state.
func (repository *transaction) AppleConsumptionRequest(
	ctx context.Context,
	id string,
) (persistence.AppleConsumptionRequestRecord, error) {
	if err := validateText("Apple consumption request ID", id); err != nil {
		return persistence.AppleConsumptionRequestRecord{}, err
	}
	var record persistence.AppleConsumptionRequestRecord
	var customerID, reason, statusReason *string
	var ciphertext, fingerprint []byte
	err := repository.tx.QueryRow(ctx, `
		SELECT id, project_id, application_id, customer_id, provider_product_id, product_type, customer_reason,
			payload_ciphertext, payload_fingerprint, encryption_key_id,
			signed_at, received_at, deadline_at, available_at,
			status, status_reason, attempt_count, completed_at
		FROM apple_consumption_requests WHERE id = $1
	`, id).Scan(
		&record.ID, &record.ProjectID, &record.ApplicationID, &customerID, &record.ProviderProductID,
		&record.ProductType, &reason, &ciphertext, &fingerprint, &record.Payload.KeyID,
		&record.SignedAt, &record.ReceivedAt, &record.DeadlineAt, &record.AvailableAt,
		&record.Status, &statusReason, &record.AttemptCount, &record.CompletedAt,
	)
	if err != nil {
		return persistence.AppleConsumptionRequestRecord{}, classifyError("load Apple consumption request", err)
	}
	payload, err := protectedValue(ciphertext, fingerprint, record.Payload.KeyID)
	if err != nil {
		return persistence.AppleConsumptionRequestRecord{}, fmt.Errorf("validate Apple consumption payload: %w", err)
	}
	record.Payload = payload
	record.CustomerID = core.CustomerID(stringValue(customerID))
	record.CustomerReason = stringValue(reason)
	record.StatusReason = stringValue(statusReason)
	return record, nil
}

// RecordAppleConsumptionAttempt appends one numbered attempt and applies its status in one statement.
// Only a pending request that the worker has not closed accepts an attempt.
func (repository *transaction) RecordAppleConsumptionAttempt(
	ctx context.Context,
	attempt persistence.AppleConsumptionAttempt,
) (bool, error) {
	if err := attempt.Validate(); err != nil {
		return false, err
	}
	var status any
	if attempt.Status != "" {
		status = attempt.Status
	}
	var body any
	if len(attempt.RequestBody) > 0 {
		body = string(attempt.RequestBody)
	}
	var httpStatus, appleErrorCode any
	if attempt.HTTPStatus != 0 {
		httpStatus = attempt.HTTPStatus
	}
	if attempt.AppleErrorCode != 0 {
		appleErrorCode = attempt.AppleErrorCode
	}
	attemptedAt := normalizeTime(attempt.AttemptedAt)
	command, err := repository.tx.Exec(ctx, `
		WITH updated AS (
			UPDATE apple_consumption_requests
			SET attempt_count = attempt_count + 1,
				status = COALESCE($2::text, status),
				status_reason = $3,
				completed_at = CASE WHEN $2::text IS NULL THEN completed_at ELSE $4::timestamptz END,
				updated_at = GREATEST(updated_at, $4::timestamptz)
			WHERE id = $1
				AND status = 'pending'
				AND processed_at IS NULL
				AND failed_at IS NULL
			RETURNING attempt_count
		)
		INSERT INTO apple_consumption_attempts (
			request_id, attempt, attempted_at, outcome, reason, http_status, apple_error_code, request_body
		)
		SELECT $1, attempt_count, $4::timestamptz, $5, $3, $6::integer, $7::bigint, $8::jsonb FROM updated
	`, attempt.RequestID, status, attempt.Reason, attemptedAt, attempt.Outcome, httpStatus, appleErrorCode, body)
	if err != nil {
		return false, classifyError("record Apple consumption attempt", err)
	}
	return command.RowsAffected() == 1, nil
}

// DeliveredPurchaseCustomers returns the customers whose allowed observations carry one protected reference.
func (repository *transaction) DeliveredPurchaseCustomers(
	ctx context.Context,
	lookup persistence.ProviderReferenceLookup,
) ([]core.CustomerID, error) {
	if err := lookup.Validate(); err != nil {
		return nil, err
	}
	rows, err := repository.tx.Query(ctx, `
		SELECT DISTINCT observation.customer_id
		FROM provider_references AS reference
		JOIN observation_references AS observation_reference
			ON observation_reference.reference_id = reference.id
			AND observation_reference.application_id = reference.application_id
		JOIN purchase_observations AS observation
			ON observation.id = observation_reference.observation_id
			AND observation.application_id = observation_reference.application_id
		WHERE observation.project_id = $1
			AND reference.application_id = $2
			AND reference.role = $3
			AND reference.kind = $4
			AND reference.value_fingerprint = $5
			AND observation.access_status = 'allowed'
		ORDER BY observation.customer_id
		LIMIT $6
	`, lookup.ProjectID, lookup.ApplicationID, lookup.Role, lookup.Kind, lookup.Fingerprint[:],
		maximumDeliveredPurchaseCustomers)
	if err != nil {
		return nil, classifyError("list delivered purchase customers", err)
	}
	defer rows.Close()
	customers := make([]core.CustomerID, 0, 1)
	for rows.Next() {
		var customerID core.CustomerID
		if err := rows.Scan(&customerID); err != nil {
			return nil, classifyError("scan delivered purchase customer", err)
		}
		customers = append(customers, customerID)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate delivered purchase customers", err)
	}
	return customers, nil
}

// RecentAppleConsumptionRequests returns the newest requests and the HTTP result of each last attempt.
func (repository *transaction) RecentAppleConsumptionRequests(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	limit int,
) ([]persistence.AppleConsumptionRequestSummary, error) {
	if err := errors.Join(projectID.Validate(), applicationID.Validate()); err != nil {
		return nil, err
	}
	rows, err := repository.tx.Query(ctx, `
		SELECT request.id, request.application_id, request.customer_id, request.provider_product_id,
			request.product_type, request.customer_reason, request.status, request.status_reason,
			request.attempt_count, request.signed_at, request.received_at, request.deadline_at,
			request.completed_at, last_attempt.http_status, last_attempt.apple_error_code
		FROM apple_consumption_requests AS request
		LEFT JOIN LATERAL (
			SELECT attempt.http_status, attempt.apple_error_code
			FROM apple_consumption_attempts AS attempt
			WHERE attempt.request_id = request.id
			ORDER BY attempt.attempt DESC
			LIMIT 1
		) AS last_attempt ON true
		WHERE request.project_id = $1 AND request.application_id = $2
		ORDER BY request.received_at DESC, request.id DESC
		LIMIT $3
	`, projectID, applicationID, persistence.ConsumptionRequestListLimit(limit))
	if err != nil {
		return nil, classifyError("list Apple consumption requests", err)
	}
	defer rows.Close()
	summaries := make([]persistence.AppleConsumptionRequestSummary, 0)
	for rows.Next() {
		var summary persistence.AppleConsumptionRequestSummary
		var customerID, reason, statusReason *string
		var httpStatus *int
		var appleErrorCode *int64
		if err := rows.Scan(
			&summary.ID, &summary.ApplicationID, &customerID, &summary.ProviderProductID, &summary.ProductType,
			&reason, &summary.Status, &statusReason, &summary.AttemptCount, &summary.SignedAt,
			&summary.ReceivedAt, &summary.DeadlineAt, &summary.CompletedAt, &httpStatus, &appleErrorCode,
		); err != nil {
			return nil, classifyError("scan Apple consumption request", err)
		}
		summary.CustomerID = core.CustomerID(stringValue(customerID))
		summary.CustomerReason = stringValue(reason)
		summary.StatusReason = stringValue(statusReason)
		if httpStatus != nil {
			summary.LastHTTPStatus = *httpStatus
		}
		if appleErrorCode != nil {
			summary.LastAppleErrorCode = *appleErrorCode
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, classifyError("iterate Apple consumption requests", err)
	}
	return summaries, nil
}

// ensureAppleConsumptionJob creates the River job of one pending request exactly once.
// The job carries the deadline so retries are clamped without reading the row.
func (repository *transaction) ensureAppleConsumptionJob(ctx context.Context, id string) error {
	if repository.river == nil {
		return errors.New("river insert client is not initialized")
	}
	var existingJobID *int64
	var status string
	var availableAt, deadlineAt time.Time
	if err := repository.tx.QueryRow(ctx, `
		SELECT river_job_id, status, available_at, deadline_at
		FROM apple_consumption_requests WHERE id = $1 FOR UPDATE
	`, id).Scan(&existingJobID, &status, &availableAt, &deadlineAt); err != nil {
		return classifyError("lock Apple consumption request", err)
	}
	if existingJobID != nil || status != persistence.ConsumptionStatusPending {
		return nil
	}
	result, err := repository.river.InsertTx(ctx, repository.tx,
		jobs.AppleConsumptionArgs{RequestID: id, DeadlineAt: deadlineAt.UTC()},
		&river.InsertOpts{
			Queue: jobs.QueueAppleConsumption, ScheduledAt: availableAt, MaxAttempts: jobs.AppleConsumptionMaxAttempts,
		},
	)
	if err != nil {
		return fmt.Errorf("enqueue Apple consumption River job: %w", err)
	}
	command, err := repository.tx.Exec(ctx, `
		UPDATE apple_consumption_requests SET river_job_id = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2
	`, result.Job.ID, id)
	if err != nil {
		return classifyError("link Apple consumption River job", err)
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("link Apple consumption River job: %w", persistence.ErrConflict)
	}
	return nil
}

// appleConsumptionMessage loads one consumption request as a generic worker queue message.
func (repository *transaction) appleConsumptionMessage(ctx context.Context, id string) (persistence.QueueMessage, error) {
	message := persistence.QueueMessage{Queue: persistence.QueueAppleConsumption, Kind: "apple_consumption_request"}
	var customerID *string
	var ciphertext, fingerprint []byte
	var completedAt, failedAt *time.Time
	err := repository.tx.QueryRow(ctx, `
		SELECT request.id, request.project_id, request.application_id, request.customer_id,
			application.provider, request.payload_ciphertext, request.payload_fingerprint,
			request.encryption_key_id, request.received_at, request.processed_at, request.failed_at
		FROM apple_consumption_requests AS request
		JOIN applications AS application
			ON application.id = request.application_id AND application.project_id = request.project_id
		WHERE request.id = $1
	`, id).Scan(&message.ID, &message.ProjectID, &message.ApplicationID, &customerID, &message.Provider,
		&ciphertext, &fingerprint, &message.ProtectedPayload.KeyID, &message.OccurredAt, &completedAt, &failedAt)
	if err != nil {
		return persistence.QueueMessage{}, classifyError("load Apple consumption message", err)
	}
	value, err := protectedValue(ciphertext, fingerprint, message.ProtectedPayload.KeyID)
	if err != nil {
		return persistence.QueueMessage{}, fmt.Errorf("validate Apple consumption protected payload: %w", err)
	}
	message.ProtectedPayload = value
	message.CustomerID = core.CustomerID(stringValue(customerID))
	message.Completed = completedAt != nil
	message.Failed = failedAt != nil
	return message, nil
}

// failAppleConsumption records the worker's terminal failure and closes the request as failed.
func (repository *transaction) failAppleConsumption(ctx context.Context, failure persistence.QueueFailure) error {
	return repository.changeQueueOutcome(ctx, `
		UPDATE apple_consumption_requests
		SET failed_at = COALESCE(failed_at, $1),
			last_error_code = COALESCE(last_error_code, $2),
			status = 'failed',
			status_reason = CASE WHEN status = 'failed' THEN COALESCE(status_reason, $2) ELSE $2 END,
			completed_at = COALESCE(completed_at, $1),
			updated_at = GREATEST(updated_at, $1)
		WHERE id = $3 AND processed_at IS NULL AND status IN ('pending', 'failed')
	`, normalizeTime(failure.FailedAt), failure.ErrorCode, failure.ID)
}

// sameConsumptionSettings reports whether stored settings already equal one write.
func sameConsumptionSettings(settings persistence.AppleConsumptionSettings, write persistence.AppleConsumptionSettingsWrite) bool {
	return settings.Enabled == write.Enabled && settings.RefundPreference == write.RefundPreference &&
		settings.SampleContentProvided == write.SampleContentProvided &&
		settings.UpdatedByKeyID == write.ActorKeyID
}

// stringValue returns the referenced text or an empty string for SQL NULL.
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
