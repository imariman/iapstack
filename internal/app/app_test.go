package app_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/imariman/iapstack/internal/app"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"go.uber.org/goleak"
)

// TestMain fails the package when process lifecycle tests leave goroutines behind.
func TestMain(main *testing.M) {
	goleak.VerifyTestMain(main)
}

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

// TestRunRuntimeModesRequireDatabaseURL verifies implemented runtime modes fail fast without persistence.
func TestRunRuntimeModesRequireDatabaseURL(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"server", "api", "worker"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			err := app.Run(context.Background(), []string{mode}, emptyEnv, &bytes.Buffer{})
			if !errors.Is(err, postgres.ErrDatabaseURLRequired) {
				t.Fatalf("Run() error = %v, want ErrDatabaseURLRequired", err)
			}
		})
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

// TestRunRejectsAutoMigrateOutsideCompactServer verifies split processes keep release migrations separate.
func TestRunRejectsAutoMigrateOutsideCompactServer(t *testing.T) {
	t.Parallel()

	getenv := func(name string) string {
		if name == "IAPSTACK_AUTO_MIGRATE" {
			return "true"
		}
		return ""
	}
	for _, mode := range []string{"api", "worker"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			err := app.Run(context.Background(), []string{mode}, getenv, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "supported only in server mode") {
				t.Fatalf("Run() error = %v, want compact-mode validation", err)
			}
		})
	}
}

// emptyEnv provides an environment lookup with no configured values.
func emptyEnv(string) string {
	return ""
}
