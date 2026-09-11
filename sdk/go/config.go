package iapstack

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

const (
	// defaultTimeout bounds one HTTP attempt, including response streaming.
	defaultTimeout = 10 * time.Second
	// defaultMaxResponseBytes bounds JSON response bodies.
	defaultMaxResponseBytes int64 = 1 << 20
)

// Config is runtime-only trusted-host configuration for one application.
type Config struct {
	// BaseURL is the IAPStack origin, optionally including a reverse-proxy path prefix.
	BaseURL string
	// ApplicationID is the application scope encoded in every public API path.
	ApplicationID string
	// ApplicationToken is the durable host bearer retained only in memory by this SDK.
	ApplicationToken string
	// Timeout is the maximum duration of one HTTP attempt, including response streaming.
	Timeout time.Duration
	// RetryPolicy is the bounded retry policy for idempotent IAPStack operations.
	RetryPolicy RetryPolicy
	// MaxResponseBytes is the maximum accepted JSON response size.
	MaxResponseBytes int64
	// AllowInsecureHTTP allows plain HTTP for explicit local development environments.
	AllowInsecureHTTP bool
	// HTTPClient is an optional injected transport; when nil the SDK owns a default client.
	HTTPClient *http.Client
}

// Validate reports whether the host configuration is complete and safe.
func (config Config) Validate() error {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return fmt.Errorf("base URL must be an origin or path prefix")
	}
	if parsed.Scheme != "https" && !(config.AllowInsecureHTTP && parsed.Scheme == "http") {
		return fmt.Errorf("base URL must use HTTPS")
	}
	if strings.TrimSpace(config.ApplicationID) == "" || strings.Contains(config.ApplicationID, "/") {
		return fmt.Errorf("application ID must be one non-empty path segment")
	}
	if err := validateBearer("application token", config.ApplicationToken); err != nil {
		return err
	}
	if config.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if config.MaxResponseBytes <= 0 {
		return fmt.Errorf("max response bytes must be positive")
	}
	return config.RetryPolicy.Validate()
}

// applyDefaults fills zero-value operational settings without touching credentials.
func (config Config) applyDefaults() Config {
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if config.MaxResponseBytes == 0 {
		config.MaxResponseBytes = defaultMaxResponseBytes
	}
	if config.RetryPolicy == (RetryPolicy{}) {
		config.RetryPolicy = DefaultRetryPolicy()
	}
	return config
}

// validateBearer rejects empty or whitespace-bearing secrets without echoing them.
func validateBearer(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be a non-empty bearer token", name)
	}
	for _, character := range value {
		if unicode.IsSpace(character) {
			return fmt.Errorf("%s must be a non-empty bearer token", name)
		}
	}
	return nil
}
