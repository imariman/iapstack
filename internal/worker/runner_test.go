package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/platform/metrics"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/jackc/pgx/v5/pgxpool"
)

// executorStore runs callbacks against one deterministic operations transaction.
type executorStore struct {
	transaction *executorTransaction
}

// executorTransaction records terminal audit writes for one durable message.
type executorTransaction struct {
	persistence.OperationsTransaction
	message       persistence.QueueMessage
	completions   []persistence.QueueCompletion
	failures      []persistence.QueueFailure
	purgeCutoff   time.Time
	purgeLimit    int
	purgeDeleted  int64
	purgeError    error
	loadError     error
	completeError error
	failError     error
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
	if transaction.loadError != nil {
		return persistence.QueueMessage{}, transaction.loadError
	}
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
	if transaction.completeError != nil {
		return transaction.completeError
	}
	transaction.completions = append(transaction.completions, completion)
	return nil
}

// FailQueue records one failed terminal audit outcome.
func (transaction *executorTransaction) FailQueue(_ context.Context, failure persistence.QueueFailure) error {
	if transaction.failError != nil {
		return transaction.failError
	}
	transaction.failures = append(transaction.failures, failure)
	return nil
}

// TestNewValidatesWorkerDependenciesAndConfiguration verifies construction fails before River starts.
func TestNewValidatesWorkerDependenciesAndConfiguration(t *testing.T) {
	t.Parallel()

	pool := &pgxpool.Pool{}
	store := &executorStore{transaction: &executorTransaction{}}
	config := Config{
		WorkerID: "worker-1", PollInterval: time.Second, JobTimeout: time.Minute,
		ShutdownTimeout: time.Second, Concurrency: 1, MaxAttempts: 3, QueueRetention: time.Hour,
	}
	handler := HandlerFunc(func(context.Context, persistence.QueueMessage) error { return nil })
	handlers := map[persistence.QueueName]Handler{
		persistence.QueueInbox: handler, persistence.QueueOutbox: handler,
		persistence.QueueReconciliation: handler,
	}
	tests := []struct {
		name     string
		pool     *pgxpool.Pool
		store    persistence.OperationsStore
		config   Config
		handlers map[persistence.QueueName]Handler
		wantCode string
	}{
		{name: "missing pool", store: store, config: config, handlers: handlers, wantCode: "pool is required"},
		{name: "missing store", pool: pool, config: config, handlers: handlers, wantCode: "store is required"},
		{name: "invalid configuration", pool: pool, store: store, handlers: handlers, wantCode: "configuration is invalid"},
		{name: "missing handler", pool: pool, store: store, config: config, handlers: map[persistence.QueueName]Handler{}, wantCode: "handler for inbox is required"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(test.pool, test.store, test.config, test.handlers, nil, nil)
			if err == nil || !strings.Contains(err.Error(), test.wantCode) {
				t.Fatalf("New() error = %v, want %q", err, test.wantCode)
			}
		})
	}

	runner, err := New(pool, store, config, handlers, nil, nil)
	if err != nil || runner == nil {
		t.Fatalf("New() = (%#v, %v), want initialized runner", runner, err)
	}
}

// TestRunRejectsUninitializedRunner verifies lifecycle calls fail without a River client.
func TestRunRejectsUninitializedRunner(t *testing.T) {
	t.Parallel()

	for _, runner := range []*Runner{nil, {}} {
		if err := runner.Run(context.Background()); err == nil {
			t.Fatal("Run() uninitialized error = nil")
		}
	}
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

// TestExecutorHandlesLoadAndTerminalStates verifies handlers run only for active durable records.
func TestExecutorHandlesLoadAndTerminalStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		transaction *executorTransaction
		wantError   string
	}{
		{name: "missing record", transaction: &executorTransaction{loadError: persistence.ErrNotFound}, wantError: "queue_record_not_found"},
		{name: "storage failure", transaction: &executorTransaction{loadError: persistence.ErrUnavailable}, wantError: "queue_record_load_failed"},
		{name: "completed record", transaction: &executorTransaction{message: persistence.QueueMessage{Completed: true}}},
		{name: "failed record", transaction: &executorTransaction{message: persistence.QueueMessage{Failed: true}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			handlerCalls := 0
			execution := newTestExecutor(test.transaction, HandlerFunc(func(context.Context, persistence.QueueMessage) error {
				handlerCalls++
				return nil
			}))
			err := execution.execute(context.Background(), persistence.QueueInbox, "message-1", 1, 3)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("execute() error = %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("execute() error = %v, want %q", err, test.wantError)
			}
			if handlerCalls != 0 {
				t.Fatalf("handler calls = %d, want 0", handlerCalls)
			}
		})
	}
}

