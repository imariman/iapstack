package processing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

const (
	// reconciliationScheduleRetryPurpose authenticates protected verification payloads awaiting scheduling.
	reconciliationScheduleRetryPurpose = "reconciliation_schedule_retry"

	// reconciliationRetryBatch bounds one worker pass over failed subscription schedules.
	reconciliationRetryBatch = 32
	// reconciliationRetryMaxAttempts marks a schedule dead after this many failed promotions.
	// A row that can never succeed must leave the due batch instead of occupying it every minute.
	reconciliationRetryMaxAttempts = 12
	// reconciliationRetryBaseDelay is the wait after the first failed promotion.
	reconciliationRetryBaseDelay = time.Minute
	// reconciliationRetryMaxDelay caps exponential backoff for a schedule that keeps failing.
	reconciliationRetryMaxDelay = time.Hour
)

// SetLogger records schedule-retry outcomes on the worker logger.
// A nil logger falls back to the process default when a row is marked dead.
func (service *Service) SetLogger(logger *slog.Logger) {
	if service == nil {
		return
	}
	service.logger = logger
}

// SetClock overrides the time source used for reconciliation scheduling and retry promotion.
func (service *Service) SetClock(clock func() time.Time) {
	if service == nil {
		return
	}
	if clock == nil {
		service.clock = time.Now
		return
	}
	service.clock = clock
}

// ScheduleReconciliation stores the next daily refresh job or records a worker retry when that insert fails.
func (service *Service) ScheduleReconciliation(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	customerID core.CustomerID,
	verificationPayload jobs.VerificationPayload,
) error {
	if service == nil || service.store == nil || service.protection == nil {
		return errors.New("processing service is not initialized")
	}
	scheduleErr := service.schedule(ctx, projectID, applicationID, customerID, verificationPayload)
	if scheduleErr == nil {
		return nil
	}
	retryErr := service.saveReconciliationScheduleRetry(
		ctx, projectID, applicationID, customerID, verificationPayload,
	)
	return errors.Join(scheduleErr, retryErr)
}

// RetryReconciliationSchedules inserts reconciliation jobs for schedules the HTTP request could not commit.
func (service *Service) RetryReconciliationSchedules(ctx context.Context) error {
	if service == nil || service.store == nil || service.protection == nil {
		return errors.New("processing service is not initialized")
	}
	now := service.now()
	for range reconciliationRetryBatch {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		retry, hasRow, promoteErr := service.promoteNextReconciliationScheduleRetry(ctx, now)
		if !hasRow && promoteErr == nil {
			return nil
		}
		if promoteErr == nil {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !hasRow {
			return promoteErr
		}
		// The promotion error can mention storage internals. Persist the backoff so one poison
		// row leaves the due batch, and return only a failure to record that backoff.
		if recordErr := service.recordReconciliationScheduleRetryFailure(ctx, retry, now); recordErr != nil {
			return recordErr
		}
	}
	return nil
}

// saveReconciliationScheduleRetry protects and persists one verification payload for worker promotion.
func (service *Service) saveReconciliationScheduleRetry(
	ctx context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	customerID core.CustomerID,
	verificationPayload jobs.VerificationPayload,
) error {
	payload, err := json.Marshal(verificationPayload)
	if err != nil {
		return err
	}
	defer zero(payload)
	protected, err := protection.Protect(ctx, service.protection, protection.Scope{
		ProjectID: projectID, ApplicationID: applicationID, Purpose: reconciliationScheduleRetryPurpose,
	}, payload)
	if err != nil {
		return fmt.Errorf("protect reconciliation schedule retry: %w", err)
	}
	id, err := newReconciliationScheduleRetryID()
	if err != nil {
		return err
	}
	retry := persistence.ReconciliationScheduleRetry{
		ID:            id,
		ProjectID:     projectID,
		ApplicationID: applicationID,
		CustomerID:    customerID,
		Payload:       protected,
	}
	return service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		return repository.SaveReconciliationScheduleRetry(ctx, retry)
	})
}

