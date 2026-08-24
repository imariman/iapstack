package credentials_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/credentials"
	"github.com/imariman/iapstack/internal/persistence"
	platformprotection "github.com/imariman/iapstack/internal/platform/protection"
	protectionport "github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// credentialTestProjectID identifies the owning project in service tests.
	credentialTestProjectID core.ProjectID = "project-1"
	// credentialTestApplicationID identifies the owning application in service tests.
	credentialTestApplicationID core.ApplicationID = "application-1"
	// credentialTestProviderApplicationID identifies the provider-side application.
	credentialTestProviderApplicationID core.ProviderApplicationID = "huawei-app-1"
	// credentialTestKind identifies the logical provider credential package.
	credentialTestKind stores.CredentialKind = "server_api"
	// credentialTestContentType identifies the provider-owned credential schema.
	credentialTestContentType = "application/vnd.iapstack.huawei-credentials+json"
	// credentialTestOldKeyID identifies encryption writes before rotation.
	credentialTestOldKeyID = "encryption-old"
	// credentialTestActiveKeyID identifies encryption writes after rotation.
	credentialTestActiveKeyID = "encryption-active"
	// credentialTestSecret is the plaintext marker protected by service tests.
	credentialTestSecret = `{"client_id":"client-1","client_secret":"never-log-this-secret"}`
	// credentialTestRotatedSecret is a distinct logical credential revision.
	credentialTestRotatedSecret = `{"client_id":"client-1","client_secret":"rotated-secret"}`
	// credentialTestKeySize is the required root-key byte length.
	credentialTestKeySize = 32
)

// fakeStore owns one copy-on-commit credential repository for service tests.
type fakeStore struct {
	application core.Application
	records     map[persistence.CredentialKey]persistence.CredentialRecord
}

// fakeTransaction implements only repositories used by the credential service.
type fakeTransaction struct {
	persistence.Transaction
	application core.Application
	records     map[persistence.CredentialKey]persistence.CredentialRecord
}

// TestNewServiceRejectsNilDependencies verifies fail-fast credential service assembly.
func TestNewServiceRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	keyring := newKeyring(t, credentialTestActiveKeyID, true)
	if _, err := credentials.NewService(nil, keyring); err == nil {
		t.Fatal("NewService() nil store error = nil, want validation error")
	}
	var nilStore *fakeStore
	if _, err := credentials.NewService(nilStore, keyring); err == nil {
		t.Fatal("NewService() typed nil store error = nil, want validation error")
	}
	var nilKeyring *platformprotection.Keyring
	if _, err := credentials.NewService(newFakeStore(), nilKeyring); err == nil {
		t.Fatal("NewService() typed nil keyring error = nil, want validation error")
	}
}

// TestServicePutAndResolveCredential verifies encrypted persistence and adapter-facing resolution.
func TestServicePutAndResolveCredential(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	service := newCredentialService(t, store, newKeyring(t, credentialTestActiveKeyID, true))
	application := credentialApplication()
	credential := newCredential(t, credentialTestSecret)
	metadata, err := service.Put(context.Background(), application, credential, 0)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if metadata.Revision != 1 || metadata.ApplicationID != application.ID || metadata.Kind != credentialTestKind {
		t.Fatalf("Put() metadata = %#v, want first scoped revision", metadata)
	}

	record := store.records[credentialKey()]
	if bytes.Contains(record.Payload.Ciphertext, []byte("never-log-this-secret")) {
		t.Fatal("stored ciphertext contains plaintext credential marker")
	}
	if record.Payload.KeyID != credentialTestActiveKeyID {
		t.Fatalf("stored key ID = %q, want %q", record.Payload.KeyID, credentialTestActiveKeyID)
	}
	resolved, err := service.Credential(context.Background(), application, credentialTestKind)
	if err != nil {
		t.Fatalf("Credential() error = %v", err)
	}
	if string(resolved.Bytes()) != credentialTestSecret {
		t.Fatalf("Credential() payload size = %d, want original payload", len(resolved.Bytes()))
	}

	wrongProvider := application
	wrongProvider.Store.Provider = core.ProviderGooglePlay
	if _, err := service.Credential(context.Background(), wrongProvider, credentialTestKind); !errors.Is(err, stores.ErrCredentialNotFound) {
		t.Fatalf("Credential() wrong provider error = %v, want ErrCredentialNotFound", err)
	}
}