// TestExecutorHandlesCancellationAndExhaustedRetries verifies interruption and final retry semantics.
func TestExecutorHandlesCancellationAndExhaustedRetries(t *testing.T) {
	t.Parallel()

	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	cancelledTransaction := &executorTransaction{}
	cancelledExecution := newTestExecutor(
		cancelledTransaction,
		HandlerFunc(func(context.Context, persistence.QueueMessage) error { return context.Canceled }),
	)
	if err := cancelledExecution.execute(cancelledContext, persistence.QueueInbox, "message-1", 1, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("execute() cancellation error = %v", err)
	}
	if len(cancelledTransaction.failures) != 0 {
		t.Fatalf("cancellation failures = %#v, want none", cancelledTransaction.failures)
	}

	exhaustedTransaction := &executorTransaction{}
	exhaustedExecution := newTestExecutor(
		exhaustedTransaction,
		HandlerFunc(func(context.Context, persistence.QueueMessage) error {
			return stores.NewFailure("google_play", "verify", stores.FailureTemporary, 0, nil)
		}),
	)
	err := exhaustedExecution.execute(context.Background(), persistence.QueueInbox, "message-1", 3, 3)
	if err == nil || !strings.Contains(err.Error(), "provider_temporary") {
		t.Fatalf("execute() exhausted retry error = %v", err)
	}
	if len(exhaustedTransaction.failures) != 1 ||
		exhaustedTransaction.failures[0].ErrorCode != "provider_temporary" {
		t.Fatalf("exhausted retry failures = %#v", exhaustedTransaction.failures)
	}
}

// TestExecutorSurfacesTerminalTransitionFailures verifies storage errors remain retryable to River.
func TestExecutorSurfacesTerminalTransitionFailures(t *testing.T) {
	t.Parallel()

	completionTransaction := &executorTransaction{completeError: persistence.ErrUnavailable}
	completionExecution := newTestExecutor(
		completionTransaction,
		HandlerFunc(func(context.Context, persistence.QueueMessage) error { return nil }),
	)
	if err := completionExecution.execute(context.Background(), persistence.QueueInbox, "message-1", 1, 3); err == nil || err.Error() != "queue_transition_failed" {
		t.Fatalf("execute() completion transition error = %v", err)
	}

	failureTransaction := &executorTransaction{failError: persistence.ErrUnavailable}
	failureExecution := newTestExecutor(
		failureTransaction,
		HandlerFunc(func(context.Context, persistence.QueueMessage) error {
			return stores.NewFailure("google_play", "verify", stores.FailureInvalidEvidence, 0, nil)
		}),
	)
	if err := failureExecution.execute(context.Background(), persistence.QueueInbox, "message-1", 1, 3); err == nil || err.Error() != "queue_transition_failed" {
		t.Fatalf("execute() failure transition error = %v", err)
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

// TestQueueMappingsAndSafeCodesCoverClosedVocabularies verifies labels cannot grow from arbitrary input.
func TestQueueMappingsAndSafeCodesCoverClosedVocabularies(t *testing.T) {
	t.Parallel()

	wantQueues := []persistence.QueueName{
		persistence.QueueInbox,
		persistence.QueueReconciliation,
		persistence.QueueOutbox,
	}
	if got := durableQueues(); !reflect.DeepEqual(got, wantQueues) {
		t.Fatalf("durableQueues() = %#v, want %#v", got, wantQueues)
	}
	for _, test := range []struct {
		name  string
		queue string
		want  persistence.QueueName
		ok    bool
	}{
		{name: "inbox", queue: jobs.QueueInbox, want: persistence.QueueInbox, ok: true},
		{name: "outbox", queue: jobs.QueueOutbox, want: persistence.QueueOutbox, ok: true},
		{name: "reconciliation", queue: jobs.QueueReconciliation, want: persistence.QueueReconciliation, ok: true},
		{name: "unknown", queue: "dynamic-provider-queue"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, ok := persistenceQueue(test.queue)
			if got != test.want || ok != test.ok {
				t.Fatalf("persistenceQueue(%q) = (%q, %t), want (%q, %t)", test.queue, got, ok, test.want, test.ok)
			}
		})
	}
	if got := errorCode(context.DeadlineExceeded); got != "deadline_exceeded" {
		t.Fatalf("errorCode(deadline) = %q", got)
	}
	for _, code := range []string{"", "UPPERCASE", "contains-hyphen", strings.Repeat("a", 65)} {
		if got := safeErrorCode(code); got != "handler_failed" {
			t.Fatalf("safeErrorCode(%q) = %q", code, got)
		}
	}
	if got := safeErrorCode("provider_temporary_2"); got != "provider_temporary_2" {
		t.Fatalf("safeErrorCode(valid) = %q", got)
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
