package stores

import (
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// FailureInvalidEvidence indicates malformed, forged, or inconsistent purchase evidence.
	FailureInvalidEvidence FailureKind = "invalid_evidence"
	// FailureUnauthorized indicates rejected provider credentials or authorization.
	FailureUnauthorized FailureKind = "unauthorized"
	// FailureNotFound indicates that the provider could not find the purchase.
	FailureNotFound FailureKind = "not_found"
	// FailureConflict indicates a provider-side concurrent or state conflict.
	FailureConflict FailureKind = "conflict"
	// FailureRateLimited indicates provider throttling that can be retried later.
	FailureRateLimited FailureKind = "rate_limited"
	// FailureTemporary indicates a transient provider or network failure.
	FailureTemporary FailureKind = "temporary"
	// FailurePermanent indicates a non-retryable provider failure.
	FailurePermanent FailureKind = "permanent"
)

type FailureKind string

type Failure struct {
	Provider   core.Provider
	Operation  string
	Kind       FailureKind
	RetryAfter time.Duration
	cause      error
}

// NewFailure constructs a classified provider operation failure with an optional cause.
func NewFailure(
	provider core.Provider,
	operation string,
	kind FailureKind,
	retryAfter time.Duration,
	cause error,
) *Failure {
	return &Failure{
		Provider:   provider,
		Operation:  operation,
		Kind:       kind,
		RetryAfter: retryAfter,
		cause:      cause,
	}
}

// Error returns a stable redacted provider failure description.
func (failure *Failure) Error() string {
	return fmt.Sprintf("store %s %s failed: %s", failure.Provider, failure.Operation, failure.Kind)
}

// Unwrap exposes the diagnostic cause to explicit error inspection.
func (failure *Failure) Unwrap() error {
	return failure.cause
}

// Retryable reports whether the failure category is safe to retry by default.
func (failure *Failure) Retryable() bool {
	return failure.Kind == FailureRateLimited || failure.Kind == FailureTemporary
}

// CodeValue returns one stable provider failure code for durable worker state.
func (failure *Failure) CodeValue() string {
	if failure == nil {
		return "provider_failure"
	}
	return "provider_" + string(failure.Kind)
}