// TestServiceCredentialWritesAreIdempotentAndOptimistic verifies retry and conflict semantics.
func TestServiceCredentialWritesAreIdempotentAndOptimistic(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	service := newCredentialService(t, store, newKeyring(t, credentialTestActiveKeyID, true))
	application := credentialApplication()
	original := newCredential(t, credentialTestSecret)
	rotated := newCredential(t, credentialTestRotatedSecret)

	created, err := service.Put(context.Background(), application, original, 0)
	if err != nil {
		t.Fatalf("create Put() error = %v", err)
	}
	retried, err := service.Put(context.Background(), application, original, 0)
	if err != nil {
		t.Fatalf("retry Put() error = %v", err)
	}
	if retried.Revision != created.Revision {
		t.Fatalf("retry revision = %d, want %d", retried.Revision, created.Revision)
	}
	if _, err := service.Put(context.Background(), application, rotated, 0); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("conflicting create error = %v, want ErrConflict", err)
	}

	updated, err := service.Put(context.Background(), application, rotated, created.Revision)
	if err != nil {
		t.Fatalf("rotate Put() error = %v", err)
	}
	if updated.Revision != 2 {
		t.Fatalf("rotated revision = %d, want 2", updated.Revision)
	}
	retriedUpdate, err := service.Put(context.Background(), application, rotated, created.Revision)
	if err != nil {
		t.Fatalf("retry rotated Put() error = %v", err)
	}
	if retriedUpdate.Revision != updated.Revision {
		t.Fatalf("retry rotated revision = %d, want %d", retriedUpdate.Revision, updated.Revision)
	}
	if _, err := service.Put(context.Background(), application, original, created.Revision); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale different Put() error = %v, want ErrConflict", err)
	}
}

// TestServiceSupportsEncryptionKeyRotation verifies retained reads and explicit re-encryption.
func TestServiceSupportsEncryptionKeyRotation(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	oldService := newCredentialService(t, store, newKeyring(t, credentialTestOldKeyID, true))
	application := credentialApplication()
	credential := newCredential(t, credentialTestSecret)
	created, err := oldService.Put(context.Background(), application, credential, 0)
	if err != nil {
		t.Fatalf("old Put() error = %v", err)
	}
	oldRecord := store.records[credentialKey()].Clone()

	activeOnlyStore := store.clone()
	activeOnlyService := newCredentialService(t, activeOnlyStore, newKeyring(t, credentialTestActiveKeyID, false))
	if _, err := activeOnlyService.Credential(
		context.Background(),
		application,
		credentialTestKind,
	); !errors.Is(err, protectionport.ErrKeyUnavailable) {
		t.Fatalf("Credential() without retained key error = %v, want ErrKeyUnavailable", err)
	}

	rotatedService := newCredentialService(t, store, newKeyring(t, credentialTestActiveKeyID, true))
	if _, err := rotatedService.Credential(context.Background(), application, credentialTestKind); err != nil {
		t.Fatalf("Credential() with retained key error = %v", err)
	}
	rotated, err := rotatedService.Put(context.Background(), application, credential, created.Revision)
	if err != nil {
		t.Fatalf("re-encryption Put() error = %v", err)
	}
	newRecord := store.records[credentialKey()]
	if rotated.Revision != 2 || newRecord.Payload.KeyID != credentialTestActiveKeyID {
		t.Fatalf("rotated record = %#v", newRecord)
	}
	if newRecord.Payload.Fingerprint != oldRecord.Payload.Fingerprint {
		t.Fatal("credential fingerprint changed during encryption-key rotation")
	}
	postRotationService := newCredentialService(t, store, newKeyring(t, credentialTestActiveKeyID, false))
	if _, err := postRotationService.Credential(context.Background(), application, credentialTestKind); err != nil {
		t.Fatalf("Credential() after old-key removal error = %v", err)
	}
}

