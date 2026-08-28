package metrics

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// observingWriter records output and updates the source registry during the first write.
type observingWriter struct {
	output   bytes.Buffer
	registry *Registry
	observed bool
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
	registry.ObserveQueueAttempt("outbox", "completed")
	registry.ObserveWebhook("delivered")

	var output bytes.Buffer
	if err := registry.WritePrometheus(&output); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	for _, expected := range []string{
		`iapstack_http_requests_total{method="GET",route="GET /readyz",status="200"} 1`,
		`iapstack_queue_depth{queue="outbox",state="pending"} 3`,
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
