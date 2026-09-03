// Package worker executes durable inbox, reconciliation, and outbox jobs through River.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/platform/metrics"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

const (
	// queueMetricsInterval controls the inexpensive aggregate River depth query.
	queueMetricsInterval = time.Minute
	// queueCleanupInterval bounds how long expired terminal audit rows remain after retention.
	queueCleanupInterval = time.Minute
	// queueCleanupBatchSize bounds row locks and transaction work per queue and cleanup pass.
	queueCleanupBatchSize = 1_000
)

// Handler processes one durable message without changing queue state directly.
type Handler interface {
	// Handle performs one bounded attempt and returns a safely classifiable error.
	Handle(context.Context, persistence.QueueMessage) error
}

// HandlerFunc adapts one function to the durable message handler contract.
type HandlerFunc func(context.Context, persistence.QueueMessage) error

// RetryableError exposes whether one safe failure should be attempted again.
type RetryableError interface {
	error
	// Retryable reports whether the failed operation may succeed later.
	Retryable() bool
}

// Config controls River identity, polling, attempts, retention, shutdown, and per-queue concurrency.
type Config struct {
	WorkerID        string
	PollInterval    time.Duration
	JobTimeout      time.Duration
	ShutdownTimeout time.Duration
	Concurrency     int
	MaxAttempts     int
	QueueRetention  time.Duration
}

// Runner owns the River client and the three provider-neutral job workers.
type Runner struct {
	client          *river.Client[pgx.Tx]
	pool            *pgxpool.Pool
	store           persistence.OperationsStore
	metrics         *metrics.Registry
	logger          *slog.Logger
	shutdownTimeout time.Duration
	queueRetention  time.Duration
	clock           func() time.Time
}

// executor loads durable payloads, invokes handlers, and records terminal audit outcomes.
type executor struct {
	store    persistence.OperationsStore
	handlers map[persistence.QueueName]Handler
	metrics  *metrics.Registry
	logger   *slog.Logger
	clock    func() time.Time
}

// inboxWorker adapts River inbox arguments to the shared executor.
type inboxWorker struct {
	river.WorkerDefaults[jobs.InboxArgs]
	executor *executor
}

// outboxWorker adapts River outbox arguments to the shared executor.
type outboxWorker struct {
	river.WorkerDefaults[jobs.OutboxArgs]
	executor *executor
}

// reconciliationWorker adapts River reconciliation arguments to the shared executor.
type reconciliationWorker struct {
	river.WorkerDefaults[jobs.ReconciliationArgs]
	executor *executor
}

// Handle calls the adapted handler function.
func (handler HandlerFunc) Handle(ctx context.Context, message persistence.QueueMessage) error {
	return handler(ctx, message)
}

// New validates dependencies and constructs one River-backed worker runner.
func New(
	pool *pgxpool.Pool,
	store persistence.OperationsStore,
	config Config,
	handlers map[persistence.QueueName]Handler,
	registry *metrics.Registry,
	logger *slog.Logger,
) (*Runner, error) {
	if pool == nil {
		return nil, errors.New("worker PostgreSQL pool is required")
	}
	if store == nil {
		return nil, errors.New("worker operations store is required")
	}
	if config.PollInterval <= 0 || config.JobTimeout <= 0 || config.ShutdownTimeout <= 0 ||
		config.Concurrency <= 0 || config.MaxAttempts <= 0 || config.QueueRetention <= 0 {
		return nil, errors.New("worker configuration is invalid")
	}
	for _, queue := range durableQueues() {
		if handlers[queue] == nil {
			return nil, fmt.Errorf("worker handler for %s is required", queue)
		}
	}
	if registry == nil {
		registry = metrics.New()
	}
	if logger == nil {
		logger = slog.Default()
	}
	execution := &executor{
		store: store, handlers: handlers, metrics: registry, logger: logger, clock: time.Now,
	}
	workers := river.NewWorkers()
	if err := river.AddWorkerSafely(workers, &inboxWorker{executor: execution}); err != nil {
		return nil, fmt.Errorf("register inbox River worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, &outboxWorker{executor: execution}); err != nil {
		return nil, fmt.Errorf("register outbox River worker: %w", err)
	}
	if err := river.AddWorkerSafely(workers, &reconciliationWorker{executor: execution}); err != nil {
		return nil, fmt.Errorf("register reconciliation River worker: %w", err)
	}
	queueConfig := river.QueueConfig{MaxWorkers: config.Concurrency}
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		ID: strings.TrimSpace(config.WorkerID),
		Queues: map[string]river.QueueConfig{
			jobs.QueueInbox: queueConfig, jobs.QueueOutbox: queueConfig, jobs.QueueReconciliation: queueConfig,
		},
		Workers:                     workers,
		Logger:                      logger,
		JobTimeout:                  config.JobTimeout,
		MaxAttempts:                 config.MaxAttempts,
		FetchPollInterval:           config.PollInterval,
		CancelledJobRetentionPeriod: config.QueueRetention,
		CompletedJobRetentionPeriod: config.QueueRetention,
		DiscardedJobRetentionPeriod: config.QueueRetention,
	})
	if err != nil {
		return nil, fmt.Errorf("construct River worker client: %w", err)
	}
	return &Runner{
		client: client, pool: pool, store: store, metrics: registry, logger: logger,
		shutdownTimeout: config.ShutdownTimeout, queueRetention: config.QueueRetention, clock: time.Now,
	}, nil
}