// TestServiceRejectsTamperedCredentials verifies generic authentication failures for stored data.
func TestServiceRejectsTamperedCredentials(t *testing.T) {
	t.Parallel()

	baseStore := newFakeStore()
	keyring := newKeyring(t, credentialTestActiveKeyID, true)
	service := newCredentialService(t, baseStore, keyring)
	if _, err := service.Put(
		context.Background(),
		credentialApplication(),
		newCredential(t, credentialTestSecret),
		0,
	); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	tests := []struct {
		name     string
		expected error
		mutate   func(*persistence.CredentialRecord)
	}{
		{
			name:     "ciphertext",
			expected: protectionport.ErrOpenFailed,
			mutate: func(record *persistence.CredentialRecord) {
				record.Payload.Ciphertext[len(record.Payload.Ciphertext)-1] ^= 0xff
			},
		},
		{
			name:     "fingerprint",
			expected: protectionport.ErrOpenFailed,
			mutate: func(record *persistence.CredentialRecord) {
				record.Payload.Fingerprint[0] ^= 0xff
			},
		},
		{
			name:     "unknown key",
			expected: protectionport.ErrKeyUnavailable,
			mutate: func(record *persistence.CredentialRecord) {
				record.Payload.KeyID = "unknown-key"
			},
		},
		{
			name:     "content type",
			expected: protectionport.ErrOpenFailed,
			mutate: func(record *persistence.CredentialRecord) {
				record.ContentType = "application/vnd.iapstack.other-credentials+json"
			},
		},
		{
			name:     "schema version",
			expected: protectionport.ErrOpenFailed,
			mutate: func(record *persistence.CredentialRecord) {
				record.SchemaVersion++
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := baseStore.clone()
			record := store.records[credentialKey()].Clone()
			test.mutate(&record)
			store.records[credentialKey()] = record
			tamperedService := newCredentialService(t, store, keyring)
			_, err := tamperedService.Credential(
				context.Background(),
				credentialApplication(),
				credentialTestKind,
			)
			if !errors.Is(err, test.expected) {
				t.Fatalf("Credential() error = %v, want %v", err, test.expected)
			}
			if bytes.Contains([]byte(err.Error()), []byte("never-log-this-secret")) {
				t.Fatalf("Credential() error exposed plaintext: %v", err)
			}
		})
	}

	providerStore := baseStore.clone()
	providerStore.application.Store.Provider = core.ProviderGooglePlay
	providerService := newCredentialService(t, providerStore, keyring)
	_, err := providerService.Credential(
		context.Background(),
		providerStore.application,
		credentialTestKind,
	)
	if !errors.Is(err, protectionport.ErrOpenFailed) {
		t.Fatalf("Credential() changed provider scope error = %v, want ErrOpenFailed", err)
	}
}

// Ping reports that the in-memory service test store is available.
func (store *fakeStore) Ping(context.Context) error {
	return nil
}

// Transact applies one copy-on-commit credential transaction.
func (store *fakeStore) Transact(_ context.Context, operation persistence.TransactionFunc) error {
	working := &fakeTransaction{
		application: store.application,
		records:     cloneCredentialRecords(store.records),
	}
	if err := operation(working); err != nil {
		return err
	}
	store.records = working.records
	return nil
}

// Close releases no resources for the in-memory service test store.
func (store *fakeStore) Close() {}

// Application returns the authoritative application in its exact project scope.
func (repository *fakeTransaction) Application(
	_ context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) (core.Application, error) {
	if repository.application.ProjectID != projectID || repository.application.ID != applicationID {
		return core.Application{}, persistence.ErrNotFound
	}
	return repository.application, nil
}

// Credential returns one defensive protected credential copy.
func (repository *fakeTransaction) Credential(
	_ context.Context,
	key persistence.CredentialKey,
) (persistence.CredentialRecord, error) {
	record, exists := repository.records[key]
	if !exists {
		return persistence.CredentialRecord{}, persistence.ErrNotFound
	}
	return record.Clone(), nil
}

