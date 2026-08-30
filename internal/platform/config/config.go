package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultHTTPAddress is the API listen address used when no override is supplied.
	defaultHTTPAddress = ":8080"
	// defaultWorkerHTTPAddress is the worker probe and metrics listen address.
	defaultWorkerHTTPAddress = ":8081"
	// defaultShutdownTimeout bounds graceful process shutdown.
	defaultShutdownTimeout = 10 * time.Second
	// defaultReadinessTimeout bounds one dependency readiness probe.
	defaultReadinessTimeout = 2 * time.Second
	// defaultWorkerPollInterval controls how often an idle worker checks durable queues.
	defaultWorkerPollInterval = time.Second
	// defaultWorkerJobTimeout bounds one durable job attempt.
	defaultWorkerJobTimeout = 30 * time.Second
	// defaultWorkerConcurrency limits concurrent durable job attempts per process.
	defaultWorkerConcurrency = 4
	// defaultWorkerMaxAttempts moves repeatedly failing records to their terminal state.
	defaultWorkerMaxAttempts = 12
	// defaultQueueRetention controls terminal River metadata and durable audit record lifetime.
	defaultQueueRetention = 30 * 24 * time.Hour
	// defaultHTTPBodyLimit bounds JSON and provider notification request bodies.
	defaultHTTPBodyLimit int64 = 1 << 20
	// defaultAuthMaxConcurrentDerivations bounds simultaneous memory-hard API key checks.
	defaultAuthMaxConcurrentDerivations = 4
	// maximumAuthConcurrentDerivations prevents unsafe memory allocation through configuration.
	maximumAuthConcurrentDerivations = 32
	// minimumMetricsBearerTokenLength prevents weak public metrics credentials.
	minimumMetricsBearerTokenLength = 32
	// defaultProviderTimeout bounds one outbound provider call.
	defaultProviderTimeout = 15 * time.Second
	// defaultWebhookTimeout bounds one outbound application webhook call.
	defaultWebhookTimeout = 10 * time.Second
	// defaultTelemetryExportInterval controls batched Grafana Cloud metric delivery.
	defaultTelemetryExportInterval = time.Minute
	// defaultTelemetryExportTimeout bounds one Grafana Cloud metric delivery attempt.
	defaultTelemetryExportTimeout = 10 * time.Second
)

// Config contains validated process configuration shared by IAPStack modes.
type Config struct {
	HTTPAddress                  string
	WorkerHTTPAddress            string
	ShutdownTimeout              time.Duration
	ReadinessTimeout             time.Duration
	LogLevel                     slog.Level
	DatabaseURL                  string
	AutoMigrate                  bool
	BootstrapAdminKey            string
	MetricsBearerToken           string
	WorkerID                     string
	WorkerPollInterval           time.Duration
	WorkerJobTimeout             time.Duration
	WorkerConcurrency            int
	WorkerMaxAttempts            int
	QueueRetention               time.Duration
	HTTPBodyLimit                int64
	AuthMaxConcurrentDerivations int
	ProviderTimeout              time.Duration
	HuaweiAllowPrivateNetworks   bool
	WebhookTimeout               time.Duration
	WebhookAllowPrivateNetworks  bool
	TelemetryEndpoint            string
	TelemetryUsername            string
	TelemetryToken               string
	TelemetryEnvironment         string
	TelemetryExportInterval      time.Duration
	TelemetryExportTimeout       time.Duration
}

