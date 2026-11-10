package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

// scheduleRetrySecret is failure text that must never reach worker logs.
const scheduleRetrySecret = "retired-key-do-not-log"

// scheduleRetryStore is an in-memory schedule queue for promotion tests.
type scheduleRetryStore struct {
	retries        []persistence.ReconciliationScheduleRetry
	jobs           []persistence.ReconciliationJob
	scheduled      map[string]time.Time
	dead           map[string]time.Time
	failCustomer   core.CustomerID
	failSave       error
	retrySaveErr   error
	recordErr      error
	beforeComplete func(*scheduleRetryStore, string)
}

// scheduleRetryTransaction applies one promotion against the in-memory queue.
type scheduleRetryTransaction struct {
	persistence.OperationsTransaction
	store *scheduleRetryStore
}

// scheduleRetryProtection protects reconciliation jobs and opens stored retry payloads.
type scheduleRetryProtection struct {
	openErr    error
	protectErr error
}

// Operate runs one callback against a transactional snapshot that commits only on nil error.
func (store *scheduleRetryStore) Operate(_ context.Context, operation persistence.OperationsFunc) error {
	snapshot := store.clone()
	if err := operation(&scheduleRetryTransaction{store: snapshot}); err != nil {
		return err
	}
	*store = *snapshot
	return nil
}

// clone copies the in-memory retry queue so failed transactions roll back cleanly.
func (store *scheduleRetryStore) clone() *scheduleRetryStore {
	cloned := &scheduleRetryStore{
		retries:        append([]persistence.ReconciliationScheduleRetry(nil), store.retries...),
		jobs:           append([]persistence.ReconciliationJob(nil), store.jobs...),
		scheduled:      make(map[string]time.Time, len(store.scheduled)),
		dead:           make(map[string]time.Time, len(store.dead)),
		failCustomer:   store.failCustomer,
		failSave:       store.failSave,
		retrySaveErr:   store.retrySaveErr,
		recordErr:      store.recordErr,
		beforeComplete: store.beforeComplete,
	}
	for id, at := range store.scheduled {
		cloned.scheduled[id] = at
	}
	for id, at := range store.dead {
		cloned.dead[id] = at
	}
	return cloned
}

// Protect encrypts one test payload or returns the configured failure.
func (service scheduleRetryProtection) Protect(_ context.Context, request protection.Request) (protection.Value, error) {
	if err := request.Validate(); err != nil {
		return protection.Value{}, err
	}
	if service.protectErr != nil {
		return protection.Value{}, service.protectErr
	}
	plaintext := request.Bytes()
	return protection.Value{
		Ciphertext:  plaintext,
		Fingerprint: sha256.Sum256(plaintext),
		KeyID:       "key-1",
	}, nil
}

// Open returns one valid verification payload or the configured failure.
func (service scheduleRetryProtection) Open(_ context.Context, request protection.OpenRequest) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if service.openErr != nil {
		return nil, service.openErr
	}
	return []byte(`{"external_customer_id":"customer-external","claimed_products":["monthly"],"evidence":{"product_kind":"subscription","purchase_token":"token-1"}}`), nil
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

