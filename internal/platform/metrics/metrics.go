// Package metrics exposes a small dependency-free Prometheus registry for IAPStack operations.
package metrics

import (
	"fmt"
	"io"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// contentType is the Prometheus text exposition media type.
	contentType = "text/plain; version=0.0.4; charset=utf-8"
)

// Registry stores bounded-cardinality process metrics.
type Registry struct {
	mu              sync.RWMutex
	httpRequests    map[string]uint64
	httpDurations   map[string]durationMetric
	providerCalls   map[string]uint64
	verification    map[string]uint64
	queueDepth      map[string]int64
	queueAttempts   map[string]uint64
	webhookAttempts map[string]uint64
}

// durationMetric stores aggregate seconds and observation count.
type durationMetric struct {
	count uint64
	sum   float64
}

// registrySnapshot owns immutable map copies that can be written without holding the registry lock.
type registrySnapshot struct {
	httpRequests    map[string]uint64
	httpDurations   map[string]durationMetric
	providerCalls   map[string]uint64
	verification    map[string]uint64
	queueDepth      map[string]int64
	queueAttempts   map[string]uint64
	webhookAttempts map[string]uint64
}

// New constructs an empty operational metrics registry.
func New() *Registry {
	return &Registry{
		httpRequests:    make(map[string]uint64),
		httpDurations:   make(map[string]durationMetric),
		providerCalls:   make(map[string]uint64),
		verification:    make(map[string]uint64),
		queueDepth:      make(map[string]int64),
		queueAttempts:   make(map[string]uint64),
		webhookAttempts: make(map[string]uint64),
	}
}

// ContentType returns the Prometheus exposition media type.
func ContentType() string {
	return contentType
}

// ObserveHTTP records one HTTP request using a normalized route and status class.
func (registry *Registry) ObserveHTTP(method, route string, status int, elapsed time.Duration) {
	key := labels(method, route, fmt.Sprintf("%d", status))
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.httpRequests[key]++
	duration := registry.httpDurations[key]
	duration.count++
	duration.sum += elapsed.Seconds()
	registry.httpDurations[key] = duration
}

// ObserveProvider records one provider call outcome.
func (registry *Registry) ObserveProvider(provider, operation, outcome string) {
	registry.increment(registry.providerCalls, labels(provider, operation, outcome))
}

// ObserveVerification records one provider-neutral verification outcome.
func (registry *Registry) ObserveVerification(provider, outcome string) {
	registry.increment(registry.verification, labels(provider, outcome))
}

// SetQueueDepth updates one queue and state gauge.
func (registry *Registry) SetQueueDepth(queue, state string, depth int64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.queueDepth[labels(queue, state)] = depth
}

// ObserveQueueAttempt records one durable queue attempt result.
func (registry *Registry) ObserveQueueAttempt(queue, outcome string) {
	registry.increment(registry.queueAttempts, labels(queue, outcome))
}

// ObserveWebhook records one application webhook delivery result.
func (registry *Registry) ObserveWebhook(outcome string) {
	registry.increment(registry.webhookAttempts, labels(outcome))
}

// WritePrometheus writes one internally consistent text snapshot.
func (registry *Registry) WritePrometheus(writer io.Writer) error {
	snapshot := registry.snapshot()

	if err := writeCounter(writer, "iapstack_http_requests_total", []string{"method", "route", "status"}, snapshot.httpRequests); err != nil {
		return err
	}
	if err := writeDurations(writer, snapshot.httpDurations); err != nil {
		return err
	}
	if err := writeCounter(writer, "iapstack_provider_calls_total", []string{"provider", "operation", "outcome"}, snapshot.providerCalls); err != nil {
		return err
	}
	if err := writeCounter(writer, "iapstack_verifications_total", []string{"provider", "outcome"}, snapshot.verification); err != nil {
		return err
	}
	if err := writeGauge(writer, "iapstack_queue_depth", []string{"queue", "state"}, snapshot.queueDepth); err != nil {
		return err
	}
	if err := writeCounter(writer, "iapstack_queue_attempts_total", []string{"queue", "outcome"}, snapshot.queueAttempts); err != nil {
		return err
	}
	return writeCounter(writer, "iapstack_webhook_attempts_total", []string{"outcome"}, snapshot.webhookAttempts)
}

// increment adds one to a counter map while holding the registry lock.
func (registry *Registry) increment(values map[string]uint64, key string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	values[key]++
}

// snapshot copies every bounded metric map while holding the read lock briefly.
func (registry *Registry) snapshot() registrySnapshot {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return registrySnapshot{
		httpRequests:    maps.Clone(registry.httpRequests),
		httpDurations:   maps.Clone(registry.httpDurations),
		providerCalls:   maps.Clone(registry.providerCalls),
		verification:    maps.Clone(registry.verification),
		queueDepth:      maps.Clone(registry.queueDepth),
		queueAttempts:   maps.Clone(registry.queueAttempts),
		webhookAttempts: maps.Clone(registry.webhookAttempts),
	}
}

// labels encodes already-bounded label values without exposing arbitrary user input.
func labels(values ...string) string {
	return strings.Join(values, "\x00")
}

// writeCounter emits one counter family in stable key order.
func writeCounter(writer io.Writer, name string, labelNames []string, values map[string]uint64) error {
	if _, err := fmt.Fprintf(writer, "# TYPE %s counter\n", name); err != nil {
		return err
	}
	for _, key := range sortedKeys(values) {
		if _, err := fmt.Fprintf(writer, "%s%s %d\n", name, formatLabels(labelNames, key), values[key]); err != nil {
			return err
		}
	}
	return nil
}

// writeGauge emits one gauge family in stable key order.
func writeGauge(writer io.Writer, name string, labelNames []string, values map[string]int64) error {
	if _, err := fmt.Fprintf(writer, "# TYPE %s gauge\n", name); err != nil {
		return err
	}
	for _, key := range sortedKeys(values) {
		if _, err := fmt.Fprintf(writer, "%s%s %d\n", name, formatLabels(labelNames, key), values[key]); err != nil {
			return err
		}
	}
	return nil
}

// writeDurations emits count and sum series for request duration observations.
func writeDurations(writer io.Writer, values map[string]durationMetric) error {
	const name = "iapstack_http_request_duration_seconds"
	if _, err := fmt.Fprintf(writer, "# TYPE %s summary\n", name); err != nil {
		return err
	}
	for _, key := range sortedKeys(values) {
		value := values[key]
		labelsValue := formatLabels([]string{"method", "route", "status"}, key)
		if _, err := fmt.Fprintf(writer, "%s_count%s %d\n%s_sum%s %g\n", name, labelsValue, value.count, name, labelsValue, value.sum); err != nil {
			return err
		}
	}
	return nil
}

// sortedKeys returns stable map keys for deterministic exposition and tests.
func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// formatLabels converts one internal label key to escaped Prometheus syntax.
func formatLabels(names []string, key string) string {
	values := strings.Split(key, "\x00")
	pairs := make([]string, 0, len(names))
	for index, name := range names {
		value := ""
		if index < len(values) {
			value = values[index]
		}
		value = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"").Replace(value)
		pairs = append(pairs, fmt.Sprintf("%s=\"%s\"", name, value))
	}
	return "{" + strings.Join(pairs, ",") + "}"
}