// Load reads and validates process configuration from environment values.
func Load(getenv func(string) string) (Config, error) {
	var err error
	httpAddress, httpAddressEnvironment := resolveHTTPAddress(getenv)
	cfg := Config{
		HTTPAddress:                  httpAddress,
		WorkerHTTPAddress:            valueOrDefault(getenv("IAPSTACK_WORKER_HTTP_ADDRESS"), defaultWorkerHTTPAddress),
		ShutdownTimeout:              defaultShutdownTimeout,
		ReadinessTimeout:             defaultReadinessTimeout,
		LogLevel:                     slog.LevelInfo,
		DatabaseURL:                  strings.TrimSpace(getenv("IAPSTACK_DATABASE_URL")),
		AutoMigrate:                  false,
		BootstrapAdminKey:            strings.TrimSpace(getenv("IAPSTACK_BOOTSTRAP_ADMIN_KEY")),
		MetricsBearerToken:           strings.TrimSpace(getenv("IAPSTACK_METRICS_BEARER_TOKEN")),
		WorkerID:                     strings.TrimSpace(getenv("IAPSTACK_WORKER_ID")),
		WorkerPollInterval:           defaultWorkerPollInterval,
		WorkerJobTimeout:             defaultWorkerJobTimeout,
		WorkerConcurrency:            defaultWorkerConcurrency,
		WorkerMaxAttempts:            defaultWorkerMaxAttempts,
		QueueRetention:               defaultQueueRetention,
		HTTPBodyLimit:                defaultHTTPBodyLimit,
		AuthMaxConcurrentDerivations: defaultAuthMaxConcurrentDerivations,
		ProviderTimeout:              defaultProviderTimeout,
		HuaweiAllowPrivateNetworks:   false,
		WebhookTimeout:               defaultWebhookTimeout,
		WebhookAllowPrivateNetworks:  false,
		TelemetryEndpoint:            strings.TrimSpace(getenv("IAPSTACK_GRAFANA_OTLP_ENDPOINT")),
		TelemetryUsername:            strings.TrimSpace(getenv("IAPSTACK_GRAFANA_OTLP_USERNAME")),
		TelemetryToken:               strings.TrimSpace(getenv("IAPSTACK_GRAFANA_OTLP_TOKEN")),
		TelemetryEnvironment:         valueOrDefault(getenv("IAPSTACK_ENVIRONMENT"), "development"),
		TelemetryExportInterval:      defaultTelemetryExportInterval,
		TelemetryExportTimeout:       defaultTelemetryExportTimeout,
	}

	if raw := strings.TrimSpace(getenv("IAPSTACK_SHUTDOWN_TIMEOUT")); raw != "" {
		if cfg.ShutdownTimeout, err = positiveDuration("IAPSTACK_SHUTDOWN_TIMEOUT", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_AUTO_MIGRATE")); raw != "" {
		if cfg.AutoMigrate, err = strictBoolean("IAPSTACK_AUTO_MIGRATE", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_READINESS_TIMEOUT")); raw != "" {
		if cfg.ReadinessTimeout, err = positiveDuration("IAPSTACK_READINESS_TIMEOUT", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_WORKER_POLL_INTERVAL")); raw != "" {
		if cfg.WorkerPollInterval, err = positiveDuration("IAPSTACK_WORKER_POLL_INTERVAL", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_WORKER_JOB_TIMEOUT")); raw != "" {
		if cfg.WorkerJobTimeout, err = positiveDuration("IAPSTACK_WORKER_JOB_TIMEOUT", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_QUEUE_RETENTION")); raw != "" {
		if cfg.QueueRetention, err = positiveDuration("IAPSTACK_QUEUE_RETENTION", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_PROVIDER_TIMEOUT")); raw != "" {
		if cfg.ProviderTimeout, err = positiveDuration("IAPSTACK_PROVIDER_TIMEOUT", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS")); raw != "" {
		if cfg.HuaweiAllowPrivateNetworks, err = strictBoolean("IAPSTACK_HUAWEI_ALLOW_PRIVATE_NETWORKS", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_WEBHOOK_TIMEOUT")); raw != "" {
		if cfg.WebhookTimeout, err = positiveDuration("IAPSTACK_WEBHOOK_TIMEOUT", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_GRAFANA_EXPORT_INTERVAL")); raw != "" {
		if cfg.TelemetryExportInterval, err = positiveDuration("IAPSTACK_GRAFANA_EXPORT_INTERVAL", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_GRAFANA_EXPORT_TIMEOUT")); raw != "" {
		if cfg.TelemetryExportTimeout, err = positiveDuration("IAPSTACK_GRAFANA_EXPORT_TIMEOUT", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS")); raw != "" {
		if cfg.WebhookAllowPrivateNetworks, err = strictBoolean("IAPSTACK_WEBHOOK_ALLOW_PRIVATE_NETWORKS", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_WORKER_CONCURRENCY")); raw != "" {
		if cfg.WorkerConcurrency, err = positiveInteger("IAPSTACK_WORKER_CONCURRENCY", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_WORKER_MAX_ATTEMPTS")); raw != "" {
		if cfg.WorkerMaxAttempts, err = positiveInteger("IAPSTACK_WORKER_MAX_ATTEMPTS", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_HTTP_BODY_LIMIT")); raw != "" {
		bodyLimit, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || bodyLimit <= 0 {
			return Config{}, fmt.Errorf("IAPSTACK_HTTP_BODY_LIMIT must be a positive integer")
		}
		cfg.HTTPBodyLimit = bodyLimit
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_AUTH_MAX_CONCURRENT_DERIVATIONS")); raw != "" {
		derivations, parseErr := positiveInteger("IAPSTACK_AUTH_MAX_CONCURRENT_DERIVATIONS", raw)
		if parseErr != nil {
			return Config{}, parseErr
		}
		if derivations > maximumAuthConcurrentDerivations {
			return Config{}, fmt.Errorf(
				"IAPSTACK_AUTH_MAX_CONCURRENT_DERIVATIONS must not exceed %d",
				maximumAuthConcurrentDerivations,
			)
		}
		cfg.AuthMaxConcurrentDerivations = derivations
	}
	if cfg.MetricsBearerToken != "" && len(cfg.MetricsBearerToken) < minimumMetricsBearerTokenLength {
		return Config{}, fmt.Errorf("IAPSTACK_METRICS_BEARER_TOKEN must be at least %d characters", minimumMetricsBearerTokenLength)
	}
	if err := validateTelemetry(cfg); err != nil {
		return Config{}, err
	}

	if raw := strings.TrimSpace(getenv("IAPSTACK_LOG_LEVEL")); raw != "" {
		level, err := parseLogLevel(raw)
		if err != nil {
			return Config{}, err
		}
		cfg.LogLevel = level
	}

	if err := validateAddress(httpAddressEnvironment, cfg.HTTPAddress); err != nil {
		return Config{}, err
	}
	if err := validateAddress("IAPSTACK_WORKER_HTTP_ADDRESS", cfg.WorkerHTTPAddress); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// validateTelemetry requires complete HTTPS Grafana credentials and one bounded environment label.
func validateTelemetry(cfg Config) error {
	configuredValues := 0
	for _, value := range []string{cfg.TelemetryEndpoint, cfg.TelemetryUsername, cfg.TelemetryToken} {
		if value != "" {
			configuredValues++
		}
	}
	if configuredValues != 0 && configuredValues != 3 {
		return fmt.Errorf("IAPSTACK_GRAFANA_OTLP_ENDPOINT, IAPSTACK_GRAFANA_OTLP_USERNAME, and IAPSTACK_GRAFANA_OTLP_TOKEN must be configured together")
	}
	if cfg.TelemetryEndpoint != "" {
		endpoint, err := url.Parse(cfg.TelemetryEndpoint)
		if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return fmt.Errorf("IAPSTACK_GRAFANA_OTLP_ENDPOINT must be an HTTPS URL without credentials, query, or fragment")
		}
		if len(cfg.TelemetryToken) < minimumMetricsBearerTokenLength {
			return fmt.Errorf("IAPSTACK_GRAFANA_OTLP_TOKEN must be at least %d characters", minimumMetricsBearerTokenLength)
		}
	}
	if !validTelemetryEnvironment(cfg.TelemetryEnvironment) {
		return fmt.Errorf("IAPSTACK_ENVIRONMENT must contain 1-32 letters, numbers, dots, underscores, or hyphens")
	}
	return nil
}

// validTelemetryEnvironment bounds the only deployment-provided telemetry label.
func validTelemetryEnvironment(value string) bool {
	if len(value) == 0 || len(value) > 32 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

// resolveHTTPAddress prefers the native IAPStack setting and otherwise honors the
// conventional port injected by managed application platforms.
func resolveHTTPAddress(getenv func(string) string) (string, string) {
	if address := strings.TrimSpace(getenv("IAPSTACK_HTTP_ADDRESS")); address != "" {
		return address, "IAPSTACK_HTTP_ADDRESS"
	}
	if port := strings.TrimSpace(getenv("PORT")); port != "" {
		return ":" + port, "PORT"
	}
	return defaultHTTPAddress, "IAPSTACK_HTTP_ADDRESS"
}

// strictBoolean accepts explicit true or false configuration without permissive aliases.
func strictBoolean(name, value string) (bool, error) {
	switch strings.ToLower(value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

// positiveDuration parses one required positive duration setting.
func positiveDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return duration, nil
}

// positiveInteger parses one required positive integer setting.
func positiveInteger(name, value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return number, nil
}

// valueOrDefault returns a trimmed configured value or its fallback.
func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

// parseLogLevel converts a configured log level into its slog representation.
func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("IAPSTACK_LOG_LEVEL must be one of debug, info, warn, or error")
	}
}

// validateAddress checks one named host-port setting and usable TCP port range.
func validateAddress(name, address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%s must be in host:port form: %w", name, err)
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("%s port must be between 1 and 65535", name)
	}

	return nil
}
