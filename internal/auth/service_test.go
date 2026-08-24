package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// fakeOperationsStore retains API key verifiers for authentication tests.
type fakeOperationsStore struct {
	records map[string]persistence.APIKeyRecord
}

// fakeOperationsTransaction embeds unused repositories and implements API key operations.
type fakeOperationsTransaction struct {
	persistence.OperationsTransaction
	records map[string]persistence.APIKeyRecord
}

// TestServiceCreatesAndAuthenticatesScopedKeys verifies one-time bearer creation and scope recovery.
func TestServiceCreatesAndAuthenticatesScopedKeys(t *testing.T) {
	store := &fakeOperationsStore{records: make(map[string]persistence.APIKeyRecord)}
	service, err := NewService(store, "bootstrap-secret")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	principal := Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: core.ProjectID("project-1"),
		ApplicationID: core.ApplicationID("application-1"),
	}
	bearer, err := service.Create(context.Background(), principal)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if bearer == "" {
		t.Fatal("Create() bearer is empty")
	}
	authenticated, err := service.Authenticate(context.Background(), bearer)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if authenticated != principal {
		t.Fatalf("Authenticate() = %#v, want %#v", authenticated, principal)
	}
	if _, err := service.Authenticate(context.Background(), bearer+"tampered"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tampered Authenticate() error = %v, want ErrUnauthorized", err)
	}
	admin, err := service.Authenticate(context.Background(), "bootstrap-secret")
	if err != nil || admin.Role != persistence.APIKeyRoleAdmin {
		t.Fatalf("bootstrap Authenticate() = %#v, %v", admin, err)
	}
}

// Operate executes one fake atomic operations callback.
func (store *fakeOperationsStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	return operation(&fakeOperationsTransaction{records: store.records})
}

// APIKey returns one retained non-revoked verifier.
func (transaction *fakeOperationsTransaction) APIKey(
	_ context.Context,
	id string,
) (persistence.APIKeyRecord, error) {
	record, exists := transaction.records[id]
	if !exists {
		return persistence.APIKeyRecord{}, persistence.ErrNotFound
	}
	return record, nil
}

// PutAPIKey retains one verifier and rejects duplicate identities.
func (transaction *fakeOperationsTransaction) PutAPIKey(_ context.Context, record persistence.APIKeyRecord) error {
	if _, exists := transaction.records[record.ID]; exists {
		return persistence.ErrConflict
	}
	transaction.records[record.ID] = record
	return nil
}