// Run starts River, stops accepting work on cancellation, and bounds graceful shutdown.
func (runner *Runner) Run(ctx context.Context) error {
	if runner == nil || runner.client == nil {
		return errors.New("river worker runner is not initialized")
	}
	if err := runner.client.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("start River worker: %w", err)
	}
	lifecycleContext := context.WithoutCancel(ctx)
	maintenanceContext, maintenanceCancel := context.WithCancel(lifecycleContext)
	var maintenanceWorkers sync.WaitGroup
	maintenanceWorkers.Add(2)
	go func() {
		defer maintenanceWorkers.Done()
		runner.sampleQueueDepth(maintenanceContext)
	}()
	go func() {
		defer maintenanceWorkers.Done()
		runner.cleanTerminalQueues(maintenanceContext)
	}()
	defer func() {
		maintenanceCancel()
		maintenanceWorkers.Wait()
	}()
	<-ctx.Done()
	shutdownContext, cancel := context.WithTimeout(lifecycleContext, runner.shutdownTimeout)
	defer cancel()
	gracefulStopError := runner.client.Stop(shutdownContext)
	if gracefulStopError == nil {
		return nil
	}
	hardStopContext, hardStopCancel := context.WithTimeout(lifecycleContext, runner.shutdownTimeout)
	defer hardStopCancel()
	if err := runner.client.StopAndCancel(hardStopContext); err != nil {
		return errors.Join(
			fmt.Errorf("stop River worker gracefully: %w", gracefulStopError),
			fmt.Errorf("cancel River worker: %w", err),
		)
	}
	return nil
}

