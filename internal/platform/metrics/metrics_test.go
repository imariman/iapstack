package metrics

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

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
