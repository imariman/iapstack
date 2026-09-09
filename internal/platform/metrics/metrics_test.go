package metrics

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// observingWriter records output and updates the source registry during the first write.
type observingWriter struct {
	output   bytes.Buffer
	registry *Registry
	observed bool
}

// TestRegistryMirrorsSnapshotsToOpenTelemetry verifies direct push uses the existing metric names.
func TestRegistryMirrorsSnapshotsToOpenTelemetry(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})
	registry := New()
	if err := registry.AttachOpenTelemetry(provider.Meter("iapstack-test")); err != nil {
		t.Fatalf("AttachOpenTelemetry() error = %v", err)
	}
	registry.SetQueueOldestAge("inbox", 2*time.Minute)
	registry.ObserveQueueDuration("inbox", "completed", 250*time.Millisecond)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	names := make(map[string]bool)
	for _, scope := range collected.ScopeMetrics {
		for _, value := range scope.Metrics {
			names[value.Name] = true
		}
	}
	for _, expected := range []string{
		"iapstack_queue_oldest_runnable_age_seconds",
		"iapstack_queue_job_duration_seconds_count",
		"iapstack_queue_job_duration_seconds_sum",
	} {
		if !names[expected] {
			t.Errorf("OTLP collection omitted %q: %#v", expected, names)
		}
	}
}

// Write proves metric output does not hold a lock while calling an external writer.
func (writer *observingWriter) Write(payload []byte) (int, error) {
	if !writer.observed {
		writer.observed = true
		writer.registry.ObserveWebhook("during_write")
	}
	return writer.output.Write(payload)
}

// TestRegistryWritesBoundedPrometheusMetrics verifies counters, gauges, and duration summaries.
func TestRegistryWritesBoundedPrometheusMetrics(t *testing.T) {
	registry := New()
	registry.ObserveHTTP("GET", "GET /readyz", 200, 25*time.Millisecond)
	registry.ObserveProvider("huawei_appgallery", "verify", "success")
	registry.ObserveVerification("huawei_appgallery", "success")
	registry.SetQueueDepth("outbox", "pending", 3)
	registry.SetQueueOldestAge("outbox", 90*time.Second)
	registry.ObserveQueueAttempt("outbox", "completed")
	registry.ObserveQueueDuration("outbox", "completed", 125*time.Millisecond)
	registry.ObserveWebhook("delivered")

	var output bytes.Buffer
	if err := registry.WritePrometheus(&output); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	for _, expected := range []string{
		`iapstack_http_requests_total{method="GET",route="GET /readyz",status="200"} 1`,
		`iapstack_queue_depth{queue="outbox",state="pending"} 3`,
		`iapstack_queue_oldest_runnable_age_seconds{queue="outbox"} 90`,
		`iapstack_queue_job_duration_seconds_count{queue="outbox",outcome="completed"} 1`,
		`iapstack_webhook_attempts_total{outcome="delivered"} 1`,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("metrics output omitted %q\n%s", expected, output.String())
		}
	}
}

// TestRegistryReleasesLockBeforeWriting verifies a writer can safely record a concurrent metric.
func TestRegistryReleasesLockBeforeWriting(t *testing.T) {
	registry := New()
	registry.ObserveWebhook("before_write")
	writer := &observingWriter{registry: registry}
	if err := registry.WritePrometheus(writer); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}

	var output bytes.Buffer
	if err := registry.WritePrometheus(&output); err != nil {
		t.Fatalf("second WritePrometheus() error = %v", err)
	}
	if !strings.Contains(output.String(), `iapstack_webhook_attempts_total{outcome="during_write"} 1`) {
		t.Fatalf("metrics output omitted writer-time observation\n%s", output.String())
	}
}

// TestRegistryBoundsUnsupportedHTTPMethods verifies distinct unsupported methods share one series.
func TestRegistryBoundsUnsupportedHTTPMethods(t *testing.T) {
	t.Parallel()

	registry := New()
	registry.ObserveHTTP("GET", "GET /healthz", 200, time.Millisecond)
	registry.ObserveHTTP("POST", "POST /v1/admin/api-keys", 200, time.Millisecond)
	for index := 0; index < 64; index++ {
		registry.ObserveHTTP(fmt.Sprintf("AUDIT%d", index), "unmatched", 405, time.Millisecond)
	}
	registry.ObserveHTTP(strings.Repeat("X", 2048), "unmatched", 405, time.Millisecond)

	var output bytes.Buffer
	if err := registry.WritePrometheus(&output); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	metricsOutput := output.String()
	if !strings.Contains(metricsOutput, `iapstack_http_requests_total{method="GET",route="GET /healthz",status="200"} 1`) {
		t.Fatalf("metrics output omitted allowed GET series\n%s", metricsOutput)
	}
	if !strings.Contains(metricsOutput, `iapstack_http_requests_total{method="POST",route="POST /v1/admin/api-keys",status="200"} 1`) {
		t.Fatalf("metrics output omitted allowed POST series\n%s", metricsOutput)
	}
	if !strings.Contains(metricsOutput, `iapstack_http_requests_total{method="OTHER",route="unmatched",status="405"} 65`) {
		t.Fatalf("metrics output omitted bounded OTHER series\n%s", metricsOutput)
	}
	if strings.Contains(metricsOutput, `method="AUDIT`) || strings.Contains(metricsOutput, strings.Repeat("X", 32)) {
		t.Fatalf("metrics output retained an unsupported method label\n%s", metricsOutput)
	}
	if countMetricSeries(metricsOutput, "iapstack_http_requests_total{") != 3 {
		t.Fatalf("request series count = %d, want 3\n%s", countMetricSeries(metricsOutput, "iapstack_http_requests_total{"), metricsOutput)
	}
	if countMetricSeries(metricsOutput, "iapstack_http_request_duration_seconds_count{") != 3 {
		t.Fatalf("duration series count = %d, want 3\n%s", countMetricSeries(metricsOutput, "iapstack_http_request_duration_seconds_count{"), metricsOutput)
	}
}

// countMetricSeries counts Prometheus series lines with the supplied family prefix.
func countMetricSeries(output, prefix string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}
