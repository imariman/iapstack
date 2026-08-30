// Package telemetry sends bounded IAPStack metrics to one managed OTLP destination.
package telemetry

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
)

const (
	// instrumentationName identifies the stable IAPStack metrics instrumentation scope.
	instrumentationName = "github.com/imariman/iapstack/internal/platform/metrics"
	// serviceName is the low-cardinality service identity exported to Grafana Cloud.
	serviceName = "iapstack"
)

// Config contains one optional Grafana Cloud OTLP connection and bounded export timing.
type Config struct {
	Endpoint       string
	Username       string
	Token          string
	Environment    string
	Instance       string
	ExportInterval time.Duration
	ExportTimeout  time.Duration
}

// Provider owns the OTLP exporter, periodic reader, and metric instrument factory.
type Provider struct {
	meterProvider *metric.MeterProvider
}

// New constructs an optional direct OTLP provider without starting a separate collector process.
func New(ctx context.Context, config Config) (*Provider, error) {
	if config.Endpoint == "" {
		return &Provider{}, nil
	}
	if config.Username == "" || config.Token == "" || config.Environment == "" || config.Instance == "" ||
		config.ExportInterval <= 0 || config.ExportTimeout <= 0 {
		return nil, errors.New("OTLP metrics configuration is incomplete")
	}
	exporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(metricsEndpointURL(config.Endpoint)),
		otlpmetrichttp.WithHeaders(map[string]string{
			"Authorization": basicAuthorization(config.Username, config.Token),
		}),
		otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression),
		otlpmetrichttp.WithTimeout(config.ExportTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("construct OTLP metrics exporter: %w", err)
	}
	reader := metric.NewPeriodicReader(exporter,
		metric.WithInterval(config.ExportInterval),
		metric.WithTimeout(config.ExportTimeout),
	)
	provider := metric.NewMeterProvider(
		metric.WithReader(reader),
		metric.WithResource(resource.NewSchemaless(
			attribute.String("service.name", serviceName),
			attribute.String("service.instance.id", config.Instance),
			attribute.String("deployment.environment.name", config.Environment),
		)),
	)
	return &Provider{meterProvider: provider}, nil
}

// Enabled reports whether direct OTLP export is configured for this process.
func (provider *Provider) Enabled() bool {
	return provider != nil && provider.meterProvider != nil
}

// Meter returns the IAPStack instrumentation scope when export is enabled.
func (provider *Provider) Meter() otelmetric.Meter {
	if !provider.Enabled() {
		return nil
	}
	return provider.meterProvider.Meter(instrumentationName)
}

// Shutdown flushes pending metrics and releases exporter resources within the caller deadline.
func (provider *Provider) Shutdown(ctx context.Context) error {
	if !provider.Enabled() {
		return nil
	}
	if err := provider.meterProvider.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("shutdown OTLP metrics provider: %w", err)
	}
	return nil
}

// basicAuthorization builds the Grafana OTLP Basic credential without logging its inputs.
func basicAuthorization(username, token string) string {
	credential := base64.StdEncoding.EncodeToString([]byte(username + ":" + token))
	return "Basic " + credential
}

// metricsEndpointURL converts Grafana's copied OTLP base URL to the metrics signal URL.
func metricsEndpointURL(endpoint string) string {
	trimmed := strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(trimmed, "/v1/metrics") {
		return trimmed
	}
	return trimmed + "/v1/metrics"
}
