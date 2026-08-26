package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/platform/metrics"
	"github.com/imariman/iapstack/internal/stores"
)

// executorStore runs callbacks against one deterministic operations transaction.
type executorStore struct {
	transaction *executorTransaction
}

// executorTransaction records terminal audit writes for one durable message.
type executorTransaction struct {
	persistence.OperationsTransaction
	message      persistence.QueueMessage
	completions  []persistence.QueueCompletion
	failures     []persistence.QueueFailure
	purgeCutoff  time.Time
	purgeLimit   int
	purgeDeleted int64
	purgeError   error
}

// codedTestError supplies an arbitrary code through the worker error contract.
type codedTestError struct {
	code string
}

// Operate executes one fake operations transaction callback.
func (store *executorStore) Operate(_ context.Context, operation persistence.OperationsFunc) error {
	return operation(store.transaction)
}

// QueueMessage returns the configured durable message.
func (transaction *executorTransaction) QueueMessage(
	_ context.Context,
	queue persistence.QueueName,
	id string,
) (persistence.QueueMessage, error) {
	message := transaction.message
	message.Queue = queue
	message.ID = id
	return message, nil
}

// CompleteQueue records one successful terminal audit outcome.
func (transaction *executorTransaction) CompleteQueue(
	_ context.Context,
	completion persistence.QueueCompletion,
) error {
	transaction.completions = append(transaction.completions, completion)
	return nil
}

// FailQueue records one failed terminal audit outcome.
func (transaction *executorTransaction) FailQueue(_ context.Context, failure persistence.QueueFailure) error {
	transaction.failures = append(transaction.failures, failure)
	return nil
}

// PurgeTerminalQueueRecords records the retention cutoff and returns the configured result.
func (transaction *executorTransaction) PurgeTerminalQueueRecords(
	_ context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	transaction.purgeCutoff = before
	transaction.purgeLimit = limit
	return transaction.purgeDeleted, transaction.purgeError
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

// TestErrorCodeRejectsUnsafeMetadata verifies handler-provided values cannot reach logs or rows unchecked.
func TestErrorCodeRejectsUnsafeMetadata(t *testing.T) {
	err := codedTestError{code: "secret=credential-value"}
	if got := errorCode(err); got != "handler_failed" {
		t.Fatalf("errorCode() = %q, want handler_failed", got)
	}
}

// TestExecutorRecordsSuccessfulCompletion verifies River work commits its audit result after handling.
func TestExecutorRecordsSuccessfulCompletion(t *testing.T) {
	transaction := &executorTransaction{}
	execution := newTestExecutor(transaction, HandlerFunc(func(context.Context, persistence.QueueMessage) error {
		return nil
	}))
	if err := execution.execute(context.Background(), persistence.QueueOutbox, "event-1", 1, 3); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if len(transaction.completions) != 1 || len(transaction.failures) != 0 {
		t.Fatalf("terminal outcomes = (%d completions, %d failures), want (1, 0)",
			len(transaction.completions), len(transaction.failures))
	}
}

// TestExecutorLeavesRetryableAttemptToRiver verifies transient work is not marked terminal early.
func TestExecutorLeavesRetryableAttemptToRiver(t *testing.T) {
	transaction := &executorTransaction{}
	execution := newTestExecutor(transaction, HandlerFunc(func(context.Context, persistence.QueueMessage) error {
		return stores.NewFailure("huawei_appgallery", "verify", stores.FailureTemporary, 0, nil)
	}))
	if err := execution.execute(context.Background(), persistence.QueueInbox, "message-1", 1, 3); err == nil {
		t.Fatal("execute() error = nil, want River retry signal")
	}
	if len(transaction.completions) != 0 || len(transaction.failures) != 0 {
		t.Fatalf("terminal outcomes = (%d completions, %d failures), want none",
			len(transaction.completions), len(transaction.failures))
	}
}

// TestExecutorRecordsPermanentFailure verifies a non-retryable handler result becomes terminal.
func TestExecutorRecordsPermanentFailure(t *testing.T) {
	transaction := &executorTransaction{}
	execution := newTestExecutor(transaction, HandlerFunc(func(context.Context, persistence.QueueMessage) error {
		return stores.NewFailure("huawei_appgallery", "verify", stores.FailureInvalidEvidence, 0, nil)
	}))
	if err := execution.execute(context.Background(), persistence.QueueReconciliation, "job-1", 1, 3); err == nil {
		t.Fatal("execute() error = nil, want River cancellation signal")
	}
	if len(transaction.failures) != 1 || transaction.failures[0].ErrorCode != "provider_invalid_evidence" {
		t.Fatalf("failures = %#v, want one safe permanent outcome", transaction.failures)
	}
}

// TestRunnerPurgesTerminalRecordsAtRetentionCutoff verifies maintenance uses the configured UTC boundary.
func TestRunnerPurgesTerminalRecordsAtRetentionCutoff(t *testing.T) {
	now := time.Date(2026, time.August, 26, 14, 30, 0, 0, time.FixedZone("test", 3*60*60))
	transaction := &executorTransaction{purgeDeleted: 3}
	runner := &Runner{
		store:          &executorStore{transaction: transaction},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		queueRetention: 30 * 24 * time.Hour,
		clock:          func() time.Time { return now },
	}
	if err := runner.purgeTerminalQueueRecords(context.Background()); err != nil {
		t.Fatalf("purgeTerminalQueueRecords() error = %v", err)
	}
	want := now.UTC().Add(-30 * 24 * time.Hour)
	if !transaction.purgeCutoff.Equal(want) {
		t.Fatalf("purge cutoff = %v, want %v", transaction.purgeCutoff, want)
	}
	if transaction.purgeLimit != queueCleanupBatchSize {
		t.Fatalf("purge limit = %d, want %d", transaction.purgeLimit, queueCleanupBatchSize)
	}
}

// TestRunnerReportsTerminalRecordPurgeFailure verifies cleanup failures remain observable to the caller.
func TestRunnerReportsTerminalRecordPurgeFailure(t *testing.T) {
	expected := errors.New("database unavailable")
	transaction := &executorTransaction{purgeError: expected}
	runner := &Runner{
		store:          &executorStore{transaction: transaction},
		logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		queueRetention: time.Hour,
		clock:          time.Now,
	}
	if err := runner.purgeTerminalQueueRecords(context.Background()); !errors.Is(err, expected) {
		t.Fatalf("purgeTerminalQueueRecords() error = %v, want wrapped failure", err)
	}
}

// newTestExecutor constructs one deterministic executor around the supplied handler.
func newTestExecutor(transaction *executorTransaction, handler Handler) *executor {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	return &executor{
		store: &executorStore{transaction: transaction},
		handlers: map[persistence.QueueName]Handler{
			persistence.QueueInbox: handler, persistence.QueueOutbox: handler, persistence.QueueReconciliation: handler,
		},
		metrics: metrics.New(),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		clock:   func() time.Time { return now },
	}
}

// Error returns a fixed non-sensitive test message.
func (err codedTestError) Error() string {
	return "coded test failure"
}

// CodeValue returns the configured candidate error code.
func (err codedTestError) CodeValue() string {
	return err.code
}
