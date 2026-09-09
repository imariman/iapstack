// Package metrics exposes a small dependency-free Prometheus registry for IAPStack operations.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
)

const (
	// contentType is the Prometheus text exposition media type.
	contentType = "text/plain; version=0.0.4; charset=utf-8"
	// otherHTTPMethod is the single retained label for methods outside the allow-list.
	otherHTTPMethod = "OTHER"
)

// Registry stores bounded-cardinality process metrics.
type Registry struct {
	mu               sync.RWMutex
	httpRequests     map[string]uint64
	httpDurations    map[string]durationMetric
	providerCalls    map[string]uint64
	verification     map[string]uint64
	queueDepth       map[string]int64
	queueOldestAge   map[string]float64
	queueAttempts    map[string]uint64
	queueDurations   map[string]durationMetric
	webhookAttempts  map[string]uint64
	otelRegistration otelmetric.Registration
}

// otelMetrics contains the exact metric families mirrored to a direct OTLP exporter.
type otelMetrics struct {
	httpRequests       otelmetric.Float64ObservableGauge
	httpDurationCount  otelmetric.Float64ObservableGauge
	httpDurationSum    otelmetric.Float64ObservableGauge
	providerCalls      otelmetric.Float64ObservableGauge
	verification       otelmetric.Float64ObservableGauge
	queueDepth         otelmetric.Float64ObservableGauge
	queueOldestAge     otelmetric.Float64ObservableGauge
	queueAttempts      otelmetric.Float64ObservableGauge
	queueDurationCount otelmetric.Float64ObservableGauge
	queueDurationSum   otelmetric.Float64ObservableGauge
	webhookAttempts    otelmetric.Float64ObservableGauge
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
	queueOldestAge  map[string]float64
	queueAttempts   map[string]uint64
	queueDurations  map[string]durationMetric
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
		queueOldestAge:  make(map[string]float64),
		queueAttempts:   make(map[string]uint64),
		queueDurations:  make(map[string]durationMetric),
		webhookAttempts: make(map[string]uint64),
	}
}

// ContentType returns the Prometheus exposition media type.
func ContentType() string {
	return contentType
}

// AttachOpenTelemetry mirrors registry snapshots to one direct OTLP meter without another process.
func (registry *Registry) AttachOpenTelemetry(meter otelmetric.Meter) error {
	if meter == nil {
		return errors.New("OpenTelemetry meter is required")
	}
	registry.mu.RLock()
	alreadyAttached := registry.otelRegistration != nil
	registry.mu.RUnlock()
	if alreadyAttached {
		return errors.New("OpenTelemetry meter is already attached")
	}
	instruments, err := newOTelMetrics(meter)
	if err != nil {
		return err
	}
	registration, err := meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		registry.observeOpenTelemetry(observer, instruments)
		return nil
	}, instruments.observables()...)
	if err != nil {
		return fmt.Errorf("register OpenTelemetry metrics callback: %w", err)
	}
	registry.mu.Lock()
	registry.otelRegistration = registration
	registry.mu.Unlock()
	return nil
}

// ObserveHTTP records one HTTP request using a normalized method, route, and status class.
func (registry *Registry) ObserveHTTP(method, route string, status int, elapsed time.Duration) {
	key := labels(normalizeHTTPMethod(method), route, fmt.Sprintf("%d", status))
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

// SetQueueOldestAge updates the age in seconds of one queue's oldest runnable job.
func (registry *Registry) SetQueueOldestAge(queue string, age time.Duration) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.queueOldestAge[labels(queue)] = max(age.Seconds(), 0)
}

// ObserveQueueAttempt records one durable queue attempt result.
func (registry *Registry) ObserveQueueAttempt(queue, outcome string) {
	registry.increment(registry.queueAttempts, labels(queue, outcome))
}

