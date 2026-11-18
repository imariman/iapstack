package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/protection"
)

const (
	// QueueAppleConsumption identifies protected App Store consumption response work.
	QueueAppleConsumption QueueName = "apple_consumption"

	// ConsumptionRefundPreferenceNone omits refundPreference from the request.
	ConsumptionRefundPreferenceNone = "none"
	// ConsumptionRefundPreferenceDecline asks Apple to decline the refund.
	ConsumptionRefundPreferenceDecline = "decline"
	// ConsumptionRefundPreferenceGrantFull asks Apple to grant the refund in full.
	ConsumptionRefundPreferenceGrantFull = "grant_full"
	// ConsumptionRefundPreferenceGrantProrated asks Apple to prorate auto-renewable subscription refunds.
	ConsumptionRefundPreferenceGrantProrated = "grant_prorated"

	// ConsumptionStatusPending means the request is waiting for its next attempt.
	ConsumptionStatusPending = "pending"
	// ConsumptionStatusSent means Apple accepted the consumption information.
	ConsumptionStatusSent = "sent"
	// ConsumptionStatusSkipped means IAPStack decided not to answer and recorded why.
	ConsumptionStatusSkipped = "skipped"
	// ConsumptionStatusExpired means the twelve-hour window closed before an answer was accepted.
	ConsumptionStatusExpired = "expired"
	// ConsumptionStatusFailed means Apple rejected the request or attempts ran out.
	ConsumptionStatusFailed = "failed"

	// ConsumptionAttemptSent records an accepted request.
	ConsumptionAttemptSent = "sent"
	// ConsumptionAttemptSkipped records a decision not to send.
	ConsumptionAttemptSkipped = "skipped"
	// ConsumptionAttemptExpired records a run after the deadline.
	ConsumptionAttemptExpired = "expired"
	// ConsumptionAttemptTransientError records a failure that is retried.
	ConsumptionAttemptTransientError = "transient_error"
	// ConsumptionAttemptPermanentError records a failure that is not retried.
	ConsumptionAttemptPermanentError = "permanent_error"

	// maximumConsumptionRequestList bounds one administrative consumption request page.
	maximumConsumptionRequestList = 100
)

var (
	// consumptionReasonPattern matches the bounded reason codes stored with requests and attempts.
	consumptionReasonPattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	// consumptionProductTypes is the closed normalized App Store product type set.
	consumptionProductTypes = map[string]struct{}{
		"consumable": {}, "non_consumable": {}, "auto_renewable_subscription": {}, "non_renewing_subscription": {},
	}
	// consumptionCustomerReasons is the closed normalized consumptionRequestReason set.
	consumptionCustomerReasons = map[string]struct{}{
		"unintended_purchase": {}, "fulfillment_issue": {}, "unsatisfied_with_purchase": {}, "legal": {}, "other": {},
	}
)

// AppleConsumptionRepository stores consumption settings, consent, requests, and attempts.
type AppleConsumptionRepository interface {
	// AppleConsumptionSettings returns one application's settings or ErrNotFound when they were never saved.
	AppleConsumptionSettings(context.Context, core.ProjectID, core.ApplicationID) (AppleConsumptionSettings, error)
	// PutAppleConsumptionSettings creates or updates settings with optimistic revision control.
	PutAppleConsumptionSettings(context.Context, AppleConsumptionSettingsWrite) (AppleConsumptionSettings, error)
	// AppleConsumptionConsent returns one customer's recorded consent or ErrNotFound.
	AppleConsumptionConsent(context.Context, core.ProjectID, core.ApplicationID, core.CustomerID) (AppleConsumptionConsent, error)
	// PutAppleConsumptionConsent records the latest consent decision for one customer.
	PutAppleConsumptionConsent(context.Context, AppleConsumptionConsentWrite) (AppleConsumptionConsent, error)
	// SaveAppleConsumptionRequest stores one request and enqueues its job atomically. A redelivery is a no-op.
	SaveAppleConsumptionRequest(context.Context, AppleConsumptionRequest) (string, error)
	// AppleConsumptionRequest loads one request with its protected payload and state.
	AppleConsumptionRequest(context.Context, string) (AppleConsumptionRequestRecord, error)
	// RecordAppleConsumptionAttempt appends one attempt and applies its status. False means the request was already terminal.
	RecordAppleConsumptionAttempt(context.Context, AppleConsumptionAttempt) (bool, error)
	// DeliveredPurchaseCustomers returns customers with an allowed observation carrying one protected reference.
	DeliveredPurchaseCustomers(context.Context, ProviderReferenceLookup) ([]core.CustomerID, error)
	// RecentAppleConsumptionRequests returns the newest requests for one application without protected payloads.
	RecentAppleConsumptionRequests(context.Context, core.ProjectID, core.ApplicationID, int) ([]AppleConsumptionRequestSummary, error)
}

