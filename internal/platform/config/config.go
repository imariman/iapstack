package config

import (
	"fmt"
	"log/slog"
	"net"
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
	// defaultWorkerBatchSize limits records claimed by one worker poll.
	defaultWorkerBatchSize = 16
	// defaultWorkerMaxAttempts moves repeatedly failing records to their terminal state.
	defaultWorkerMaxAttempts = 12
	// defaultHTTPBodyLimit bounds JSON and provider notification request bodies.
	defaultHTTPBodyLimit int64 = 1 << 20
	// defaultProviderTimeout bounds one outbound provider call.
	defaultProviderTimeout = 15 * time.Second
	// defaultWebhookTimeout bounds one outbound application webhook call.
	defaultWebhookTimeout = 10 * time.Second
)

// Config contains validated process configuration shared by IAPStack modes.
type Config struct {
	HTTPAddress        string
	WorkerHTTPAddress  string
	ShutdownTimeout    time.Duration
	ReadinessTimeout   time.Duration
	LogLevel           slog.Level
	DatabaseURL        string
	BootstrapAdminKey  string
	WorkerID           string
	WorkerPollInterval time.Duration
	WorkerJobTimeout   time.Duration
	WorkerConcurrency  int
	WorkerBatchSize    int
	WorkerMaxAttempts  int
	HTTPBodyLimit      int64
	ProviderTimeout    time.Duration
	WebhookTimeout     time.Duration
}

// Load reads and validates process configuration from environment values.
func Load(getenv func(string) string) (Config, error) {
	var err error
	cfg := Config{
		HTTPAddress:        valueOrDefault(getenv("IAPSTACK_HTTP_ADDRESS"), defaultHTTPAddress),
		WorkerHTTPAddress:  valueOrDefault(getenv("IAPSTACK_WORKER_HTTP_ADDRESS"), defaultWorkerHTTPAddress),
		ShutdownTimeout:    defaultShutdownTimeout,
		ReadinessTimeout:   defaultReadinessTimeout,
		LogLevel:           slog.LevelInfo,
		DatabaseURL:        strings.TrimSpace(getenv("IAPSTACK_DATABASE_URL")),
		BootstrapAdminKey:  strings.TrimSpace(getenv("IAPSTACK_BOOTSTRAP_ADMIN_KEY")),
		WorkerID:           valueOrDefault(getenv("IAPSTACK_WORKER_ID"), "worker-1"),
		WorkerPollInterval: defaultWorkerPollInterval,
		WorkerJobTimeout:   defaultWorkerJobTimeout,
		WorkerConcurrency:  defaultWorkerConcurrency,
		WorkerBatchSize:    defaultWorkerBatchSize,
		WorkerMaxAttempts:  defaultWorkerMaxAttempts,
		HTTPBodyLimit:      defaultHTTPBodyLimit,
		ProviderTimeout:    defaultProviderTimeout,
		WebhookTimeout:     defaultWebhookTimeout,
	}

	if raw := strings.TrimSpace(getenv("IAPSTACK_SHUTDOWN_TIMEOUT")); raw != "" {
		if cfg.ShutdownTimeout, err = positiveDuration("IAPSTACK_SHUTDOWN_TIMEOUT", raw); err != nil {
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
	if raw := strings.TrimSpace(getenv("IAPSTACK_PROVIDER_TIMEOUT")); raw != "" {
		if cfg.ProviderTimeout, err = positiveDuration("IAPSTACK_PROVIDER_TIMEOUT", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_WEBHOOK_TIMEOUT")); raw != "" {
		if cfg.WebhookTimeout, err = positiveDuration("IAPSTACK_WEBHOOK_TIMEOUT", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_WORKER_CONCURRENCY")); raw != "" {
		if cfg.WorkerConcurrency, err = positiveInteger("IAPSTACK_WORKER_CONCURRENCY", raw); err != nil {
			return Config{}, err
		}
	}
	if raw := strings.TrimSpace(getenv("IAPSTACK_WORKER_BATCH_SIZE")); raw != "" {
		if cfg.WorkerBatchSize, err = positiveInteger("IAPSTACK_WORKER_BATCH_SIZE", raw); err != nil {
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

	if raw := strings.TrimSpace(getenv("IAPSTACK_LOG_LEVEL")); raw != "" {
		level, err := parseLogLevel(raw)
		if err != nil {
			return Config{}, err
		}
		cfg.LogLevel = level
	}

	if err := validateHTTPAddress(cfg.HTTPAddress); err != nil {
		return Config{}, err
	}
	if err := validateAddress("IAPSTACK_WORKER_HTTP_ADDRESS", cfg.WorkerHTTPAddress); err != nil {
		return Config{}, err
	}

	return cfg, nil
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

// validateHTTPAddress checks host-port syntax and the usable TCP port range.
func validateHTTPAddress(address string) error {
	return validateAddress("IAPSTACK_HTTP_ADDRESS", address)
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
