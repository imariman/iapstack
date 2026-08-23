package app_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/imariman/iapstack/internal/app"
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

// TestRunReportsReservedModes verifies that reserved modes return their bootstrap error.
func TestRunReportsReservedModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"worker", "migrate"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			err := app.Run(context.Background(), []string{mode}, emptyEnv, &bytes.Buffer{})
			if !errors.Is(err, app.ErrModeUnavailable) {
				t.Fatalf("Run() error = %v, want ErrModeUnavailable", err)
			}
		})
	}
}

// emptyEnv provides an environment lookup with no configured values.
func emptyEnv(string) string {
	return ""
}