// cleanTerminalQueues periodically removes expired audit rows that cannot be executed again.
func (runner *Runner) cleanTerminalQueues(ctx context.Context) {
	ticker := time.NewTicker(queueCleanupInterval)
	defer ticker.Stop()
	for {
		if err := runner.purgeTerminalQueueRecords(ctx); err != nil && ctx.Err() == nil {
			runner.logger.Error("purge terminal queue records", "error_code", "queue_cleanup_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// purgeTerminalQueueRecords applies the configured retention cutoff in one atomic operation.
func (runner *Runner) purgeTerminalQueueRecords(ctx context.Context) error {
	cutoff := runner.clock().UTC().Add(-runner.queueRetention)
	var deleted int64
	err := runner.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var purgeErr error
		deleted, purgeErr = repository.PurgeTerminalQueueRecords(ctx, cutoff, queueCleanupBatchSize)
		return purgeErr
	})
	if err != nil {
		return fmt.Errorf("purge terminal queue records: %w", err)
	}
	if deleted > 0 {
		runner.logger.Info("purged terminal queue records", "record_count", deleted)
	}
	return nil
}

// sampleQueueDepth publishes bounded River state counts until worker shutdown.
func (runner *Runner) sampleQueueDepth(ctx context.Context) {
	ticker := time.NewTicker(queueMetricsInterval)
	defer ticker.Stop()
	for {
		if err := runner.refreshQueueDepth(ctx); err != nil && ctx.Err() == nil {
			runner.logger.Error("sample River queue depth", "error_code", "queue_metrics_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// refreshQueueDepth reads grouped River job states without participating in queue transitions.
func (runner *Runner) refreshQueueDepth(ctx context.Context) error {
	states := []string{"available", "cancelled", "completed", "discarded", "pending", "retryable", "running", "scheduled"}
	for _, queue := range durableQueues() {
		runner.metrics.SetQueueOldestAge(string(queue), 0)
		for _, state := range states {
			runner.metrics.SetQueueDepth(string(queue), state, 0)
		}
	}
	rows, err := runner.pool.Query(ctx, `
		SELECT queue, state, count(*)
		FROM river_job
		WHERE queue = ANY($1)
		GROUP BY queue, state
	`, []string{jobs.QueueInbox, jobs.QueueOutbox, jobs.QueueReconciliation})
	if err != nil {
		return fmt.Errorf("query River queue depth: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var queue, state string
		var depth int64
		if err := rows.Scan(&queue, &state, &depth); err != nil {
			return fmt.Errorf("scan River queue depth: %w", err)
		}
		persistenceQueue, ok := persistenceQueue(queue)
		if !ok {
			continue
		}
		runner.metrics.SetQueueDepth(string(persistenceQueue), state, depth)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate River queue depth: %w", err)
	}
	rows.Close()
	ageRows, err := runner.pool.Query(ctx, `
		SELECT queue, EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - MIN(scheduled_at)))::double precision
		FROM river_job
		WHERE queue = ANY($1)
			AND state IN ('available', 'retryable', 'scheduled')
			AND scheduled_at <= CURRENT_TIMESTAMP
		GROUP BY queue
	`, []string{jobs.QueueInbox, jobs.QueueOutbox, jobs.QueueReconciliation})
	if err != nil {
		return fmt.Errorf("query River queue age: %w", err)
	}
	defer ageRows.Close()
	for ageRows.Next() {
		var queue string
		var ageSeconds float64
		if err := ageRows.Scan(&queue, &ageSeconds); err != nil {
			return fmt.Errorf("scan River queue age: %w", err)
		}
		persistenceQueue, ok := persistenceQueue(queue)
		if !ok {
			continue
		}
		runner.metrics.SetQueueOldestAge(string(persistenceQueue), time.Duration(ageSeconds*float64(time.Second)))
	}
	if err := ageRows.Err(); err != nil {
		return fmt.Errorf("iterate River queue age: %w", err)
	}
	return nil
}

// Work processes one protected notification job.
func (worker *inboxWorker) Work(ctx context.Context, job *river.Job[jobs.InboxArgs]) error {
	return worker.executor.execute(ctx, persistence.QueueInbox, job.Args.MessageID, job.Attempt, job.MaxAttempts)
}

// Work processes one immutable webhook delivery job.
func (worker *outboxWorker) Work(ctx context.Context, job *river.Job[jobs.OutboxArgs]) error {
	return worker.executor.execute(ctx, persistence.QueueOutbox, job.Args.EventID, job.Attempt, job.MaxAttempts)
}

// Work processes one protected lifecycle reconciliation job.
func (worker *reconciliationWorker) Work(ctx context.Context, job *river.Job[jobs.ReconciliationArgs]) error {
	return worker.executor.execute(ctx, persistence.QueueReconciliation, job.Args.JobID, job.Attempt, job.MaxAttempts)
}

// execute loads one audit record, invokes its handler, and persists terminal outcomes.
func (execution *executor) execute(
	ctx context.Context,
	queue persistence.QueueName,
	id string,
	attempt int,
	maxAttempts int,
) error {
	startedAt := time.Now()
	outcome := "failed"
	defer func() {
		execution.metrics.ObserveQueueDuration(string(queue), outcome, time.Since(startedAt))
	}()

	var message persistence.QueueMessage
	err := execution.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		message, loadErr = repository.QueueMessage(ctx, queue, id)
		return loadErr
	})
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			outcome = "cancelled"
			return river.JobCancel(errors.New("queue_record_not_found"))
		}
		return errors.New("queue_record_load_failed")
	}
	if message.Completed || message.Failed {
		outcome = "skipped"
		return nil
	}
	handlerErr := execution.handlers[queue].Handle(ctx, message)
	if handlerErr == nil {
		if err := execution.complete(ctx, queue, id); err != nil {
			return err
		}
		outcome = "completed"
		return nil
	}
	if errors.Is(handlerErr, context.Canceled) && ctx.Err() != nil {
		outcome = "cancelled"
		return context.Canceled
	}
	code := errorCode(handlerErr)
	execution.logHandlerFailure(queue, id, attempt, code, handlerErr)
	if isRetryable(handlerErr) && attempt < maxAttempts {
		outcome = "retry"
		execution.metrics.ObserveQueueAttempt(string(queue), "retry")
		return errors.New(code)
	}
	if err := execution.fail(ctx, queue, id, code); err != nil {
		return err
	}
	if isRetryable(handlerErr) {
		return errors.New(code)
	}
	return river.JobCancel(errors.New(code))
}

// logHandlerFailure records only bounded provider classification fields, never raw provider errors or payloads.
func (execution *executor) logHandlerFailure(
	queue persistence.QueueName,
	id string,
	attempt int,
	code string,
	err error,
) {
	attributes := []any{
		"queue", queue,
		"message_id", id,
		"attempt", attempt,
		"error_code", code,
	}
	var failure *stores.Failure
	if errors.As(err, &failure) {
		attributes = append(attributes,
			"provider", safeErrorCode(string(failure.Provider)),
			"provider_operation", safeErrorCode(failure.Operation),
			"provider_failure_kind", safeErrorCode(string(failure.Kind)),
		)
	}
	execution.logger.Warn("queue handler failed", attributes...)
}

// complete records one successful audit outcome after the handler returns.
func (execution *executor) complete(ctx context.Context, queue persistence.QueueName, id string) error {
	completedAt := execution.clock().UTC()
	err := execution.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		return repository.CompleteQueue(ctx, persistence.QueueCompletion{
			Queue: queue, ID: id, CompletedAt: completedAt,
		})
	})
	if err != nil {
		execution.logger.Error("record queue completion",
			"queue", queue, "message_id", id, "error_code", "queue_transition_failed")
		return errors.New("queue_transition_failed")
	}
	execution.metrics.ObserveQueueAttempt(string(queue), "completed")
	return nil
}

// fail records one terminal audit outcome after attempts are exhausted or a permanent error occurs.
func (execution *executor) fail(ctx context.Context, queue persistence.QueueName, id, code string) error {
	failedAt := execution.clock().UTC()
	err := execution.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		return repository.FailQueue(ctx, persistence.QueueFailure{
			Queue: queue, ID: id, FailedAt: failedAt, ErrorCode: code,
		})
	})
	if err != nil {
		execution.logger.Error("record queue failure",
			"queue", queue, "message_id", id, "error_code", "queue_transition_failed")
		return errors.New("queue_transition_failed")
	}
	execution.metrics.ObserveQueueAttempt(string(queue), "failed")
	return nil
}

