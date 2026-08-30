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
	// portEnvironment names the conventional managed-platform listen port.
	portEnvironment = "PORT"
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
	// huaweiPrivateNetworksEnvironment names the explicit private Huawei endpoint opt-in.
	huaweiPrivateNetworksEnvironment = "IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS"
	// authMaxDerivationsEnvironment names the memory-hard authentication concurrency setting.
	authMaxDerivationsEnvironment = "IAPSTACK_AUTH_MAX_CONCURRENT_DERIVATIONS"
	// metricsBearerTokenEnvironment names the dedicated operational metrics credential.
	metricsBearerTokenEnvironment = "IAPSTACK_METRICS_BEARER_TOKEN"
	// grafanaEndpointEnvironment names the managed OTLP ingestion URL.
	grafanaEndpointEnvironment = "IAPSTACK_GRAFANA_OTLP_ENDPOINT"
	// grafanaUsernameEnvironment names the managed OTLP instance identity.
	grafanaUsernameEnvironment = "IAPSTACK_GRAFANA_OTLP_USERNAME"
	// grafanaTokenEnvironment names the managed OTLP access-policy secret.
	grafanaTokenEnvironment = "IAPSTACK_GRAFANA_OTLP_TOKEN"
	// telemetryEnvironmentEnvironment names the bounded deployment environment label.
	telemetryEnvironmentEnvironment = "IAPSTACK_ENVIRONMENT"

	// defaultHTTPAddress is the expected listen address when no override is configured.
	defaultHTTPAddress = ":8080"
	// overriddenHTTPAddress is the non-default listen address used by override tests.
	overriddenHTTPAddress = "127.0.0.1:9090"
	// overriddenShutdownTimeout is the non-default graceful-shutdown duration used by tests.
	overriddenShutdownTimeout time.Duration = 3 * time.Second
	// testDatabaseURL is the canonical PostgreSQL connection string used by configuration tests.
	testDatabaseURL = "postgres://iapstack:secret@localhost/iapstack"
	// testMetricsBearerToken is a sufficiently long metrics credential used by configuration tests.
	testMetricsBearerToken = "metrics-configuration-token-32-characters"
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
	if cfg.AutoMigrate {
		t.Error("AutoMigrate = true, want secure default false")
	}
	if cfg.WorkerID != "" {
		t.Errorf("WorkerID = %q, want automatic empty marker", cfg.WorkerID)
	}
	if cfg.MetricsBearerToken != "" {
		t.Errorf("MetricsBearerToken = %q, want empty", cfg.MetricsBearerToken)
	}
	if cfg.TelemetryEndpoint != "" || cfg.TelemetryUsername != "" || cfg.TelemetryToken != "" {
		t.Errorf("telemetry credentials = (%q, %q, %q), want disabled", cfg.TelemetryEndpoint, cfg.TelemetryUsername, cfg.TelemetryToken)
	}
	if cfg.TelemetryEnvironment != "development" {
		t.Errorf("TelemetryEnvironment = %q, want development", cfg.TelemetryEnvironment)
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
	if cfg.HuaweiAllowPrivateNetworks {
		t.Error("HuaweiAllowPrivateNetworks = true, want secure default false")
	}
}