// PutCredential creates, rotates, retries, or conflicts one fake credential revision.
func (repository *fakeTransaction) PutCredential(
	_ context.Context,
	write persistence.CredentialWrite,
) (persistence.CredentialRecord, error) {
	record, exists := repository.records[write.CredentialKey]
	if !exists {
		if write.ExpectedRevision != 0 {
			return persistence.CredentialRecord{}, persistence.ErrNotFound
		}
		createdAt := time.Date(2026, time.August, 24, 3, 0, 0, 0, time.UTC)
		record = persistence.CredentialRecord{
			CredentialKey: write.CredentialKey,
			ContentType:   write.ContentType,
			SchemaVersion: write.SchemaVersion,
			Payload:       write.Payload.Clone(),
			Revision:      1,
			CreatedAt:     createdAt,
			UpdatedAt:     createdAt,
		}
		repository.records[write.CredentialKey] = record
		return record.Clone(), nil
	}
	if write.ExpectedRevision != record.Revision {
		if fakeCredentialWriteMatches(record, write) {
			return record.Clone(), nil
		}
		return persistence.CredentialRecord{}, persistence.ErrConflict
	}
	record.ContentType = write.ContentType
	record.SchemaVersion = write.SchemaVersion
	record.Payload = write.Payload.Clone()
	record.Revision++
	record.UpdatedAt = record.UpdatedAt.Add(time.Second)
	repository.records[write.CredentialKey] = record
	return record.Clone(), nil
}

// newFakeStore returns one empty application-scoped credential repository.
func newFakeStore() *fakeStore {
	return &fakeStore{
		application: credentialApplication(),
		records:     make(map[persistence.CredentialKey]persistence.CredentialRecord),
	}
}

// clone returns an independent copy of one fake store and its protected bytes.
func (store *fakeStore) clone() *fakeStore {
	return &fakeStore{
		application: store.application,
		records:     cloneCredentialRecords(store.records),
	}
}

// cloneCredentialRecords returns an independent protected record map.
func cloneCredentialRecords(
	records map[persistence.CredentialKey]persistence.CredentialRecord,
) map[persistence.CredentialKey]persistence.CredentialRecord {
	clone := make(map[persistence.CredentialKey]persistence.CredentialRecord, len(records))
	for key, record := range records {
		clone[key] = record.Clone()
	}
	return clone
}

// fakeCredentialWriteMatches reports logical equality without comparing randomized ciphertext.
func fakeCredentialWriteMatches(
	record persistence.CredentialRecord,
	write persistence.CredentialWrite,
) bool {
	return record.CredentialKey == write.CredentialKey &&
		record.ContentType == write.ContentType &&
		record.SchemaVersion == write.SchemaVersion &&
		record.Payload.Fingerprint == write.Payload.Fingerprint
}

// credentialApplication returns one authoritative Huawei application fixture.
func credentialApplication() core.Application {
	return core.Application{
		ID:        credentialTestApplicationID,
		ProjectID: credentialTestProjectID,
		Store: core.StoreApplication{
			Provider:    core.ProviderHuaweiAppGallery,
			Environment: core.EnvironmentSandbox,
			ID:          credentialTestProviderApplicationID,
		},
	}
}

// credentialKey returns the persistence identity of the test credential package.
func credentialKey() persistence.CredentialKey {
	return persistence.CredentialKey{
		ProjectID:     credentialTestProjectID,
		ApplicationID: credentialTestApplicationID,
		Kind:          string(credentialTestKind),
	}
}

// newCredential returns one valid opaque provider credential or fails the test.
func newCredential(t *testing.T, payload string) stores.Credential {
	t.Helper()
	credential, err := stores.NewCredential(
		credentialTestKind,
		credentialTestContentType,
		1,
		[]byte(payload),
	)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	return credential
}

// newCredentialService assembles a credential service or fails the test.
func newCredentialService(
	t *testing.T,
	store persistence.Store,
	keyring protectionport.Service,
) *credentials.Service {
	t.Helper()
	service, err := credentials.NewService(store, keyring)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

// newKeyring returns deterministic encryption roots with an independently stable fingerprint key.
func newKeyring(
	t *testing.T,
	activeKeyID string,
	includeOldKey bool,
) *platformprotection.Keyring {
	t.Helper()
	keys := map[string][]byte{
		credentialTestActiveKeyID: bytes.Repeat([]byte{0x22}, credentialTestKeySize),
	}
	if includeOldKey {
		keys[credentialTestOldKeyID] = bytes.Repeat([]byte{0x11}, credentialTestKeySize)
	}
	config, err := platformprotection.NewConfig(
		activeKeyID,
		keys,
		bytes.Repeat([]byte{0x33}, credentialTestKeySize),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	keyring, err := platformprotection.New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return keyring
}