// durableQueues returns every fixed queue that must have a registered handler.
func durableQueues() []persistence.QueueName {
	return []persistence.QueueName{
		persistence.QueueInbox,
		persistence.QueueReconciliation,
		persistence.QueueOutbox,
	}
}

// persistenceQueue maps one River queue name back to the stable metrics label.
func persistenceQueue(queue string) (persistence.QueueName, bool) {
	switch queue {
	case jobs.QueueInbox:
		return persistence.QueueInbox, true
	case jobs.QueueOutbox:
		return persistence.QueueOutbox, true
	case jobs.QueueReconciliation:
		return persistence.QueueReconciliation, true
	default:
		return "", false
	}
}

// isRetryable applies explicit error classification and treats context deadlines as transient.
func isRetryable(err error) bool {
	var classified RetryableError
	if errors.As(err, &classified) {
		return classified.Retryable()
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// errorCode returns a bounded safe code without persisting arbitrary error messages.
func errorCode(err error) string {
	type coded interface {
		CodeValue() string
	}
	var value coded
	if errors.As(err, &value) {
		return safeErrorCode(value.CodeValue())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	return "handler_failed"
}

// safeErrorCode accepts only bounded lowercase identifiers suitable for logs and audit metadata.
func safeErrorCode(code string) string {
	if len(code) == 0 || len(code) > 64 {
		return "handler_failed"
	}
	for _, character := range code {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return "handler_failed"
		}
	}
	return code
}