// promoteNextReconciliationScheduleRetry locks one due retry, claims it, and inserts its reconciliation job.
func (service *Service) promoteNextReconciliationScheduleRetry(
	ctx context.Context,
	now time.Time,
) (persistence.ReconciliationScheduleRetry, bool, error) {
	var selected persistence.ReconciliationScheduleRetry
	var hasRow bool
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		pending, listErr := repository.ListPendingReconciliationScheduleRetries(ctx, now, 1)
		if listErr != nil || len(pending) == 0 {
			return listErr
		}
		selected = pending[0]
		hasRow = true
		verificationPayload, openErr := service.openScheduleRetry(ctx, selected)
		if openErr != nil {
			return openErr
		}
		defer zero(verificationPayload.Evidence)
		job, jobErr := service.reconciliationJob(
			ctx,
			selected.ProjectID,
			selected.ApplicationID,
			selected.CustomerID,
			verificationPayload,
			nextReconciliationTime(now),
		)
		if jobErr != nil {
			return jobErr
		}
		claimed, claimErr := repository.CompleteReconciliationScheduleRetry(ctx, selected.ID, now)
		if claimErr != nil || !claimed {
			return claimErr
		}
		_, saveErr := repository.SaveReconciliationJob(ctx, job)
		return saveErr
	})
	return selected, hasRow, err
}

// openScheduleRetry decrypts and decodes one stored verification payload.
func (service *Service) openScheduleRetry(
	ctx context.Context,
	retry persistence.ReconciliationScheduleRetry,
) (jobs.VerificationPayload, error) {
	request, err := protection.NewOpenRequest(protection.Scope{
		ProjectID: retry.ProjectID, ApplicationID: retry.ApplicationID, Purpose: reconciliationScheduleRetryPurpose,
	}, retry.Payload)
	if err != nil {
		return jobs.VerificationPayload{}, err
	}
	plaintext, err := service.protection.Open(ctx, request)
	if err != nil {
		return jobs.VerificationPayload{}, fmt.Errorf("open reconciliation schedule retry: %w", err)
	}
	defer zero(plaintext)
	var payload jobs.VerificationPayload
	if err := decodeStrict(plaintext, &payload); err != nil {
		return jobs.VerificationPayload{}, fmt.Errorf("decode reconciliation schedule retry: %w", err)
	}
	return payload, nil
}

// now returns the processing clock in UTC.
func (service *Service) now() time.Time {
	if service != nil && service.clock != nil {
		return service.clock().UTC()
	}
	return time.Now().UTC()
}

// recordReconciliationScheduleRetryFailure applies exponential backoff or marks the row dead.
func (service *Service) recordReconciliationScheduleRetryFailure(
	ctx context.Context,
	retry persistence.ReconciliationScheduleRetry,
	now time.Time,
) error {
	attempt := retry.Attempts + 1
	if attempt < 1 {
		attempt = 1
	}
	dead := attempt >= reconciliationRetryMaxAttempts
	nextAttemptAt := now.Add(reconciliationRetryBackoff(attempt))
	if dead {
		nextAttemptAt = now
	}
	var updated bool
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var recordErr error
		updated, recordErr = repository.RecordReconciliationScheduleRetryFailure(ctx, persistence.ReconciliationScheduleRetryFailure{
			ID:            retry.ID,
			AttemptedAt:   now,
			NextAttemptAt: nextAttemptAt,
			Dead:          dead,
		})
		return recordErr
	})
	if err != nil {
		return err
	}
	if updated && dead {
		service.logDeadScheduleRetry(ctx, retry, attempt)
	}
	return nil
}

// reconciliationRetryBackoff doubles the wait on each failed promotion until the cap.
func reconciliationRetryBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return reconciliationRetryBaseDelay
	}
	delay := reconciliationRetryBaseDelay
	for current := 1; current < attempt; current++ {
		if delay >= reconciliationRetryMaxDelay/2 {
			return reconciliationRetryMaxDelay
		}
		delay *= 2
	}
	if delay > reconciliationRetryMaxDelay {
		return reconciliationRetryMaxDelay
	}
	return delay
}

// logDeadScheduleRetry records one exhausted schedule without purchase evidence or the storage error.
func (service *Service) logDeadScheduleRetry(
	ctx context.Context,
	retry persistence.ReconciliationScheduleRetry,
	attempts int,
) {
	logger := service.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Log(ctx, slog.LevelError, "reconciliation schedule retry exhausted",
		slog.String("error_code", "reconciliation_schedule_retry_dead"),
		slog.String("project_id", string(retry.ProjectID)),
		slog.String("application_id", string(retry.ApplicationID)),
		slog.String("customer_id", string(retry.CustomerID)),
		slog.Int("attempts", attempts),
	)
}

// newReconciliationScheduleRetryID generates one unique non-secret identifier per retry row.
func newReconciliationScheduleRetryID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate reconciliation schedule retry ID: %w", err)
	}
	return "reconcile-retry-" + hex.EncodeToString(raw[:]), nil
}
