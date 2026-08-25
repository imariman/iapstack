package config_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/platform/config"
)

const (
	// httpAddressEnvironment names the HTTP listen-address setting used by tests.
	httpAddressEnvironment = "IAPSTACK_HTTP_ADDRESS"
	// shutdownTimeoutEnvironment names the graceful-shutdown setting used by tests.
	shutdownTimeoutEnvironment = "IAPSTACK_SHUTDOWN_TIMEOUT"
	// workerHTTPAddressEnvironment names the worker probe-address setting used by tests.
	workerHTTPAddressEnvironment = "IAPSTACK_WORKER_HTTP_ADDRESS"
	// logLevelEnvironment names the structured-log-level setting used by tests.
	logLevelEnvironment = "IAPSTACK_LOG_LEVEL"
	// databaseURLEnvironment names the PostgreSQL connection setting used by tests.
	databaseURLEnvironment = "IAPSTACK_DATABASE_URL"
	// webhookPrivateNetworksEnvironment names the explicit private webhook target opt-in.
	webhookPrivateNetworksEnvironment = "IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS"
	// authMaxDerivationsEnvironment names the memory-hard authentication concurrency setting.
	authMaxDerivationsEnvironment = "IAPSTACK_AUTH_MAX_CONCURRENT_DERIVATIONS"

	// defaultHTTPAddress is the expected listen address when no override is configured.
	defaultHTTPAddress = ":8080"
	// overriddenHTTPAddress is the non-default listen address used by override tests.
	overriddenHTTPAddress = "127.0.0.1:9090"
	// overriddenShutdownTimeout is the non-default graceful-shutdown duration used by tests.
	overriddenShutdownTimeout time.Duration = 3 * time.Second
	// testDatabaseURL is the canonical PostgreSQL connection string used by configuration tests.
	testDatabaseURL = "postgres://iapstack:secret@localhost/iapstack"
)

// TestLoadDefaults verifies configuration defaults when no environment values are set.
func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddress != defaultHTTPAddress {
		t.Errorf("HTTPAddress = %q, want %q", cfg.HTTPAddress, defaultHTTPAddress)
	}
	if cfg.WorkerHTTPAddress != ":8081" {
		t.Errorf("WorkerHTTPAddress = %q, want :8081", cfg.WorkerHTTPAddress)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("DatabaseURL = %q, want empty", cfg.DatabaseURL)
	}
	if cfg.WorkerID != "" {
		t.Errorf("WorkerID = %q, want automatic empty marker", cfg.WorkerID)
	}
	if cfg.QueueRetention != 30*24*time.Hour {
		t.Errorf("QueueRetention = %v, want 720h", cfg.QueueRetention)
	}
	if cfg.AuthMaxConcurrentDerivations != 4 {
		t.Errorf("AuthMaxConcurrentDerivations = %d, want 4", cfg.AuthMaxConcurrentDerivations)
	}
	if cfg.WebhookAllowPrivateNetworks {
		t.Error("WebhookAllowPrivateNetworks = true, want secure default false")
	}
}

// TestLoadOverrides verifies supported environment configuration overrides.
func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(map[string]string{
		httpAddressEnvironment:            overriddenHTTPAddress,
		shutdownTimeoutEnvironment:        overriddenShutdownTimeout.String(),
		logLevelEnvironment:               "debug",
		databaseURLEnvironment:            " " + testDatabaseURL + " ",
		webhookPrivateNetworksEnvironment: "true",
		authMaxDerivationsEnvironment:     "7",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPAddress != overriddenHTTPAddress {
		t.Errorf("HTTPAddress = %q, want %q", cfg.HTTPAddress, overriddenHTTPAddress)
	}
	if cfg.ShutdownTimeout != overriddenShutdownTimeout {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, overriddenShutdownTimeout)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if cfg.DatabaseURL != testDatabaseURL {
		t.Errorf("DatabaseURL = %q, want %q", cfg.DatabaseURL, testDatabaseURL)
	}
	if !cfg.WebhookAllowPrivateNetworks {
		t.Error("WebhookAllowPrivateNetworks = false, want true override")
	}
	if cfg.AuthMaxConcurrentDerivations != 7 {
		t.Errorf("AuthMaxConcurrentDerivations = %d, want 7", cfg.AuthMaxConcurrentDerivations)
	}
}

// TestLoadRejectsInvalidValues verifies fail-fast validation for malformed configuration.
func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "address shape", values: map[string]string{httpAddressEnvironment: "8080"}},
		{name: "address port", values: map[string]string{httpAddressEnvironment: ":0"}},
		{name: "worker address", values: map[string]string{workerHTTPAddressEnvironment: "8081"}},
		{name: "shutdown syntax", values: map[string]string{shutdownTimeoutEnvironment: "later"}},
		{name: "shutdown sign", values: map[string]string{shutdownTimeoutEnvironment: "-1s"}},
		{name: "log level", values: map[string]string{logLevelEnvironment: "verbose"}},
		{name: "worker attempts", values: map[string]string{"IAPSTACK_WORKER_MAX_ATTEMPTS": "0"}},
		{name: "private webhook setting", values: map[string]string{webhookPrivateNetworksEnvironment: "yes"}},
		{name: "authentication derivations sign", values: map[string]string{authMaxDerivationsEnvironment: "0"}},
		{name: "authentication derivations bound", values: map[string]string{authMaxDerivationsEnvironment: "33"}},
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
