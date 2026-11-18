package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/apple"
)

const (
	// appleConsumptionProtectionPurpose authenticates protected consumption request payloads.
	appleConsumptionProtectionPurpose = "apple_consumption_request"
	// appleConsumptionIdentityPurpose scopes the keyed fingerprint that names one consumption request.
	appleConsumptionIdentityPurpose = "apple_consumption_notification"
	// appleConsumptionInitialDelay lets the same notification's purchase refresh commit before the first answer.
	appleConsumptionInitialDelay = time.Minute

	// consumptionReasonAccepted records that Apple accepted the consumption information.
	consumptionReasonAccepted = "accepted"
	// consumptionReasonDisabled records an application whose operator has not enabled consumption answers.
	consumptionReasonDisabled = "disabled"
	// consumptionReasonNoConsent records a customer without a recorded yes.
	consumptionReasonNoConsent = "no_consent"
	// consumptionReasonConsentWithdrawn records a customer whose recorded decision is no consent.
	consumptionReasonConsentWithdrawn = "consent_withdrawn"
	// consumptionReasonFamilyShared records a Family Sharing transaction, which Apple does not accept.
	consumptionReasonFamilyShared = "family_shared"
	// consumptionReasonUnsupportedOwnership records an ownership type other than a direct purchase.
	consumptionReasonUnsupportedOwnership = "unsupported_ownership"
	// consumptionReasonNoDeliveryEvidence records a purchase IAPStack never verified with allowed access.
	consumptionReasonNoDeliveryEvidence = "no_delivery_evidence"
	// consumptionReasonDeadlinePassed records a run at or after the twelve-hour deadline.
	consumptionReasonDeadlinePassed = "deadline_passed"
	// consumptionReasonAppleRejected records an HTTP 400 answer.
	consumptionReasonAppleRejected = "apple_rejected"
	// consumptionReasonTransactionNotFound records an HTTP 404 answer.
	consumptionReasonTransactionNotFound = "transaction_not_found"
	// consumptionReasonUnauthorized records rejected or missing App Store Server API credentials.
	consumptionReasonUnauthorized = "provider_unauthorized"
	// consumptionReasonRateLimited records an HTTP 429 answer.
	consumptionReasonRateLimited = "provider_rate_limited"
	// consumptionReasonTemporary records a network failure or an HTTP 5xx answer.
	consumptionReasonTemporary = "provider_temporary"
	// consumptionReasonPermanent records any other provider failure.
	consumptionReasonPermanent = "provider_permanent"
	// consumptionReasonInvalidRequest records a body the builder refused for a reason other than consent or delivery.
	consumptionReasonInvalidRequest = "invalid_request"
)

// appleConsumptionSender sends one consumption body to the App Store Server API.
type appleConsumptionSender interface {
	// SendConsumptionInformation calls Send Consumption Information for one transaction.
	SendConsumptionInformation(context.Context, core.Application, string, apple.ConsumptionRequest) (apple.ConsumptionResponse, error)
}

// consumptionFailure is a classified handler error the worker stores as a safe code.
type consumptionFailure struct {
	code      string
	retryable bool
	cause     error
}

// consumptionDecision is the outcome of the consent and evidence checks before any provider call.
type consumptionDecision struct {
	skipReason string
	input      apple.ConsumptionInput
}

// SetAppleConsumptionSender enables the worker to answer App Store consumption requests.
func (service *Service) SetAppleConsumptionSender(sender appleConsumptionSender) {
	if service == nil {
		return
	}
	service.consumption = sender
}

