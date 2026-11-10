package persistence

import (
	"errors"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/protection"
)

// ReconciliationScheduleRetry is one durable pending verification payload awaiting a reconciliation job insert.
type ReconciliationScheduleRetry struct {
	ID            string
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	CustomerID    core.CustomerID
	Payload       protection.Value
	// Attempts is how many promotions have already failed. Zero is a new row.
	Attempts int
	// NextAttemptAt is when the worker may select the row again. Zero on insert uses the database default.
	NextAttemptAt time.Time
}

// ReconciliationScheduleRetryFailure is one failed promotion of a pending schedule retry.
type ReconciliationScheduleRetryFailure struct {
	ID            string
	AttemptedAt   time.Time
	NextAttemptAt time.Time
	Dead          bool
}

// Validate checks one pending schedule retry and its protected verification payload.
func (retry ReconciliationScheduleRetry) Validate() error {
	var attemptError error
	if retry.Attempts < 0 {
		attemptError = errors.New("reconciliation schedule retry attempts cannot be negative")
	}
	return errors.Join(
		validateText("reconciliation schedule retry ID", retry.ID),
		retry.ProjectID.Validate(),
		retry.ApplicationID.Validate(),
		retry.CustomerID.Validate(),
		retry.Payload.Validate(),
		attemptError,
	)
}

// Validate checks the identity and schedule of one failed promotion.
func (failure ReconciliationScheduleRetryFailure) Validate() error {
	return errors.Join(
		validateText("reconciliation schedule retry ID", failure.ID),
		validateTime("reconciliation schedule retry attempt time", failure.AttemptedAt),
		validateTime("reconciliation schedule retry next attempt time", failure.NextAttemptAt),
	)
}