// SaveReconciliationScheduleRetry records one pending retry idempotently per live payload.
func (transaction *scheduleRetryTransaction) SaveReconciliationScheduleRetry(
	_ context.Context,
	retry persistence.ReconciliationScheduleRetry,
) error {
	if transaction.store.retrySaveErr != nil {
		return transaction.store.retrySaveErr
	}
	if err := retry.Validate(); err != nil {
		return err
	}
	for _, existing := range transaction.store.retries {
		if _, done := transaction.store.scheduled[existing.ID]; done {
			continue
		}
		if _, dead := transaction.store.dead[existing.ID]; dead {
			continue
		}
		if existing.ApplicationID == retry.ApplicationID &&
			existing.CustomerID == retry.CustomerID &&
			existing.Payload.Fingerprint == retry.Payload.Fingerprint {
			return nil
		}
	}
	transaction.store.retries = append(transaction.store.retries, retry)
	return nil
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

// CompleteReconciliationScheduleRetry claims one pending retry before its job is inserted.
func (transaction *scheduleRetryTransaction) CompleteReconciliationScheduleRetry(
	_ context.Context,
	id string,
	scheduledAt time.Time,
) (bool, error) {
	if scheduledAt.IsZero() {
		return false, errors.New("reconciliation schedule retry completion time is required")
	}
	if transaction.store.beforeComplete != nil {
		transaction.store.beforeComplete(transaction.store, id)
	}
	if _, done := transaction.store.scheduled[id]; done {
		return false, nil
	}
	if _, dead := transaction.store.dead[id]; dead {
		return false, nil
	}
	if transaction.store.scheduled == nil {
		transaction.store.scheduled = map[string]time.Time{}
	}
	transaction.store.scheduled[id] = scheduledAt
	return true, nil
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
	poison := newProcessingScheduleRetry("retry-poison", "customer-poison", 1)
	poison.NextAttemptAt = now.Add(-time.Minute)
	fresh := newProcessingScheduleRetry("retry-fresh", "customer-fresh", 2)
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
	if _, scheduled := store.scheduled["retry-poison"]; scheduled {
		t.Fatal("failed job insert left retry-poison marked scheduled")
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
	retry := newProcessingScheduleRetry("retry-unreadable", "customer-1", 3)
	retry.Attempts = reconciliationRetryMaxAttempts - 1
	retry.NextAttemptAt = now
	store := &scheduleRetryStore{retries: []persistence.ReconciliationScheduleRetry{retry}}
	logs := &bytes.Buffer{}
	service := &Service{
		store:      store,
		protection: scheduleRetryProtection{openErr: errors.New(scheduleRetrySecret)},
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
	retry := newProcessingScheduleRetry("retry-record", "customer-1", 4)
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

// TestScheduleRetrySkipsJobInsertWhenRowAlreadyDeadOrScheduled verifies an unclaimed row stops before inserting.
func TestScheduleRetrySkipsJobInsertWhenRowAlreadyDeadOrScheduled(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	retry := newProcessingScheduleRetry("retry-race", "customer-1", 5)
	retry.Attempts = reconciliationRetryMaxAttempts - 1
	retry.NextAttemptAt = now
	store := &scheduleRetryStore{
		retries: []persistence.ReconciliationScheduleRetry{retry},
		beforeComplete: func(current *scheduleRetryStore, id string) {
			if current.dead == nil {
				current.dead = map[string]time.Time{}
			}
			current.dead[id] = now
		},
	}
	service := &Service{
		store:      store,
		protection: scheduleRetryProtection{},
		clock:      func() time.Time { return now },
	}

	if err := service.RetryReconciliationSchedules(context.Background()); err != nil {
		t.Fatalf("RetryReconciliationSchedules() error = %v", err)
	}
	if len(store.jobs) != 0 {
		t.Fatalf("jobs = %d, want 0 when another worker already marked the retry dead", len(store.jobs))
	}
	if _, scheduled := store.scheduled[retry.ID]; scheduled {
		t.Fatal("dead retry was also marked scheduled")
	}
}

// TestScheduleReconciliationJoinsRetryFailure preserves both schedule and retry errors on double failure.
func TestScheduleReconciliationJoinsRetryFailure(t *testing.T) {
	t.Parallel()

	jobSaveErr := errors.New("job insert unavailable")
	retrySaveErr := errors.New("retry insert unavailable")
	store := &scheduleRetryStore{
		failCustomer: "customer-1",
		failSave:     jobSaveErr,
		retrySaveErr: retrySaveErr,
	}
	service := &Service{
		store:      store,
		protection: scheduleRetryProtection{},
		clock:      func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) },
	}

	err := service.ScheduleReconciliation(
		context.Background(),
		"project-1",
		"application-1",
		"customer-1",
		jobs.VerificationPayload{
			ExternalCustomerID: "customer-external",
			ClaimedProducts:    []core.ProviderProductID{"monthly"},
			Evidence:           []byte(`{"product_kind":"subscription","purchase_token":"token-1"}`),
		},
	)
	if !errors.Is(err, jobSaveErr) || !errors.Is(err, retrySaveErr) {
		t.Fatalf("ScheduleReconciliation() error = %v, want both schedule and retry errors", err)
	}
}

// newProcessingScheduleRetry builds one valid pending retry without a due time.
func newProcessingScheduleRetry(id, customer string, mark byte) persistence.ReconciliationScheduleRetry {
	var fingerprint [32]byte
	fingerprint[0] = mark
	return persistence.ReconciliationScheduleRetry{
		ID:            id,
		ProjectID:     "project-1",
		ApplicationID: "application-1",
		CustomerID:    core.CustomerID(customer),
		Payload: protection.Value{
			Ciphertext:  []byte("ciphertext"),
			Fingerprint: fingerprint,
			KeyID:       "key-1",
		},
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