// TestLoadUsesPlatformPort verifies managed platforms can inject their native port.
func TestLoadUsesPlatformPort(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(map[string]string{portEnvironment: "10000"}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddress != ":10000" {
		t.Errorf("HTTPAddress = %q, want :10000", cfg.HTTPAddress)
	}
}

// TestLoadHTTPAddressOverridesPlatformPort verifies the product-specific setting wins.
func TestLoadHTTPAddressOverridesPlatformPort(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(map[string]string{
		httpAddressEnvironment: overriddenHTTPAddress,
		portEnvironment:        "10000",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTPAddress != overriddenHTTPAddress {
		t.Errorf("HTTPAddress = %q, want %q", cfg.HTTPAddress, overriddenHTTPAddress)
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
		"IAPSTACK_AUTO_MIGRATE":           "true",
		webhookPrivateNetworksEnvironment: "true",
		huaweiPrivateNetworksEnvironment:  "true",
		authMaxDerivationsEnvironment:     "7",
		metricsBearerTokenEnvironment:     " " + testMetricsBearerToken + " ",
		grafanaEndpointEnvironment:        "https://otlp-gateway.example.com/otlp",
		grafanaUsernameEnvironment:        "123456",
		grafanaTokenEnvironment:           testMetricsBearerToken,
		telemetryEnvironmentEnvironment:   "sandbox",
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
	if !cfg.AutoMigrate {
		t.Error("AutoMigrate = false, want true override")
	}
	if !cfg.WebhookAllowPrivateNetworks {
		t.Error("WebhookAllowPrivateNetworks = false, want true override")
	}
	if !cfg.HuaweiAllowPrivateNetworks {
		t.Error("HuaweiAllowPrivateNetworks = false, want true override")
	}
	if cfg.AuthMaxConcurrentDerivations != 7 {
		t.Errorf("AuthMaxConcurrentDerivations = %d, want 7", cfg.AuthMaxConcurrentDerivations)
	}
	if cfg.MetricsBearerToken != testMetricsBearerToken {
		t.Errorf("MetricsBearerToken = %q, want configured token", cfg.MetricsBearerToken)
	}
	if cfg.TelemetryEndpoint != "https://otlp-gateway.example.com/otlp" ||
		cfg.TelemetryUsername != "123456" || cfg.TelemetryToken != testMetricsBearerToken ||
		cfg.TelemetryEnvironment != "sandbox" {
		t.Errorf("telemetry configuration = (%q, %q, %q, %q), want configured Grafana values",
			cfg.TelemetryEndpoint, cfg.TelemetryUsername, cfg.TelemetryToken, cfg.TelemetryEnvironment)
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
		{name: "platform port syntax", values: map[string]string{portEnvironment: "http"}},
		{name: "platform port range", values: map[string]string{portEnvironment: "65536"}},
		{name: "worker address", values: map[string]string{workerHTTPAddressEnvironment: "8081"}},
		{name: "shutdown syntax", values: map[string]string{shutdownTimeoutEnvironment: "later"}},
		{name: "shutdown sign", values: map[string]string{shutdownTimeoutEnvironment: "-1s"}},
		{name: "auto migrate setting", values: map[string]string{"IAPSTACK_AUTO_MIGRATE": "yes"}},
		{name: "log level", values: map[string]string{logLevelEnvironment: "verbose"}},
		{name: "worker attempts", values: map[string]string{"IAPSTACK_WORKER_MAX_ATTEMPTS": "0"}},
		{name: "private webhook setting", values: map[string]string{webhookPrivateNetworksEnvironment: "yes"}},
		{name: "private huawei setting", values: map[string]string{huaweiPrivateNetworksEnvironment: "yes"}},
		{name: "authentication derivations sign", values: map[string]string{authMaxDerivationsEnvironment: "0"}},
		{name: "authentication derivations bound", values: map[string]string{authMaxDerivationsEnvironment: "33"}},
		{name: "metrics bearer too short", values: map[string]string{metricsBearerTokenEnvironment: "short"}},
		{name: "partial Grafana credentials", values: map[string]string{grafanaEndpointEnvironment: "https://example.com/otlp"}},
		{name: "insecure Grafana endpoint", values: map[string]string{
			grafanaEndpointEnvironment: "http://example.com/otlp", grafanaUsernameEnvironment: "1", grafanaTokenEnvironment: testMetricsBearerToken,
		}},
		{name: "Grafana endpoint credentials", values: map[string]string{
			grafanaEndpointEnvironment: "https://user@example.com/otlp", grafanaUsernameEnvironment: "1", grafanaTokenEnvironment: testMetricsBearerToken,
		}},
		{name: "Grafana token too short", values: map[string]string{
			grafanaEndpointEnvironment: "https://example.com/otlp", grafanaUsernameEnvironment: "1", grafanaTokenEnvironment: "short",
		}},
		{name: "unsafe telemetry environment", values: map[string]string{telemetryEnvironmentEnvironment: "production/customer"}},
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