// HandleAppleConsumption runs one attempt for a durable consumption request and records its outcome.
// Every decision, including a skip or an expired deadline, is one audited attempt.
func (service *Service) HandleAppleConsumption(ctx context.Context, message persistence.QueueMessage) error {
	if message.Queue != persistence.QueueAppleConsumption {
		return errors.New("unsupported consumption message")
	}
	var record persistence.AppleConsumptionRequestRecord
	if err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		record, loadErr = repository.AppleConsumptionRequest(ctx, message.ID)
		return loadErr
	}); err != nil {
		return err
	}
	if record.Status != persistence.ConsumptionStatusPending {
		return nil
	}
	now := service.now()
	if !now.Before(record.DeadlineAt) {
		return service.finishConsumption(ctx, record, persistence.AppleConsumptionAttempt{
			Outcome: persistence.ConsumptionAttemptExpired, Reason: consumptionReasonDeadlinePassed,
			Status: persistence.ConsumptionStatusExpired,
		})
	}
	application, settings, err := service.consumptionSettings(ctx, record)
	if err != nil {
		return err
	}
	if !settings.Enabled || application.Store.Provider != core.ProviderAppleAppStore {
		return service.finishConsumption(ctx, record, persistence.AppleConsumptionAttempt{
			Outcome: persistence.ConsumptionAttemptSkipped, Reason: consumptionReasonDisabled,
			Status: persistence.ConsumptionStatusSkipped,
		})
	}
	if service.consumption == nil {
		return &consumptionFailure{code: "consumption_sender_unavailable"}
	}
	payload, err := service.openConsumptionPayload(ctx, record)
	if err != nil {
		return err
	}
	decision, err := service.decideConsumption(ctx, record, payload, settings)
	if err != nil {
		return err
	}
	if decision.skipReason != "" {
		return service.finishConsumption(ctx, record, persistence.AppleConsumptionAttempt{
			Outcome: persistence.ConsumptionAttemptSkipped, Reason: decision.skipReason,
			Status: persistence.ConsumptionStatusSkipped,
		})
	}
	body, err := apple.BuildConsumptionRequest(decision.input)
	if err != nil {
		return service.finishConsumption(ctx, record, persistence.AppleConsumptionAttempt{
			Outcome: persistence.ConsumptionAttemptSkipped, Reason: consumptionBuildReason(err),
			Status: persistence.ConsumptionStatusSkipped,
		})
	}
	encodedBody, err := json.Marshal(body)
	if err != nil {
		return err
	}
	response, sendErr := service.consumption.SendConsumptionInformation(ctx, application, payload.TransactionID, body)
	attempt := persistence.AppleConsumptionAttempt{
		HTTPStatus: response.HTTPStatus, AppleErrorCode: response.AppleErrorCode, RequestBody: encodedBody,
	}
	if sendErr == nil {
		attempt.Outcome, attempt.Reason, attempt.Status =
			persistence.ConsumptionAttemptSent, consumptionReasonAccepted, persistence.ConsumptionStatusSent
		return service.finishConsumption(ctx, record, attempt)
	}
	if errors.Is(sendErr, context.Canceled) && ctx.Err() != nil {
		return sendErr
	}
	reason, retryable := consumptionSendReason(sendErr, response.HTTPStatus)
	attempt.Reason = reason
	if retryable {
		attempt.Outcome = persistence.ConsumptionAttemptTransientError
	} else {
		attempt.Outcome, attempt.Status = persistence.ConsumptionAttemptPermanentError, persistence.ConsumptionStatusFailed
	}
	recorded, err := service.recordConsumption(ctx, record, attempt)
	if err != nil || !recorded {
		return err
	}
	return &consumptionFailure{code: reason, retryable: retryable, cause: sendErr}
}

// Error returns the safe consumption failure code.
func (failure *consumptionFailure) Error() string {
	return "apple consumption " + failure.code
}

// Unwrap exposes the underlying provider error to explicit inspection.
func (failure *consumptionFailure) Unwrap() error {
	return failure.cause
}

// Retryable reports whether the worker should run the request again.
func (failure *consumptionFailure) Retryable() bool {
	return failure.retryable
}

// CodeValue returns the bounded code stored on the request when attempts end.
func (failure *consumptionFailure) CodeValue() string {
	return failure.code
}

