package app_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/imariman/iapstack/internal/app"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

// TestRunRequiresKnownMode verifies that Run rejects missing, extra, and unknown modes.
func TestRunRequiresKnownMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing"},
		{name: "too many", args: []string{"api", "extra"}},
		{name: "unknown", args: []string{"unknown"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := app.Run(context.Background(), tt.args, emptyEnv, &bytes.Buffer{})
			if !errors.Is(err, app.ErrUsage) {
				t.Fatalf("Run() error = %v, want ErrUsage", err)
			}
		})
	}
}

// TestRunWorkerRequiresDatabaseURL verifies that the implemented worker fails fast without persistence.
func TestRunWorkerRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	err := app.Run(context.Background(), []string{"worker"}, emptyEnv, &bytes.Buffer{})
	if !errors.Is(err, postgres.ErrDatabaseURLRequired) {
		t.Fatalf("Run() error = %v, want ErrDatabaseURLRequired", err)
	}
}

// TestRunMigrateRequiresDatabaseURL verifies fail-fast migration configuration validation.
func TestRunMigrateRequiresDatabaseURL(t *testing.T) {
	t.Parallel()

	err := app.Run(context.Background(), []string{"migrate"}, emptyEnv, &bytes.Buffer{})
	if !errors.Is(err, postgres.ErrDatabaseURLRequired) {
		t.Fatalf("Run() error = %v, want ErrDatabaseURLRequired", err)
	}
}

// emptyEnv provides an environment lookup with no configured values.
func emptyEnv(string) string {
	return ""
}
