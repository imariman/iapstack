package stores

import (
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

type FailureKind string

const (
	FailureInvalidEvidence FailureKind = "invalid_evidence"
	FailureUnauthorized    FailureKind = "unauthorized"
	FailureNotFound        FailureKind = "not_found"
	FailureConflict        FailureKind = "conflict"
	FailureRateLimited     FailureKind = "rate_limited"
	FailureTemporary       FailureKind = "temporary"
	FailurePermanent       FailureKind = "permanent"
)

type Failure struct {
	Provider   core.Provider
	Operation  string
	Kind       FailureKind
	RetryAfter time.Duration
	cause      error
}

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

func (failure *Failure) Error() string {
	return fmt.Sprintf("store %s %s failed: %s", failure.Provider, failure.Operation, failure.Kind)
}

func (failure *Failure) Unwrap() error {
	return failure.cause
}

func (failure *Failure) Retryable() bool {
	return failure.Kind == FailureRateLimited || failure.Kind == FailureTemporary
}