// scheduleAppleConsumption stores one durable consumption request for a CONSUMPTION_REQUEST notification.
// The request id is a keyed fingerprint of notificationUUID, so Apple redeliveries map to one request.
func (service *Service) scheduleAppleConsumption(
	ctx context.Context,
	message persistence.QueueMessage,
	notification apple.NotificationEnvelope,
) error {
	signal := notification.Consumption
	if signal == nil {
		return nil
	}
	identity := []byte(notification.NotificationUUID)
	protectedIdentity, err := protection.Protect(ctx, service.protection, protection.Scope{
		ProjectID: message.ProjectID, ApplicationID: message.ApplicationID, Purpose: appleConsumptionIdentityPurpose,
	}, identity)
	if err != nil {
		return fmt.Errorf("protect App Store consumption identity: %w", err)
	}
	zero(protectedIdentity.Ciphertext)
	payload, err := json.Marshal(jobs.AppleConsumptionPayload{
		TransactionID: signal.TransactionID, ExternalCustomerID: signal.ExternalCustomerID, Ownership: signal.Ownership,
	})
	if err != nil {
		return err
	}
	defer zero(payload)
	protected, err := protection.Protect(ctx, service.protection, protection.Scope{
		ProjectID: message.ProjectID, ApplicationID: message.ApplicationID, Purpose: appleConsumptionProtectionPurpose,
	}, payload)
	if err != nil {
		return fmt.Errorf("protect App Store consumption request: %w", err)
	}
	now := service.now()
	receivedAt := now
	if !message.OccurredAt.IsZero() && message.OccurredAt.Before(now) {
		receivedAt = message.OccurredAt.UTC()
	}
	deadline := notification.SignedAt.UTC().Add(jobs.AppleConsumptionResponseWindow)
	availableAt := now.Add(appleConsumptionInitialDelay)
	if !availableAt.Before(deadline) {
		availableAt = now
	}
	request := persistence.AppleConsumptionRequest{
		ID:                deterministicID("consumption", protectedIdentity.Fingerprint),
		ProjectID:         message.ProjectID,
		ApplicationID:     message.ApplicationID,
		ProviderProductID: signal.ProviderProductID, ProductType: signal.ProductType,
		CustomerReason: signal.CustomerReason, Payload: protected,
		SignedAt: notification.SignedAt.UTC(), ReceivedAt: receivedAt, DeadlineAt: deadline, AvailableAt: availableAt,
	}
	return service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		// The customer column is only a display link. The worker resolves the customer again before
		// checking consent, so an unknown or inconsistent identity is stored as empty here.
		customer, lookupErr := repository.CustomerByExternalID(ctx, message.ProjectID, signal.ExternalCustomerID)
		switch {
		case lookupErr == nil:
			request.CustomerID = customer.ID
		case errors.Is(lookupErr, persistence.ErrNotFound) || errors.Is(lookupErr, persistence.ErrConflict):
			request.CustomerID = ""
		default:
			return lookupErr
		}
		_, saveErr := repository.SaveAppleConsumptionRequest(ctx, request)
		return saveErr
	})
}

// openConsumptionPayload authenticates and decodes the protected identifiers of one request.
func (service *Service) openConsumptionPayload(
	ctx context.Context,
	record persistence.AppleConsumptionRequestRecord,
) (jobs.AppleConsumptionPayload, error) {
	plaintext, err := service.open(ctx, persistence.QueueMessage{
		ProjectID: record.ProjectID, ApplicationID: record.ApplicationID, ProtectedPayload: record.Payload,
	}, appleConsumptionProtectionPurpose)
	if err != nil {
		return jobs.AppleConsumptionPayload{}, &consumptionFailure{code: "consumption_payload_unavailable", cause: err}
	}
	defer zero(plaintext)
	var payload jobs.AppleConsumptionPayload
	if err := decodeStrict(plaintext, &payload); err != nil || payload.TransactionID == "" {
		return jobs.AppleConsumptionPayload{}, &consumptionFailure{code: "consumption_payload_invalid", cause: err}
	}
	return payload, nil
}

