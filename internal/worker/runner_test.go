package worker

import (
	"bytes"
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
	"github.com/imariman/iapstack/internal/webhooks"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"go.uber.org/goleak"
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

// TestMain fails the package when River lifecycle tests leave goroutines behind.
func TestMain(main *testing.M) {
	goleak.VerifyTestMain(main)
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

// TestRetryClassificationUsesExplicitContracts verifies provider and persistence retry boundaries.
func TestRetryClassificationUsesExplicitContracts(t *testing.T) {
	temporary := stores.NewFailure("huawei_appgallery", "verify", stores.FailureTemporary, 0, context.DeadlineExceeded)
	if !isRetryable(temporary) {
		t.Fatal("temporary provider failure was not retryable")
	}
	invalid := stores.NewFailure("huawei_appgallery", "verify", stores.FailureInvalidEvidence, 0, nil)
	if isRetryable(invalid) {
		t.Fatal("invalid evidence failure was retryable")
	}
	if !isRetryable(errors.Join(errors.New("persist verification"), persistence.ErrUnavailable)) {
		t.Fatal("transient persistence unavailability was not retryable")
	}
	if isRetryable(persistence.ErrConflict) {
		t.Fatal("permanent persistence identity conflict was retryable")
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
	if err := execution.execute(context.Background(), persistence.QueueOutbox, "event-1", 1, 3, 0); err != nil {
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
	if err := execution.execute(context.Background(), persistence.QueueInbox, "message-1", 1, 3, 0); err == nil {
		t.Fatal("execute() error = nil, want River retry signal")
	}
	if len(transaction.completions) != 0 || len(transaction.failures) != 0 {
		t.Fatalf("terminal outcomes = (%d completions, %d failures), want none",
			len(transaction.completions), len(transaction.failures))
	}
}

// TestExecutorRetriesUnavailableHandlerStorageForEveryQueue verifies contention never becomes an early terminal outcome.
func TestExecutorRetriesUnavailableHandlerStorageForEveryQueue(t *testing.T) {
	t.Parallel()

	queues := []persistence.QueueName{
		persistence.QueueInbox,
		persistence.QueueOutbox,
		persistence.QueueReconciliation,
	}
	for _, queue := range queues {
		queue := queue
		t.Run(string(queue), func(t *testing.T) {
			t.Parallel()

			transaction := &executorTransaction{}
			execution := newTestExecutor(transaction, HandlerFunc(func(context.Context, persistence.QueueMessage) error {
				return errors.Join(errors.New("persist verification"), persistence.ErrUnavailable)
			}))
			if err := execution.execute(context.Background(), queue, "record-1", 1, 3, 0); err == nil {
				t.Fatal("execute() error = nil, want River retry signal")
			}
			if len(transaction.completions) != 0 || len(transaction.failures) != 0 {
				t.Fatalf("terminal outcomes = (%d completions, %d failures), want none",
					len(transaction.completions), len(transaction.failures))
			}
		})
	}
}

// TestExecutorRetriesWebhookDependencyFailures verifies delivery classification prevents premature outbox cancellation.
func TestExecutorRetriesWebhookDependencyFailures(t *testing.T) {
	transaction := &executorTransaction{}
	execution := newTestExecutor(transaction, HandlerFunc(func(context.Context, persistence.QueueMessage) error {
		return &webhooks.DeliveryError{Code: "webhook_endpoint_unavailable", CanRetry: true}
	}))
	err := execution.execute(context.Background(), persistence.QueueOutbox, "event-1", 1, 3, 0)
	if err == nil || err.Error() != "webhook_endpoint_unavailable" {
		t.Fatalf("execute() error = %v, want webhook_endpoint_unavailable retry signal", err)
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
	if err := execution.execute(context.Background(), persistence.QueueReconciliation, "job-1", 1, 3, 0); err == nil {
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
			err := execution.execute(context.Background(), persistence.QueueInbox, "message-1", 1, 3, 0)
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
	if err := cancelledExecution.execute(cancelledContext, persistence.QueueInbox, "message-1", 1, 3, 0); !errors.Is(err, context.Canceled) {
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
	err := exhaustedExecution.execute(context.Background(), persistence.QueueInbox, "message-1", 3, 3, 0)
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
	if err := completionExecution.execute(context.Background(), persistence.QueueInbox, "message-1", 1, 3, 0); err == nil || err.Error() != "queue_transition_failed" {
		t.Fatalf("execute() completion transition error = %v", err)
	}

	failureTransaction := &executorTransaction{failError: persistence.ErrUnavailable}
	failureExecution := newTestExecutor(
		failureTransaction,
		HandlerFunc(func(context.Context, persistence.QueueMessage) error {
			return stores.NewFailure("google_play", "verify", stores.FailureInvalidEvidence, 0, nil)
		}),
	)
	if err := failureExecution.execute(context.Background(), persistence.QueueInbox, "message-1", 1, 3, 0); err == nil || err.Error() != "queue_transition_failed" {
		t.Fatalf("execute() failure transition error = %v", err)
	}
}

// TestExecutorSnoozesFinalAuditTransition verifies the last attempt stays runnable until the audit row commits.
func TestExecutorSnoozesFinalAuditTransition(t *testing.T) {
	t.Parallel()

	permanent := stores.NewFailure("google_play", "verify", stores.FailureInvalidEvidence, 0, nil)
	tests := []struct {
		name          string
		attempt       int
		maxAttempts   int
		completeError error
		failError     error
		handlerErr    error
		wantSnooze    bool
	}{
		{name: "final completion", attempt: 3, maxAttempts: 3, completeError: persistence.ErrUnavailable, wantSnooze: true},
		{name: "only completion attempt", attempt: 1, maxAttempts: 1, completeError: persistence.ErrUnavailable, wantSnooze: true},
		{name: "final failure", attempt: 3, maxAttempts: 3, failError: persistence.ErrUnavailable, handlerErr: permanent, wantSnooze: true},
		{name: "final success", attempt: 3, maxAttempts: 3},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			transaction := &executorTransaction{completeError: test.completeError, failError: test.failError}
			execution := newTestExecutor(transaction, HandlerFunc(func(context.Context, persistence.QueueMessage) error {
				return test.handlerErr
			}))
			err := execution.execute(context.Background(), persistence.QueueOutbox, "event-1", test.attempt, test.maxAttempts, 0)
			var snooze *river.JobSnoozeError
			if test.wantSnooze {
				if !errors.As(err, &snooze) || snooze.Duration != terminalAuditRetryDelay {
					t.Fatalf("execute() error = %v, want snooze %s", err, terminalAuditRetryDelay)
				}
				if len(transaction.completions) != 0 || len(transaction.failures) != 0 {
					t.Fatalf("terminal outcomes = (%d completions, %d failures), want none",
						len(transaction.completions), len(transaction.failures))
				}
				return
			}
			if err != nil {
				t.Fatalf("execute() error = %v, want completed audit", err)
			}
			if len(transaction.completions) != 1 || len(transaction.failures) != 0 {
				t.Fatalf("terminal outcomes = (%d completions, %d failures), want (1, 0)",
					len(transaction.completions), len(transaction.failures))
			}
		})
	}
}

// TestExecutorCapsTerminalAuditSnoozes verifies a stuck audit write stops retrying and does not call the handler again.
func TestExecutorCapsTerminalAuditSnoozes(t *testing.T) {
	t.Parallel()

	secret := "db-secret-do-not-log"
	tests := []struct {
		name         string
		snoozes      int
		wantSnooze   bool
		wantLevel    string
		wantCode     string
		wantHandlers int
	}{
		{
			name: "below cap", snoozes: maximumTerminalAuditSnoozes - 1, wantSnooze: true,
			wantLevel: "WARN", wantCode: "queue_transition_failed", wantHandlers: 1,
		},
		{
			name: "at cap", snoozes: maximumTerminalAuditSnoozes,
			wantLevel: "ERROR", wantCode: "terminal_audit_write_failed",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer
			handlerCalls := 0
			transaction := &executorTransaction{completeError: errors.New(secret)}
			execution := newTestExecutor(transaction, HandlerFunc(func(context.Context, persistence.QueueMessage) error {
				handlerCalls++
				return nil
			}))
			execution.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			err := execution.execute(
				context.Background(), persistence.QueueOutbox, "event-1", 3, 3, test.snoozes,
			)
			var snooze *river.JobSnoozeError
			if test.wantSnooze {
				if !errors.As(err, &snooze) || snooze.Duration != terminalAuditRetryDelay {
					t.Fatalf("execute() error = %v, want snooze %s", err, terminalAuditRetryDelay)
				}
			} else if err == nil || err.Error() != "terminal_audit_write_failed" || errors.As(err, &snooze) {
				t.Fatalf("execute() error = %v, want terminal_audit_write_failed", err)
			}
			if handlerCalls != test.wantHandlers {
				t.Fatalf("handler calls = %d, want %d", handlerCalls, test.wantHandlers)
			}
			if len(transaction.completions) != 0 {
				t.Fatalf("completions = %d, want 0", len(transaction.completions))
			}
			text := logs.String()
			if !strings.Contains(text, `"level":"`+test.wantLevel+`"`) ||
				!strings.Contains(text, `"error_code":"`+test.wantCode+`"`) ||
				!strings.Contains(text, `"message_id":"event-1"`) ||
				strings.Contains(text, secret) {
				t.Fatalf("transition log = %s", text)
			}
			var output bytes.Buffer
			if err := execution.metrics.WritePrometheus(&output); err != nil {
				t.Fatalf("WritePrometheus() error = %v", err)
			}
			metric := `iapstack_queue_attempts_total{queue="outbox",outcome="terminal_audit_write_failed"} 1`
			if test.wantSnooze && strings.Contains(output.String(), metric) {
				t.Fatalf("snooze recorded a give-up metric\n%s", output.String())
			}
			if !test.wantSnooze && !strings.Contains(output.String(), metric) {
				t.Fatalf("metrics omitted give-up counter\n%s", output.String())
			}
		})
	}
}

// TestSnoozeCountReadsRiverMetadata verifies only a non-negative snoozes field advances the cap.
func TestSnoozeCountReadsRiverMetadata(t *testing.T) {
	t.Parallel()

	if got := snoozeCount(nil); got != 0 {
		t.Fatalf("snoozeCount(nil) = %d", got)
	}
	if got := snoozeCount([]byte(`{"snoozes":7,"other":1}`)); got != 7 {
		t.Fatalf("snoozeCount() = %d, want 7", got)
	}
	if got := snoozeCount([]byte(`not-json`)); got != 0 {
		t.Fatalf("snoozeCount(invalid) = %d, want 0", got)
	}
	if got := snoozeCount([]byte(`{"snoozes":-3}`)); got != 0 {
		t.Fatalf("snoozeCount(negative) = %d, want 0", got)
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
