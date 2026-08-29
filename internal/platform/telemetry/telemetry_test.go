package telemetry

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// TestNewLeavesTelemetryDisabledWithoutEndpoint verifies local and test runs need no cloud dependency.
func TestNewLeavesTelemetryDisabledWithoutEndpoint(t *testing.T) {
	t.Parallel()

	provider, err := New(context.Background(), Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if provider.Enabled() {
		t.Fatal("Enabled() = true, want false")
	}
	if provider.Meter() != nil {
		t.Fatal("Meter() is non-nil while telemetry is disabled")
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

// TestBasicAuthorizationEncodesExactCredential verifies Grafana authentication formatting.
func TestBasicAuthorizationEncodesExactCredential(t *testing.T) {
	t.Parallel()

	header := basicAuthorization("12345", "grafana-cloud-access-token")
	if !strings.HasPrefix(header, "Basic ") {
		t.Fatalf("authorization = %q, want Basic scheme", header)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if string(decoded) != "12345:grafana-cloud-access-token" {
		t.Fatalf("decoded credential = %q, want exact username and token", decoded)
	}
}

// TestMetricsEndpointURLAcceptsGrafanaBaseAndSignalURLs verifies the exporter posts to OTLP metrics.
func TestMetricsEndpointURLAcceptsGrafanaBaseAndSignalURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "Grafana base", base: "https://otlp.example.com/otlp", want: "https://otlp.example.com/otlp/v1/metrics"},
		{name: "trailing slash", base: "https://otlp.example.com/otlp/", want: "https://otlp.example.com/otlp/v1/metrics"},
		{name: "signal URL", base: "https://otlp.example.com/otlp/v1/metrics", want: "https://otlp.example.com/otlp/v1/metrics"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := metricsEndpointURL(tt.base); got != tt.want {
				t.Errorf("metricsEndpointURL(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

// TestNewRejectsUnusableExportTiming verifies exporter timing cannot silently disable delivery.
func TestNewRejectsUnusableExportTiming(t *testing.T) {
	t.Parallel()

	_, err := New(context.Background(), Config{
		Endpoint: "https://example.invalid/otlp", Username: "1", Token: "token",
		Environment: "test", Instance: "server", ExportInterval: 0, ExportTimeout: time.Second,
	})
	if err == nil {
		t.Fatal("New() error = nil, want invalid interval error")
	}
}