// consumptionSettings loads the application and its settings. Never-saved settings are the disabled defaults.
// The handler checks them before opening the protected payload, so a disabled application never reads it.
func (service *Service) consumptionSettings(
	ctx context.Context,
	record persistence.AppleConsumptionRequestRecord,
) (core.Application, persistence.AppleConsumptionSettings, error) {
	var application core.Application
	var settings persistence.AppleConsumptionSettings
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		application, loadErr = repository.Application(ctx, record.ProjectID, record.ApplicationID)
		if loadErr != nil {
			return loadErr
		}
		settings, loadErr = repository.AppleConsumptionSettings(ctx, record.ProjectID, record.ApplicationID)
		if errors.Is(loadErr, persistence.ErrNotFound) {
			settings, loadErr = persistence.DefaultAppleConsumptionSettings(record.ProjectID, record.ApplicationID), nil
		}
		return loadErr
	})
	return application, settings, err
}

// decideConsumption applies ownership, consent, and delivery evidence in that order.
// It reads only IAPStack's own records and never calls Apple.
func (service *Service) decideConsumption(
	ctx context.Context,
	record persistence.AppleConsumptionRequestRecord,
	payload jobs.AppleConsumptionPayload,
	settings persistence.AppleConsumptionSettings,
) (consumptionDecision, error) {
	var decision consumptionDecision
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		decision = consumptionDecision{}
		switch payload.Ownership {
		case apple.ConsumptionOwnershipPurchased:
		case apple.ConsumptionOwnershipFamilyShared:
			decision.skipReason = consumptionReasonFamilyShared
			return nil
		default:
			decision.skipReason = consumptionReasonUnsupportedOwnership
			return nil
		}
		customerID, reason, consentErr := consumptionConsent(ctx, repository, record, payload)
		if consentErr != nil {
			return consentErr
		}
		if reason != "" {
			decision.skipReason = reason
			return nil
		}
		delivered, deliveryErr := service.consumptionDelivered(ctx, repository, record, payload, customerID)
		if deliveryErr != nil {
			return deliveryErr
		}
		decision.input = apple.ConsumptionInput{
			ProductType: record.ProductType, CustomerConsented: true, Delivered: delivered,
			RefundPreference: settings.RefundPreference, SampleContentProvided: settings.SampleContentProvided,
		}
		return nil
	})
	return decision, err
}

// consumptionConsent resolves the customer and requires their recorded yes.
// Consent is opt-in only: no record, an unknown customer, or a recorded no returns a skip reason.
func consumptionConsent(
	ctx context.Context,
	repository persistence.OperationsTransaction,
	record persistence.AppleConsumptionRequestRecord,
	payload jobs.AppleConsumptionPayload,
) (core.CustomerID, string, error) {
	customer, err := repository.CustomerByExternalID(ctx, record.ProjectID, payload.ExternalCustomerID)
	if errors.Is(err, persistence.ErrNotFound) || errors.Is(err, persistence.ErrConflict) {
		return "", consumptionReasonNoConsent, nil
	}
	if err != nil {
		return "", "", err
	}
	consent, err := repository.AppleConsumptionConsent(ctx, record.ProjectID, record.ApplicationID, customer.ID)
	switch {
	case errors.Is(err, persistence.ErrNotFound):
		return customer.ID, consumptionReasonNoConsent, nil
	case err != nil:
		return "", "", err
	case !consent.Consented:
		return customer.ID, consumptionReasonConsentWithdrawn, nil
	default:
		return customer.ID, "", nil
	}
}

