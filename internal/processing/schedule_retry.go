package processing

import (
	"context"
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

// RetryReconciliationSchedules inserts reconciliation jobs for schedules the HTTP request could not commit.
func (service *Service) RetryReconciliationSchedules(ctx context.Context) error {
	if service == nil || service.store == nil || service.protection == nil {
		return errors.New("processing service is not initialized")
	}
	now := service.now()
	var pending []persistence.ReconciliationScheduleRetry
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var listErr error
		pending, listErr = repository.ListPendingReconciliationScheduleRetries(ctx, now, reconciliationRetryBatch)
		return listErr
	})
	if err != nil {
		return err
	}
	var first error
	for _, retry := range pending {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		promoteErr := service.promoteReconciliationScheduleRetry(ctx, retry, now)
		if promoteErr == nil {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// The promotion error can mention storage internals. Persist the backoff and
		// return only a failure to record that backoff. One poison row must not stop the batch.
		if recordErr := service.recordReconciliationScheduleRetryFailure(ctx, retry, now); recordErr != nil && first == nil {
			first = recordErr
		}
	}
	return first
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

// promoteReconciliationScheduleRetry inserts one idempotent reconciliation job and marks its retry scheduled.
func (service *Service) promoteReconciliationScheduleRetry(
	ctx context.Context,
	retry persistence.ReconciliationScheduleRetry,
	now time.Time,
) error {
	payload, err := service.reconciliationRetryPayload(ctx, retry)
	if err != nil {
		return err
	}
	return service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		if _, saveErr := repository.SaveReconciliationJob(ctx, persistence.ReconciliationJob{
			ID:            deterministicID("reconcile", payload.Fingerprint),
			ProjectID:     retry.ProjectID,
			ApplicationID: retry.ApplicationID,
			CustomerID:    retry.CustomerID,
			Payload:       payload,
			AvailableAt:   retry.AvailableAt,
		}); saveErr != nil {
			return saveErr
		}
		return repository.CompleteReconciliationScheduleRetry(ctx, retry.ID, now)
	})
}

// reconciliationRetryPayload returns the protected reconciliation job body for one retry.
func (service *Service) reconciliationRetryPayload(
	ctx context.Context,
	retry persistence.ReconciliationScheduleRetry,
) (protection.Value, error) {
	switch retry.Kind {
	case persistence.ReconciliationRetryReconciliationRequest:
		return retry.Payload, nil
	case persistence.ReconciliationRetryPurchaseEvidence:
		return service.protectReconciliationFromEvidence(ctx, retry)
	default:
		return protection.Value{}, fmt.Errorf("unsupported reconciliation schedule retry kind %q", retry.Kind)
	}
}

// protectReconciliationFromEvidence rebuilds a daily refresh from purchase evidence already stored by verify.
func (service *Service) protectReconciliationFromEvidence(
	ctx context.Context,
	retry persistence.ReconciliationScheduleRetry,
) (protection.Value, error) {
	var metadata persistence.ReconciliationScheduleMetadata
	if err := json.Unmarshal(retry.Metadata, &metadata); err != nil {
		return protection.Value{}, fmt.Errorf("decode reconciliation schedule retry: %w", err)
	}
	if err := metadata.Validate(); err != nil {
		return protection.Value{}, err
	}
	request, err := protection.NewOpenRequest(protection.Scope{
		ProjectID: retry.ProjectID, ApplicationID: retry.ApplicationID, Purpose: purchaseEvidencePurpose,
	}, retry.Payload)
	if err != nil {
		return protection.Value{}, err
	}
	evidence, err := service.protection.Open(ctx, request)
	if err != nil {
		return protection.Value{}, fmt.Errorf("open purchase evidence for reconciliation schedule: %w", err)
	}
	defer zero(evidence)
	bindings := make([]jobs.Binding, 0, len(metadata.CustomerBindings))
	for _, binding := range metadata.CustomerBindings {
		bindings = append(bindings, jobs.Binding{Kind: binding.Kind, Value: binding.Value})
	}
	body, err := json.Marshal(jobs.ReconciliationPayload{
		Verification: jobs.VerificationPayload{
			ExternalCustomerID: metadata.ExternalCustomerID,
			ClaimedProducts:    append([]core.ProviderProductID(nil), metadata.ClaimedProducts...),
			CustomerBindings:   bindings,
			Evidence:           append(json.RawMessage(nil), evidence...),
		},
		ScheduledFor: retry.AvailableAt.UTC(),
	})
	if err != nil {
		return protection.Value{}, err
	}
	defer zero(body)
	protected, err := protection.Protect(ctx, service.protection, protection.Scope{
		ProjectID: retry.ProjectID, ApplicationID: retry.ApplicationID, Purpose: reconciliationProtectionPurpose,
	}, body)
	if err != nil {
		return protection.Value{}, fmt.Errorf("protect reconciliation schedule retry: %w", err)
	}
	return protected, nil
}
