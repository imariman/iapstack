package processing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

const scheduleRetrySecret = "retired-key-do-not-log"

// scheduleRetryStore is an in-memory schedule queue for promotion tests.
type scheduleRetryStore struct {
	retries      []persistence.ReconciliationScheduleRetry
	jobs         []persistence.ReconciliationJob
	scheduled    map[string]time.Time
	dead         map[string]time.Time
	failCustomer core.CustomerID
	failSave     error
	recordErr    error
}

// scheduleRetryTransaction applies one promotion against the in-memory queue.
type scheduleRetryTransaction struct {
	persistence.OperationsTransaction
	store *scheduleRetryStore
}

// scheduleRetryProtection fails evidence reads with a secret that must stay out of logs.
type scheduleRetryProtection struct {
	err error
}

// Operate runs one callback unless the store is closed.
func (store *scheduleRetryStore) Operate(_ context.Context, operation persistence.OperationsFunc) error {
	return operation(&scheduleRetryTransaction{store: store})
}

// Protect is unused while a stored reconciliation payload is inserted unchanged.
func (scheduleRetryProtection) Protect(context.Context, protection.Request) (protection.Value, error) {
	return protection.Value{}, errors.New("unused")
}

// Open returns the configured evidence failure.
func (service scheduleRetryProtection) Open(context.Context, protection.OpenRequest) ([]byte, error) {
	if service.err != nil {
		return nil, service.err
	}
	return nil, errors.New("unexpected open")
}

// SaveReconciliationJob records one job or fails the configured customer.
func (transaction *scheduleRetryTransaction) SaveReconciliationJob(
	_ context.Context,
	job persistence.ReconciliationJob,
) (string, error) {
	if err := job.Validate(); err != nil {
		return "", err
	}
	if job.CustomerID == transaction.store.failCustomer {
		return "", transaction.store.failSave
	}
	transaction.store.jobs = append(transaction.store.jobs, job)
	return job.ID, nil
}

// ListPendingReconciliationScheduleRetries returns due rows in next-attempt order.
func (transaction *scheduleRetryTransaction) ListPendingReconciliationScheduleRetries(
	_ context.Context,
	now time.Time,
	limit int,
) ([]persistence.ReconciliationScheduleRetry, error) {
	if now.IsZero() {
		return nil, errors.New("reconciliation schedule retry time is required")
	}
	if limit <= 0 {
		return nil, errors.New("reconciliation schedule retry batch size must be positive")
	}
	pending := make([]persistence.ReconciliationScheduleRetry, 0)
	for _, retry := range transaction.store.retries {
		if _, done := transaction.store.scheduled[retry.ID]; done {
			continue
		}
		if _, dead := transaction.store.dead[retry.ID]; dead {
			continue
		}
		if retry.NextAttemptAt.After(now) {
			continue
		}
		pending = append(pending, retry)
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].NextAttemptAt.Equal(pending[j].NextAttemptAt) {
			return pending[i].ID < pending[j].ID
		}
		return pending[i].NextAttemptAt.Before(pending[j].NextAttemptAt)
	})
	if len(pending) > limit {
		pending = pending[:limit]
	}
	return pending, nil
}

// CompleteReconciliationScheduleRetry records that the reconciliation job exists.
func (transaction *scheduleRetryTransaction) CompleteReconciliationScheduleRetry(
	_ context.Context,
	id string,
	scheduledAt time.Time,
) error {
	if scheduledAt.IsZero() {
		return errors.New("reconciliation schedule retry completion time is required")
	}
	if transaction.store.scheduled == nil {
		transaction.store.scheduled = map[string]time.Time{}
	}
	transaction.store.scheduled[id] = scheduledAt
	return nil
}

