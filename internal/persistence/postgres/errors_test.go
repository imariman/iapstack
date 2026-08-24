package postgres

import (
	"errors"
	"testing"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestClassifyErrorMarksRuntimeTimeoutsUnavailable verifies stable operational error mapping.
func TestClassifyErrorMarksRuntimeTimeoutsUnavailable(t *testing.T) {
	for _, code := range []string{"53300", "57014", "57P01", "57P02", "57P03"} {
		err := classifyError("test operation", &pgconn.PgError{Code: code})
		if !errors.Is(err, persistence.ErrUnavailable) {
			t.Fatalf("classifyError(%q) = %v, want ErrUnavailable", code, err)
		}
	}
}

// TestClassifyErrorMarksLockTimeoutRetryable verifies lock contention receives a fresh transaction attempt.
func TestClassifyErrorMarksLockTimeoutRetryable(t *testing.T) {
	err := classifyError("test operation", &pgconn.PgError{Code: "55P03"})
	if !errors.Is(err, errRetryableTransaction) {
		t.Fatalf("classifyError(55P03) = %v, want retryable transaction", err)
	}
}
