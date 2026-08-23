package config_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/platform/config"
)

// TestLoadDefaults verifies configuration defaults when no environment values are set.
func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddress != ":8080" {
		t.Errorf("HTTPAddress = %q, want :8080", cfg.HTTPAddress)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
}

// TestLoadOverrides verifies supported environment configuration overrides.
func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(map[string]string{
		"IAPSTACK_HTTP_ADDRESS":     "127.0.0.1:9090",
		"IAPSTACK_SHUTDOWN_TIMEOUT": "3s",
		"IAPSTACK_LOG_LEVEL":        "debug",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddress != "127.0.0.1:9090" {
		t.Errorf("HTTPAddress = %q, want 127.0.0.1:9090", cfg.HTTPAddress)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 3s", cfg.ShutdownTimeout)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
}

// TestLoadRejectsInvalidValues verifies fail-fast validation for malformed configuration.
func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "address shape", values: map[string]string{"IAPSTACK_HTTP_ADDRESS": "8080"}},
		{name: "address port", values: map[string]string{"IAPSTACK_HTTP_ADDRESS": ":0"}},
		{name: "shutdown syntax", values: map[string]string{"IAPSTACK_SHUTDOWN_TIMEOUT": "later"}},
		{name: "shutdown sign", values: map[string]string{"IAPSTACK_SHUTDOWN_TIMEOUT": "-1s"}},
		{name: "log level", values: map[string]string{"IAPSTACK_LOG_LEVEL": "verbose"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := config.Load(env(tt.values)); err == nil {
				t.Fatal("Load() error = nil, want validation error")
			}
		})
	}
}

// env builds a deterministic environment lookup for configuration tests.
func env(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