// consumptionDelivered reports whether IAPStack verified this transaction with allowed access for the customer.
// That stored observation is the only delivery evidence the server has. A merged source customer counts.
func (service *Service) consumptionDelivered(
	ctx context.Context,
	repository persistence.OperationsTransaction,
	record persistence.AppleConsumptionRequestRecord,
	payload jobs.AppleConsumptionPayload,
	customerID core.CustomerID,
) (bool, error) {
	if customerID == "" {
		return false, nil
	}
	transactionID := []byte(payload.TransactionID)
	defer zero(transactionID)
	protected, err := protection.Protect(ctx, service.protection, protection.Scope{
		ProjectID: record.ProjectID, ApplicationID: record.ApplicationID,
		Purpose: providerReferencePurpose + ":" + string(core.ReferenceTransaction) + ":transaction_id",
	}, transactionID)
	if err != nil {
		return false, fmt.Errorf("protect App Store consumption lookup: %w", err)
	}
	zero(protected.Ciphertext)
	owners, err := repository.DeliveredPurchaseCustomers(ctx, persistence.ProviderReferenceLookup{
		ProjectID: record.ProjectID, ApplicationID: record.ApplicationID,
		Role: core.ReferenceTransaction, Kind: "transaction_id", Fingerprint: protected.Fingerprint,
	})
	if err != nil {
		return false, err
	}
	for _, owner := range owners {
		if owner == customerID {
			return true, nil
		}
		inClosure, closureErr := repository.CustomerInClosure(ctx, record.ProjectID, customerID, owner)
		if closureErr != nil {
			return false, closureErr
		}
		if inClosure {
			return true, nil
		}
	}
	return false, nil
}

// finishConsumption records one attempt that ends the handler run successfully.
func (service *Service) finishConsumption(
	ctx context.Context,
	record persistence.AppleConsumptionRequestRecord,
	attempt persistence.AppleConsumptionAttempt,
) error {
	_, err := service.recordConsumption(ctx, record, attempt)
	return err
}

// recordConsumption records one attempt and logs it without identifiers.
// False means another run already closed the request, so this attempt changed nothing.
func (service *Service) recordConsumption(
	ctx context.Context,
	record persistence.AppleConsumptionRequestRecord,
	attempt persistence.AppleConsumptionAttempt,
) (bool, error) {
	attempt.RequestID = record.ID
	attempt.AttemptedAt = service.now()
	var recorded bool
	if err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var recordErr error
		recorded, recordErr = repository.RecordAppleConsumptionAttempt(ctx, attempt)
		return recordErr
	}); err != nil {
		return false, err
	}
	logger := service.logger
	if logger == nil {
		logger = slog.Default()
	}
	level := slog.LevelInfo
	if attempt.Outcome == persistence.ConsumptionAttemptTransientError || attempt.Outcome == persistence.ConsumptionAttemptPermanentError {
		level = slog.LevelWarn
	}
	logger.Log(ctx, level, "apple consumption attempt",
		slog.String("consumption_request_id", record.ID),
		slog.String("application_id", string(record.ApplicationID)),
		slog.String("outcome", attempt.Outcome),
		slog.String("reason", attempt.Reason),
		slog.Int("http_status", attempt.HTTPStatus),
		slog.Bool("recorded", recorded),
	)
	return recorded, nil
}

// consumptionBuildReason maps a builder refusal to its stored skip reason.
func consumptionBuildReason(err error) string {
	switch {
	case errors.Is(err, apple.ErrConsumptionConsentRequired):
		return consumptionReasonNoConsent
	case errors.Is(err, apple.ErrConsumptionDeliveryUnknown):
		return consumptionReasonNoDeliveryEvidence
	default:
		return consumptionReasonInvalidRequest
	}
}

// consumptionSendReason maps one provider failure to a stored reason and whether it is retried.
func consumptionSendReason(err error, httpStatus int) (string, bool) {
	var failure *stores.Failure
	if !errors.As(err, &failure) {
		return consumptionReasonTemporary, true
	}
	switch failure.Kind {
	case stores.FailureRateLimited:
		return consumptionReasonRateLimited, true
	case stores.FailureTemporary:
		return consumptionReasonTemporary, true
	case stores.FailureUnauthorized:
		return consumptionReasonUnauthorized, false
	case stores.FailureNotFound:
		return consumptionReasonTransactionNotFound, false
	}
	if httpStatus == http.StatusBadRequest {
		return consumptionReasonAppleRejected, false
	}
	return consumptionReasonPermanent, false
}
