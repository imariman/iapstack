package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/platform/metrics"
	"github.com/imariman/iapstack/internal/stores"
)

// maintenanceStore executes queue maintenance against one deterministic transaction fake.
type maintenanceStore struct {
	transaction *maintenanceTransaction
}

// maintenanceTransaction records prune requests and returns configured queue depths.
type maintenanceTransaction struct {
	persistence.OperationsTransaction
	depths  map[persistence.QueueName]map[string]int64
	pruned  []persistence.QueuePrune
	failFor persistence.QueueName
}

// Operate executes one fake operations transaction callback.
func (store *maintenanceStore) Operate(_ context.Context, operation persistence.OperationsFunc) error {
	return operation(store.transaction)
}

// PruneQueue records one bounded retention request or returns the configured safe failure.
func (transaction *maintenanceTransaction) PruneQueue(_ context.Context, prune persistence.QueuePrune) (int64, error) {
	transaction.pruned = append(transaction.pruned, prune)
	if prune.Queue == transaction.failFor {
		return 0, errors.New("maintenance unavailable")
	}
	return 1, nil
}

// QueueDepth returns one defensive copy of configured state counts.
func (transaction *maintenanceTransaction) QueueDepth(_ context.Context, queue persistence.QueueName) (map[string]int64, error) {
	depth := make(map[string]int64, len(transaction.depths[queue]))
	for state, count := range transaction.depths[queue] {
		depth[state] = count
	}
	return depth, nil
}

// TestRetryClassificationUsesProviderContract verifies transient and permanent durable outcomes.
func TestRetryClassificationUsesProviderContract(t *testing.T) {
	temporary := stores.NewFailure("huawei_appgallery", "verify", stores.FailureTemporary, 0, context.DeadlineExceeded)
	if !isRetryable(temporary) {
		t.Fatal("temporary provider failure was not retryable")
	}
	invalid := stores.NewFailure("huawei_appgallery", "verify", stores.FailureInvalidEvidence, 0, nil)
	if isRetryable(invalid) {
		t.Fatal("invalid evidence failure was retryable")
	}
	if got := errorCode(temporary); got != "provider_temporary" {
		t.Fatalf("errorCode() = %q, want provider_temporary", got)
	}
}

// TestBackoffIsBounded verifies full jitter remains inside the exponential operational ceiling.
func TestBackoffIsBounded(t *testing.T) {
	for attempt := 1; attempt <= 32; attempt++ {
		delay := backoff(attempt)
		if delay < 0 || delay > maximumBackoff {
			t.Fatalf("backoff(%d) = %v, want [0,%v]", attempt, delay, maximumBackoff)
		}
	}
	if delay := backoff(1); delay > time.Second {
		t.Fatalf("backoff(1) = %v, want at most 1s", delay)
	}
}

// TestMaintenanceRunsOutsideClaimsAndResetsAbsentDepthStates verifies best-effort cleanup metrics.
func TestMaintenanceRunsOutsideClaimsAndResetsAbsentDepthStates(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	transaction := &maintenanceTransaction{
		depths: map[persistence.QueueName]map[string]int64{
			persistence.QueueInbox:          {"pending": 2},
			persistence.QueueReconciliation: {"processed": 3},
			persistence.QueueOutbox:         {"delivered": 4},
		},
		failFor: persistence.QueueReconciliation,
	}
	registry := metrics.New()
	registry.SetQueueDepth(string(persistence.QueueInbox), "failed", 9)
	runner := &Runner{
		store: &maintenanceStore{transaction: transaction},
		config: Config{
			MaintenanceInterval: time.Minute,
			QueueRetention:      30 * 24 * time.Hour,
			PruneBatchSize:      25,
		},
		metrics: registry,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	runner.maintain(context.Background(), now)
	if len(transaction.pruned) != 3 {
		t.Fatalf("prune calls = %d, want 3", len(transaction.pruned))
	}
	for _, prune := range transaction.pruned {
		if prune.Before != now.Add(-30*24*time.Hour) || prune.Limit != 25 {
			t.Fatalf("prune request = %#v, want configured boundary and limit", prune)
		}
	}
	var output bytes.Buffer
	if err := registry.WritePrometheus(&output); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	if !strings.Contains(output.String(), `iapstack_queue_depth{queue="inbox",state="failed"} 0`) {
		t.Fatalf("queue metrics did not reset absent state:\n%s", output.String())
	}
	if !strings.Contains(output.String(), `iapstack_queue_depth{queue="outbox",state="delivered"} 4`) {
		t.Fatalf("queue metrics did not publish maintained depth:\n%s", output.String())
	}
	if strings.Contains(output.String(), `queue="reconciliation"`) {
		t.Fatalf("failed queue maintenance changed metrics:\n%s", output.String())
	}

	runner.maintain(context.Background(), now.Add(30*time.Second))
	if len(transaction.pruned) != 3 {
		t.Fatalf("maintenance ran before interval; prune calls = %d, want 3", len(transaction.pruned))
	}
}
