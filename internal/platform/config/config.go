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
	// defaultShutdownTimeout bounds graceful process shutdown.
	defaultShutdownTimeout = 10 * time.Second
)

type Config struct {
	HTTPAddress     string
	ShutdownTimeout time.Duration
	LogLevel        slog.Level
}

// Load reads and validates process configuration from environment values.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		HTTPAddress:     valueOrDefault(getenv("IAPSTACK_HTTP_ADDRESS"), defaultHTTPAddress),
		ShutdownTimeout: defaultShutdownTimeout,
		LogLevel:        slog.LevelInfo,
	}

	if raw := strings.TrimSpace(getenv("IAPSTACK_SHUTDOWN_TIMEOUT")); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil || timeout <= 0 {
			return Config{}, fmt.Errorf("IAPSTACK_SHUTDOWN_TIMEOUT must be a positive duration")
		}
		cfg.ShutdownTimeout = timeout
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

	return cfg, nil
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

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

func validateHTTPAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("IAPSTACK_HTTP_ADDRESS must be in host:port form: %w", err)
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("IAPSTACK_HTTP_ADDRESS port must be between 1 and 65535")
	}

	return nil
}
