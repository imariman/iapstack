// Package worker executes durable inbox, reconciliation, and outbox work with bounded concurrency.
package worker

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/platform/metrics"
)

const (
	// maximumBackoff caps retry delay for repeatedly failing work.
	maximumBackoff = 15 * time.Minute
)

// Handler processes one worker-owned durable message without changing queue state directly.
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

// Config controls worker identity, polling, claims, attempts, and concurrency.
type Config struct {
	WorkerID     string
	PollInterval time.Duration
	JobTimeout   time.Duration
	Concurrency  int
	BatchSize    int
	MaxAttempts  int
}

// Runner claims durable messages and records each attempt outcome.
type Runner struct {
	store    persistence.OperationsStore
	config   Config
	handlers map[persistence.QueueName]Handler
	metrics  *metrics.Registry
	logger   *slog.Logger
	clock    func() time.Time
}

// Handle calls the adapted handler function.
func (handler HandlerFunc) Handle(ctx context.Context, message persistence.QueueMessage) error {
	return handler(ctx, message)
}

// New validates and constructs one durable worker runner.
func New(
	store persistence.OperationsStore,
	config Config,
	handlers map[persistence.QueueName]Handler,
	registry *metrics.Registry,
	logger *slog.Logger,
) (*Runner, error) {
	if store == nil {
		return nil, errors.New("worker operations store is required")
	}
	if config.WorkerID == "" || config.PollInterval <= 0 || config.JobTimeout <= 0 ||
		config.Concurrency <= 0 || config.BatchSize <= 0 || config.MaxAttempts <= 0 {
		return nil, errors.New("worker configuration is invalid")
	}
	for _, queue := range []persistence.QueueName{
		persistence.QueueInbox,
		persistence.QueueReconciliation,
		persistence.QueueOutbox,
	} {
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
	return &Runner{
		store: store, config: config, handlers: handlers, metrics: registry, logger: logger, clock: time.Now,
	}, nil
}

// Run polls durable queues until cancellation and waits for already-claimed attempts to finish.
func (runner *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(runner.config.PollInterval)
	defer ticker.Stop()
	semaphore := make(chan struct{}, runner.config.Concurrency)
	var attempts sync.WaitGroup

	for {
		if err := runner.poll(ctx, semaphore, &attempts); err != nil {
			runner.logger.Error("worker poll failed", "error_code", "queue_poll_failed")
		}
		select {
		case <-ctx.Done():
			attempts.Wait()
			return nil
		case <-ticker.C:
		}
	}
}

// poll claims bounded work from every queue and dispatches it under one shared semaphore.
func (runner *Runner) poll(ctx context.Context, semaphore chan struct{}, attempts *sync.WaitGroup) error {
	if ctx.Err() != nil {
		return nil
	}
	now := runner.clock().UTC()
	for _, queue := range []persistence.QueueName{
		persistence.QueueInbox,
		persistence.QueueReconciliation,
		persistence.QueueOutbox,
	} {
		availableSlots := cap(semaphore) - len(semaphore)
		if availableSlots <= 0 {
			return nil
		}
		limit := runner.config.BatchSize
		if limit > availableSlots {
			limit = availableSlots
		}
		var messages []persistence.QueueMessage
		err := runner.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
			var claimErr error
			messages, claimErr = repository.ClaimQueue(ctx, persistence.QueueClaim{
				Queue: queue, WorkerID: runner.config.WorkerID, Now: now,
				StaleBefore: now.Add(-2 * runner.config.JobTimeout), Limit: limit,
			})
			if claimErr != nil {
				return claimErr
			}
			depth, depthErr := repository.QueueDepth(ctx, queue)
			if depthErr != nil {
				return depthErr
			}
			for state, count := range depth {
				runner.metrics.SetQueueDepth(string(queue), state, count)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("claim %s: %w", queue, err)
		}
		for _, message := range messages {
			semaphore <- struct{}{}
			attempts.Add(1)
			go func(message persistence.QueueMessage) {
				defer func() {
					<-semaphore
					attempts.Done()
				}()
				runner.attempt(message)
			}(message)
		}
	}
	return nil
}

// attempt runs one handler with a timeout and records a complete, retry, or failed transition.
func (runner *Runner) attempt(message persistence.QueueMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), runner.config.JobTimeout)
	defer cancel()
	err := runner.handlers[message.Queue].Handle(ctx, message)
	completedAt := runner.clock().UTC()
	transition := persistence.QueueTransition{
		Queue: message.Queue, ID: message.ID, WorkerID: runner.config.WorkerID, CompletedAt: completedAt,
	}
	outcome := "completed"
	operation := func(operationContext context.Context, repository persistence.OperationsTransaction) error {
		return repository.CompleteQueue(operationContext, transition)
	}
	if err != nil {
		transition.ErrorCode = errorCode(err)
		if message.Attempts < runner.config.MaxAttempts && isRetryable(err) {
			outcome = "retry"
			transition.AvailableAt = completedAt.Add(backoff(message.Attempts))
			operation = func(operationContext context.Context, repository persistence.OperationsTransaction) error {
				return repository.RetryQueue(operationContext, transition)
			}
		} else {
			outcome = "failed"
			operation = func(operationContext context.Context, repository persistence.OperationsTransaction) error {
				return repository.FailQueue(operationContext, transition)
			}
		}
	}
	transitionCtx, transitionCancel := context.WithTimeout(context.Background(), runner.config.JobTimeout)
	defer transitionCancel()
	if transitionErr := runner.store.Operate(transitionCtx, func(repository persistence.OperationsTransaction) error {
		return operation(transitionCtx, repository)
	}); transitionErr != nil {
		runner.logger.Error("record queue outcome",
			"queue", message.Queue, "message_id", message.ID, "error_code", "queue_transition_failed")
		return
	}
	runner.metrics.ObserveQueueAttempt(string(message.Queue), outcome)
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
		return value.CodeValue()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	return "handler_failed"
}

// backoff returns exponential full jitter capped at the operational maximum.
func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	ceiling := time.Second
	for index := 1; index < attempt && ceiling < maximumBackoff/2; index++ {
		ceiling *= 2
	}
	if ceiling > maximumBackoff {
		ceiling = maximumBackoff
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ceiling / 2
	}
	return time.Duration(binary.BigEndian.Uint64(random[:]) % uint64(ceiling+1))
}
