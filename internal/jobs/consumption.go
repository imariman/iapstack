package jobs

import (
	"time"
)

const (
	// QueueAppleConsumption isolates App Store consumption responses from inbox and reconciliation capacity.
	QueueAppleConsumption = "iapstack_apple_consumption"
	// KindAppleConsumption identifies one protected App Store consumption response job.
	KindAppleConsumption = "iapstack_apple_consumption"
	// AppleConsumptionMaxAttempts bounds handler runs for one consumption request.
	// With AppleConsumptionRetryAt the last retry is about four hours after the first attempt.
	AppleConsumptionMaxAttempts = 10
	// AppleConsumptionResponseWindow is Apple's documented time to answer a CONSUMPTION_REQUEST.
	AppleConsumptionResponseWindow = 12 * time.Hour
	// appleConsumptionFirstRetryDelay is the backoff after the first failed attempt.
	appleConsumptionFirstRetryDelay = time.Minute
	// appleConsumptionMaximumRetryDelay caps the doubling backoff.
	appleConsumptionMaximumRetryDelay = time.Hour
)

// AppleConsumptionArgs identifies one protected consumption request and carries its non-sensitive deadline.
// The deadline lets the worker clamp retries without loading the request row.
type AppleConsumptionArgs struct {
	RequestID  string    `json:"request_id"`
	DeadlineAt time.Time `json:"deadline_at"`
}

// AppleConsumptionPayload is the protected part of one consumption request.
// It holds the App Store identifiers that must not be stored in plaintext.
type AppleConsumptionPayload struct {
	TransactionID      string `json:"transaction_id"`
	ExternalCustomerID string `json:"external_customer_id"`
	Ownership          string `json:"ownership"`
}

// Kind returns the stable River job kind for App Store consumption responses.
func (AppleConsumptionArgs) Kind() string {
	return KindAppleConsumption
}

// AppleConsumptionRetryAt returns when a failed attempt runs again.
// The delay doubles from one minute and stops growing at one hour. It never passes the deadline,
// so the last retry runs at the deadline and records the request as expired.
func AppleConsumptionRetryAt(attempt int, failedAt, deadline time.Time) time.Time {
	if attempt < 1 {
		attempt = 1
	}
	delay := appleConsumptionMaximumRetryDelay
	if attempt <= 7 {
		delay = min(appleConsumptionFirstRetryDelay<<(attempt-1), appleConsumptionMaximumRetryDelay)
	}
	next := failedAt.UTC().Add(delay)
	if !deadline.IsZero() && next.After(deadline.UTC()) {
		return deadline.UTC()
	}
	return next
}