// RecordReconciliationScheduleRetryFailure mirrors the SQL attempt increment and dead mark.
func (transaction *scheduleRetryTransaction) RecordReconciliationScheduleRetryFailure(
	_ context.Context,
	failure persistence.ReconciliationScheduleRetryFailure,
) (bool, error) {
	if transaction.store.recordErr != nil {
		return false, transaction.store.recordErr
	}
	if err := failure.Validate(); err != nil {
		return false, err
	}
	for index, retry := range transaction.store.retries {
		if retry.ID != failure.ID {
			continue
		}
		if _, done := transaction.store.scheduled[retry.ID]; done {
			return false, nil
		}
		if _, dead := transaction.store.dead[retry.ID]; dead {
			return false, nil
		}
		retry.Attempts++
		retry.NextAttemptAt = failure.NextAttemptAt
		transaction.store.retries[index] = retry
		if failure.Dead {
			if transaction.store.dead == nil {
				transaction.store.dead = map[string]time.Time{}
			}
			transaction.store.dead[retry.ID] = failure.AttemptedAt
		}
		return true, nil
	}
	return false, nil
}

// TestScheduleRetryBackoffLeavesTheDueBatch verifies one poison row does not block a newer retry.
func TestScheduleRetryBackoffLeavesTheDueBatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	poison := newProcessingScheduleRetry("retry-poison", "customer-poison", 1, persistence.ReconciliationRetryReconciliationRequest)
	poison.NextAttemptAt = now.Add(-time.Minute)
	fresh := newProcessingScheduleRetry("retry-fresh", "customer-fresh", 2, persistence.ReconciliationRetryReconciliationRequest)
	fresh.NextAttemptAt = now
	store := &scheduleRetryStore{
		retries:      []persistence.ReconciliationScheduleRetry{poison, fresh},
		failCustomer: "customer-poison",
		failSave:     errors.New(scheduleRetrySecret),
	}
	logs := &bytes.Buffer{}
	service := &Service{
		store:      store,
		protection: scheduleRetryProtection{},
		clock:      func() time.Time { return now },
		logger:     slog.New(slog.NewJSONHandler(logs, nil)),
	}

	if err := service.RetryReconciliationSchedules(context.Background()); err != nil {
		t.Fatalf("RetryReconciliationSchedules() error = %v", err)
	}
	if len(store.jobs) != 1 || store.jobs[0].CustomerID != "customer-fresh" {
		t.Fatalf("jobs = %#v, want the newer customer", store.jobs)
	}
	updatedPoison := scheduleRetryByID(t, store, "retry-poison")
	if updatedPoison.Attempts != 1 || !updatedPoison.NextAttemptAt.Equal(now.Add(reconciliationRetryBaseDelay)) {
		t.Fatalf("poison retry = attempts %d next %v, want 1 at %v",
			updatedPoison.Attempts, updatedPoison.NextAttemptAt, now.Add(reconciliationRetryBaseDelay))
	}
	if strings.Contains(logs.String(), scheduleRetrySecret) || strings.Contains(logs.String(), "reconciliation_schedule_retry_dead") {
		t.Fatalf("first failure log = %s, want no secret and no dead record", logs.String())
	}

	if err := service.RetryReconciliationSchedules(context.Background()); err != nil {
		t.Fatalf("second RetryReconciliationSchedules() error = %v", err)
	}
	if len(store.jobs) != 1 {
		t.Fatalf("jobs after second pass = %d, want 1", len(store.jobs))
	}
	if got := scheduleRetryByID(t, store, "retry-poison").Attempts; got != 1 {
		t.Fatalf("poison attempts after second pass = %d, want 1 while it is waiting", got)
	}
}

