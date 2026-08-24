package postgres

import (
	"errors"
	"fmt"
	"strings"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// errRetryableTransaction marks PostgreSQL concurrency failures that require a fresh transaction snapshot.
	errRetryableTransaction = errors.New("retryable PostgreSQL transaction failure")
)

// validateText enforces non-empty, trimmed PostgreSQL metadata values.
func validateText(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have leading or trailing whitespace", name)
	}
	return nil
}

// classifyError translates stable PostgreSQL outcomes without exposing statement values.
func classifyError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, persistence.ErrNotFound)
	}

	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "40001", "40P01", "55P03":
			return fmt.Errorf("%s: %w", operation, errRetryableTransaction)
		case "53300", "57014", "57P01", "57P02", "57P03":
			return fmt.Errorf("%s: %w", operation, persistence.ErrUnavailable)
		case "23503", "23505", "23514":
			if postgresError.ConstraintName != "" {
				return fmt.Errorf(
					"%s: %w: PostgreSQL constraint %s",
					operation,
					persistence.ErrConflict,
					postgresError.ConstraintName,
				)
			}
			return fmt.Errorf("%s: %w", operation, persistence.ErrConflict)
		}
	}
	if pgconn.SafeToRetry(err) {
		return fmt.Errorf("%s: %w", operation, persistence.ErrUnavailable)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