// AppleConsumptionSettings is one application's operator choice for answering consumption requests.
// Revision zero means the operator never saved settings, which is the same as disabled.
// Consent is not a setting: a request is answered only for a customer with a recorded yes.
type AppleConsumptionSettings struct {
	ProjectID             core.ProjectID
	ApplicationID         core.ApplicationID
	Enabled               bool
	RefundPreference      string
	SampleContentProvided bool
	Revision              int64
	UpdatedByKeyID        string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// AppleConsumptionSettingsWrite creates or updates consumption settings.
type AppleConsumptionSettingsWrite struct {
	ProjectID             core.ProjectID
	ApplicationID         core.ApplicationID
	Enabled               bool
	RefundPreference      string
	SampleContentProvided bool
	ExpectedRevision      int64
	ActorKeyID            string
}

// AppleConsumptionConsent is the latest consent decision an application recorded for one customer.
type AppleConsumptionConsent struct {
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	CustomerID    core.CustomerID
	Consented     bool
	ActorKeyID    string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AppleConsumptionConsentWrite records one consent decision.
type AppleConsumptionConsentWrite struct {
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	CustomerID    core.CustomerID
	Consented     bool
	ActorKeyID    string
}

// AppleConsumptionRequest is one CONSUMPTION_REQUEST scheduled for an answer.
// CustomerID is empty when no IAPStack customer matched the app account token.
type AppleConsumptionRequest struct {
	ID                string
	ProjectID         core.ProjectID
	ApplicationID     core.ApplicationID
	CustomerID        core.CustomerID
	ProviderProductID core.ProviderProductID
	ProductType       string
	CustomerReason    string
	Payload           protection.Value
	SignedAt          time.Time
	ReceivedAt        time.Time
	DeadlineAt        time.Time
	AvailableAt       time.Time
}

// AppleConsumptionRequestRecord is one stored request with its current state.
type AppleConsumptionRequestRecord struct {
	AppleConsumptionRequest
	Status       string
	StatusReason string
	AttemptCount int
	CompletedAt  *time.Time
}

// AppleConsumptionRequestSummary is one request row for the administrative list. It has no protected payload.
type AppleConsumptionRequestSummary struct {
	ID                 string
	ApplicationID      core.ApplicationID
	CustomerID         core.CustomerID
	ProviderProductID  core.ProviderProductID
	ProductType        string
	CustomerReason     string
	Status             string
	StatusReason       string
	AttemptCount       int
	SignedAt           time.Time
	ReceivedAt         time.Time
	DeadlineAt         time.Time
	CompletedAt        *time.Time
	LastHTTPStatus     int
	LastAppleErrorCode int64
}

// AppleConsumptionAttempt is one handler decision for a request.
// Status is the terminal request status the attempt sets, or empty when the request stays pending.
// RequestBody is the exact JSON body sent to Apple, which carries no identifier.
type AppleConsumptionAttempt struct {
	RequestID      string
	AttemptedAt    time.Time
	Outcome        string
	Reason         string
	HTTPStatus     int
	AppleErrorCode int64
	RequestBody    json.RawMessage
	Status         string
}

// DefaultAppleConsumptionSettings returns the disabled settings used before an operator saves any.
func DefaultAppleConsumptionSettings(projectID core.ProjectID, applicationID core.ApplicationID) AppleConsumptionSettings {
	return AppleConsumptionSettings{
		ProjectID: projectID, ApplicationID: applicationID, RefundPreference: ConsumptionRefundPreferenceNone,
	}
}

// ValidConsumptionRefundPreference reports whether a refund preference policy is supported.
func ValidConsumptionRefundPreference(value string) bool {
	switch value {
	case ConsumptionRefundPreferenceNone, ConsumptionRefundPreferenceDecline,
		ConsumptionRefundPreferenceGrantFull, ConsumptionRefundPreferenceGrantProrated:
		return true
	default:
		return false
	}
}

// Validate checks settings scope, the refund preference, and revision input.
func (write AppleConsumptionSettingsWrite) Validate() error {
	var preferenceError, actorError error
	if !ValidConsumptionRefundPreference(write.RefundPreference) {
		preferenceError = fmt.Errorf("unsupported consumption refund preference %q", write.RefundPreference)
	}
	if write.ActorKeyID != "" {
		actorError = validateIdentifierText("consumption settings actor", write.ActorKeyID)
	}
	return errors.Join(
		write.ProjectID.Validate(),
		write.ApplicationID.Validate(),
		preferenceError,
		validateNonNegative("consumption settings expected revision", write.ExpectedRevision),
		actorError,
	)
}

// Validate checks consent scope and its recording actor.
func (write AppleConsumptionConsentWrite) Validate() error {
	return errors.Join(
		write.ProjectID.Validate(),
		write.ApplicationID.Validate(),
		write.CustomerID.Validate(),
		validateIdentifierText("consumption consent actor", write.ActorKeyID),
	)
}

// Validate checks request scope, closed vocabularies, protected payload, and schedule.
func (request AppleConsumptionRequest) Validate() error {
	var customerError, productTypeError, reasonError, scheduleError error
	if request.CustomerID != "" {
		customerError = request.CustomerID.Validate()
	}
	if _, ok := consumptionProductTypes[request.ProductType]; !ok {
		productTypeError = fmt.Errorf("unsupported consumption product type %q", request.ProductType)
	}
	if _, ok := consumptionCustomerReasons[request.CustomerReason]; request.CustomerReason != "" && !ok {
		reasonError = fmt.Errorf("unsupported consumption customer reason %q", request.CustomerReason)
	}
	if !request.SignedAt.IsZero() && !request.DeadlineAt.After(request.SignedAt) {
		scheduleError = errors.New("consumption deadline must follow the notification signing time")
	}
	if !request.ReceivedAt.IsZero() && request.AvailableAt.Before(request.ReceivedAt) {
		scheduleError = errors.Join(scheduleError, errors.New("consumption availability cannot precede receipt"))
	}
	return errors.Join(
		validateText("consumption request ID", request.ID),
		request.ProjectID.Validate(),
		request.ApplicationID.Validate(),
		customerError,
		request.ProviderProductID.Validate(),
		productTypeError,
		reasonError,
		request.Payload.Validate(),
		validateTime("consumption signing time", request.SignedAt),
		validateTime("consumption receipt time", request.ReceivedAt),
		validateTime("consumption deadline", request.DeadlineAt),
		validateTime("consumption availability time", request.AvailableAt),
		scheduleError,
	)
}

// Validate checks one attempt outcome, its reason code, and the status it applies.
func (attempt AppleConsumptionAttempt) Validate() error {
	var outcomeError, statusError, httpError, bodyError error
	switch attempt.Outcome {
	case ConsumptionAttemptSent, ConsumptionAttemptSkipped, ConsumptionAttemptExpired,
		ConsumptionAttemptTransientError, ConsumptionAttemptPermanentError:
	default:
		outcomeError = fmt.Errorf("unsupported consumption attempt outcome %q", attempt.Outcome)
	}
	expectedStatus := map[string]string{
		ConsumptionAttemptSent:           ConsumptionStatusSent,
		ConsumptionAttemptSkipped:        ConsumptionStatusSkipped,
		ConsumptionAttemptExpired:        ConsumptionStatusExpired,
		ConsumptionAttemptTransientError: "",
		ConsumptionAttemptPermanentError: ConsumptionStatusFailed,
	}[attempt.Outcome]
	if outcomeError == nil && attempt.Status != expectedStatus {
		statusError = fmt.Errorf("consumption attempt %q cannot set status %q", attempt.Outcome, attempt.Status)
	}
	if attempt.HTTPStatus != 0 && (attempt.HTTPStatus < 100 || attempt.HTTPStatus > 599) {
		httpError = errors.New("consumption attempt HTTP status is invalid")
	}
	if len(attempt.RequestBody) > 0 {
		var object map[string]any
		if err := json.Unmarshal(attempt.RequestBody, &object); err != nil || object == nil {
			bodyError = errors.New("consumption attempt request body must be a JSON object")
		}
	}
	return errors.Join(
		validateText("consumption request ID", attempt.RequestID),
		validateTime("consumption attempt time", attempt.AttemptedAt),
		outcomeError,
		ValidateConsumptionReason(attempt.Reason),
		statusError,
		httpError,
		bodyError,
	)
}

// ValidateConsumptionReason checks one bounded lowercase reason code.
func ValidateConsumptionReason(reason string) error {
	if !consumptionReasonPattern.MatchString(reason) {
		return errors.New("consumption reason must be a lowercase code of at most 64 characters")
	}
	return nil
}

// ConsumptionRequestListLimit clamps one requested page size to the supported range.
func ConsumptionRequestListLimit(limit int) int {
	if limit <= 0 || limit > maximumConsumptionRequestList {
		return maximumConsumptionRequestList
	}
	return limit
}

// validateIdentifierText rejects empty, padded, or control-bearing identifiers.
func validateIdentifierText(name, value string) error {
	if err := validateText(name, value); err != nil {
		return err
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}
