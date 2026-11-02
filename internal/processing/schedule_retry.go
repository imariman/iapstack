package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

const (
	// purchaseEvidencePurpose matches verification's protected client-submission scope.
	purchaseEvidencePurpose = "purchase_evidence"
	// reconciliationRetryBatch bounds one worker pass over failed subscription schedules.
	reconciliationRetryBatch = 32
)

// RetryReconciliationSchedules inserts reconciliation jobs for schedules the HTTP request could not commit.
func (service *Service) RetryReconciliationSchedules(ctx context.Context) error {
	if service == nil || service.store == nil || service.protection == nil {
		return errors.New("processing service is not initialized")
	}
	now := time.Now
	if service.clock != nil {
		now = service.clock
	}
	var pending []persistence.ReconciliationScheduleRetry
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var listErr error
		pending, listErr = repository.ListPendingReconciliationScheduleRetries(ctx, reconciliationRetryBatch)
		return listErr
	})
	if err != nil {
		return err
	}
	var first error
	for _, retry := range pending {
		if promoteErr := service.promoteReconciliationScheduleRetry(ctx, retry, now().UTC()); promoteErr != nil && first == nil {
			first = promoteErr
		}
	}
	return first
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