// ObserveQueueDuration records one durable job attempt duration and bounded outcome.
func (registry *Registry) ObserveQueueDuration(queue, outcome string, elapsed time.Duration) {
	key := labels(queue, outcome)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	duration := registry.queueDurations[key]
	duration.count++
	duration.sum += elapsed.Seconds()
	registry.queueDurations[key] = duration
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
	if err := writeDurations(writer, "iapstack_http_request_duration_seconds", []string{"method", "route", "status"}, snapshot.httpDurations); err != nil {
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
	if err := writeFloatGauge(writer, "iapstack_queue_oldest_runnable_age_seconds", []string{"queue"}, snapshot.queueOldestAge); err != nil {
		return err
	}
	if err := writeCounter(writer, "iapstack_queue_attempts_total", []string{"queue", "outcome"}, snapshot.queueAttempts); err != nil {
		return err
	}
	if err := writeDurations(writer, "iapstack_queue_job_duration_seconds", []string{"queue", "outcome"}, snapshot.queueDurations); err != nil {
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
		queueOldestAge:  maps.Clone(registry.queueOldestAge),
		queueAttempts:   maps.Clone(registry.queueAttempts),
		queueDurations:  maps.Clone(registry.queueDurations),
		webhookAttempts: maps.Clone(registry.webhookAttempts),
	}
}

// newOTelMetrics constructs exact-name observable gauges for the existing cumulative registry.
func newOTelMetrics(meter otelmetric.Meter) (*otelMetrics, error) {
	type metricDefinition struct {
		name        string
		description string
		assign      func(otelmetric.Float64ObservableGauge)
	}
	instruments := &otelMetrics{}
	definitions := []metricDefinition{
		{name: "iapstack_http_requests_total", description: "Cumulative HTTP requests.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.httpRequests = value }},
		{name: "iapstack_http_request_duration_seconds_count", description: "Cumulative HTTP duration observations.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.httpDurationCount = value }},
		{name: "iapstack_http_request_duration_seconds_sum", description: "Cumulative HTTP duration seconds.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.httpDurationSum = value }},
		{name: "iapstack_provider_calls_total", description: "Cumulative store provider calls.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.providerCalls = value }},
		{name: "iapstack_verifications_total", description: "Cumulative purchase verifications.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.verification = value }},
		{name: "iapstack_queue_depth", description: "Current durable queue depth by state.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.queueDepth = value }},
		{name: "iapstack_queue_oldest_runnable_age_seconds", description: "Age of the oldest runnable durable job.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.queueOldestAge = value }},
		{name: "iapstack_queue_attempts_total", description: "Cumulative durable queue attempts.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.queueAttempts = value }},
		{name: "iapstack_queue_job_duration_seconds_count", description: "Cumulative durable job duration observations.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.queueDurationCount = value }},
		{name: "iapstack_queue_job_duration_seconds_sum", description: "Cumulative durable job duration seconds.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.queueDurationSum = value }},
		{name: "iapstack_webhook_attempts_total", description: "Cumulative webhook delivery attempts.", assign: func(value otelmetric.Float64ObservableGauge) { instruments.webhookAttempts = value }},
	}
	for _, definition := range definitions {
		instrument, err := meter.Float64ObservableGauge(definition.name, otelmetric.WithDescription(definition.description))
		if err != nil {
			return nil, fmt.Errorf("construct OpenTelemetry metric %s: %w", definition.name, err)
		}
		definition.assign(instrument)
	}
	return instruments, nil
}

// observables returns every OTLP instrument that the shared callback may observe.
func (instruments *otelMetrics) observables() []otelmetric.Observable {
	return []otelmetric.Observable{
		instruments.httpRequests,
		instruments.httpDurationCount,
		instruments.httpDurationSum,
		instruments.providerCalls,
		instruments.verification,
		instruments.queueDepth,
		instruments.queueOldestAge,
		instruments.queueAttempts,
		instruments.queueDurationCount,
		instruments.queueDurationSum,
		instruments.webhookAttempts,
	}
}

// observeOpenTelemetry writes one internally consistent registry snapshot to the OTLP callback.
func (registry *Registry) observeOpenTelemetry(observer otelmetric.Observer, instruments *otelMetrics) {
	snapshot := registry.snapshot()
	observeUintMap(observer, instruments.httpRequests, []string{"method", "route", "status"}, snapshot.httpRequests)
	observeDurationMap(observer, instruments.httpDurationCount, instruments.httpDurationSum, []string{"method", "route", "status"}, snapshot.httpDurations)
	observeUintMap(observer, instruments.providerCalls, []string{"provider", "operation", "outcome"}, snapshot.providerCalls)
	observeUintMap(observer, instruments.verification, []string{"provider", "outcome"}, snapshot.verification)
	observeIntMap(observer, instruments.queueDepth, []string{"queue", "state"}, snapshot.queueDepth)
	observeFloatMap(observer, instruments.queueOldestAge, []string{"queue"}, snapshot.queueOldestAge)
	observeUintMap(observer, instruments.queueAttempts, []string{"queue", "outcome"}, snapshot.queueAttempts)
	observeDurationMap(observer, instruments.queueDurationCount, instruments.queueDurationSum, []string{"queue", "outcome"}, snapshot.queueDurations)
	observeUintMap(observer, instruments.webhookAttempts, []string{"outcome"}, snapshot.webhookAttempts)
}

// observeUintMap records cumulative unsigned values as exact-name OTLP gauges.
func observeUintMap(observer otelmetric.Observer, instrument otelmetric.Float64ObservableGauge, names []string, values map[string]uint64) {
	for key, value := range values {
		observer.ObserveFloat64(instrument, float64(value), otelmetric.WithAttributes(metricAttributes(names, key)...))
	}
}

// observeIntMap records signed gauge values with stable attributes.
func observeIntMap(observer otelmetric.Observer, instrument otelmetric.Float64ObservableGauge, names []string, values map[string]int64) {
	for key, value := range values {
		observer.ObserveFloat64(instrument, float64(value), otelmetric.WithAttributes(metricAttributes(names, key)...))
	}
}

// observeFloatMap records floating-point gauge values with stable attributes.
func observeFloatMap(observer otelmetric.Observer, instrument otelmetric.Float64ObservableGauge, names []string, values map[string]float64) {
	for key, value := range values {
		observer.ObserveFloat64(instrument, value, otelmetric.WithAttributes(metricAttributes(names, key)...))
	}
}

// observeDurationMap records cumulative duration counts and sums as exact-name OTLP gauges.
func observeDurationMap(observer otelmetric.Observer, countInstrument, sumInstrument otelmetric.Float64ObservableGauge, names []string, values map[string]durationMetric) {
	for key, value := range values {
		options := otelmetric.WithAttributes(metricAttributes(names, key)...)
		observer.ObserveFloat64(countInstrument, float64(value.count), options)
		observer.ObserveFloat64(sumInstrument, value.sum, options)
	}
}

// metricAttributes maps one internal bounded label key to OpenTelemetry attributes.
func metricAttributes(names []string, key string) []attribute.KeyValue {
	values := strings.Split(key, "\x00")
	attributes := make([]attribute.KeyValue, 0, len(names))
	for index, name := range names {
		value := ""
		if index < len(values) {
			value = values[index]
		}
		attributes = append(attributes, attribute.String(name, value))
	}
	return attributes
}

// normalizeHTTPMethod maps client-controlled methods onto a fixed allow-list.
func normalizeHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return method
	default:
		return otherHTTPMethod
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

// writeFloatGauge emits one floating-point gauge family in stable key order.
func writeFloatGauge(writer io.Writer, name string, labelNames []string, values map[string]float64) error {
	if _, err := fmt.Fprintf(writer, "# TYPE %s gauge\n", name); err != nil {
		return err
	}
	for _, key := range sortedKeys(values) {
		if _, err := fmt.Fprintf(writer, "%s%s %g\n", name, formatLabels(labelNames, key), values[key]); err != nil {
			return err
		}
	}
	return nil
}

// writeDurations emits count and sum series for one duration observation family.
func writeDurations(writer io.Writer, name string, labelNames []string, values map[string]durationMetric) error {
	if _, err := fmt.Fprintf(writer, "# TYPE %s summary\n", name); err != nil {
		return err
	}
	for _, key := range sortedKeys(values) {
		value := values[key]
		labelsValue := formatLabels(labelNames, key)
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