// TestScheduleRetryMarksUnreadablePayloadDead verifies the attempt cap stops a retired key.
func TestScheduleRetryMarksUnreadablePayloadDead(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	retry := newProcessingScheduleRetry(
		"retry-unreadable", "customer-1", 3, persistence.ReconciliationRetryPurchaseEvidence,
	)
	retry.Attempts = reconciliationRetryMaxAttempts - 1
	retry.NextAttemptAt = now
	store := &scheduleRetryStore{retries: []persistence.ReconciliationScheduleRetry{retry}}
	logs := &bytes.Buffer{}
	service := &Service{
		store:      store,
		protection: scheduleRetryProtection{err: errors.New(scheduleRetrySecret)},
		clock:      func() time.Time { return now },
		logger:     slog.New(slog.NewJSONHandler(logs, nil)),
	}

	if err := service.RetryReconciliationSchedules(context.Background()); err != nil {
		t.Fatalf("RetryReconciliationSchedules() error = %v", err)
	}
	if len(store.jobs) != 0 {
		t.Fatalf("jobs = %d, want none for an unreadable payload", len(store.jobs))
	}
	if _, dead := store.dead[retry.ID]; !dead {
		t.Fatal("unreadable retry was not marked dead")
	}
	if got := scheduleRetryByID(t, store, retry.ID).Attempts; got != reconciliationRetryMaxAttempts {
		t.Fatalf("attempts = %d, want %d", got, reconciliationRetryMaxAttempts)
	}
	text := logs.String()
	if !strings.Contains(text, `"error_code":"reconciliation_schedule_retry_dead"`) ||
		!strings.Contains(text, `"customer_id":"customer-1"`) ||
		!strings.Contains(text, `"attempts":12`) {
		t.Fatalf("dead log = %s, want reconciliation_schedule_retry_dead", text)
	}
	if strings.Contains(text, scheduleRetrySecret) || strings.Contains(text, "ciphertext") {
		t.Fatalf("dead log leaked the failure or payload: %s", text)
	}

	if err := service.RetryReconciliationSchedules(context.Background()); err != nil {
		t.Fatalf("second RetryReconciliationSchedules() error = %v", err)
	}
	if got := scheduleRetryByID(t, store, retry.ID).Attempts; got != reconciliationRetryMaxAttempts {
		t.Fatalf("attempts after dead = %d, want no further promotion", got)
	}
}

// TestScheduleRetryReturnsRecordFailure verifies a backoff write failure stays visible to the worker.
func TestScheduleRetryReturnsRecordFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	retry := newProcessingScheduleRetry("retry-record", "customer-1", 4, persistence.ReconciliationRetryReconciliationRequest)
	retry.NextAttemptAt = now
	recordErr := errors.New("schedule retry storage unavailable")
	store := &scheduleRetryStore{
		retries:      []persistence.ReconciliationScheduleRetry{retry},
		failCustomer: "customer-1",
		failSave:     errors.New(scheduleRetrySecret),
		recordErr:    recordErr,
	}
	service := &Service{
		store:      store,
		protection: scheduleRetryProtection{},
		clock:      func() time.Time { return now },
	}

	if err := service.RetryReconciliationSchedules(context.Background()); !errors.Is(err, recordErr) {
		t.Fatalf("RetryReconciliationSchedules() error = %v, want record failure", err)
	}
	if got := scheduleRetryByID(t, store, retry.ID).Attempts; got != 0 {
		t.Fatalf("attempts = %d, want 0 when the backoff write fails", got)
	}
}

// newProcessingScheduleRetry builds one valid retry without a due time.
func newProcessingScheduleRetry(id, customer string, mark byte, kind string) persistence.ReconciliationScheduleRetry {
	var fingerprint [32]byte
	fingerprint[0] = mark
	metadata := json.RawMessage(`{}`)
	if kind == persistence.ReconciliationRetryPurchaseEvidence {
		metadata = json.RawMessage(`{"external_customer_id":"customer-external","claimed_products":["monthly"]}`)
	}
	return persistence.ReconciliationScheduleRetry{
		ID:            id,
		ProjectID:     "project-1",
		ApplicationID: "application-1",
		CustomerID:    core.CustomerID(customer),
		Kind:          kind,
		Payload: protection.Value{
			Ciphertext:  []byte("ciphertext"),
			Fingerprint: fingerprint,
			KeyID:       "key-1",
		},
		Metadata:    metadata,
		AvailableAt: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC),
	}
}

// scheduleRetryByID returns the stored retry or fails the test.
func scheduleRetryByID(t *testing.T, store *scheduleRetryStore, id string) persistence.ReconciliationScheduleRetry {
	t.Helper()
	for _, retry := range store.retries {
		if retry.ID == id {
			return retry
		}
	}
	t.Fatalf("retry %s was not stored", id)
	return persistence.ReconciliationScheduleRetry{}
}
