package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	platformprotection "github.com/imariman/iapstack/internal/platform/protection"
)

const (
	// testBootstrapAdminKey is a sufficiently long installation credential used by authentication tests.
	testBootstrapAdminKey = "bootstrap-administrator-secret-32-bytes"
)

// fakeOperationsStore retains API key verifiers for authentication tests.
type fakeOperationsStore struct {
	records map[string]persistence.APIKeyRecord
	err     error
}

// fakeOperationsTransaction embeds unused repositories and implements API key operations.
type fakeOperationsTransaction struct {
	persistence.OperationsTransaction
	records map[string]persistence.APIKeyRecord
}

// TestAuthenticationPreservesOperationalLoadFailures verifies storage outages are not reported as invalid keys.
func TestAuthenticationPreservesOperationalLoadFailures(t *testing.T) {
	store := &fakeOperationsStore{records: make(map[string]persistence.APIKeyRecord)}
	service, err := NewService(store, "", 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	bearer, err := service.Create(context.Background(), Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-1", ApplicationID: "application-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	store.err = persistence.ErrUnavailable
	if _, err := service.Authenticate(context.Background(), bearer); !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("Authenticate() storage error = %v, want ErrUnavailable", err)
	}
	store.err = nil

	id, secret, err := parseBearer(bearer)
	if err != nil {
		t.Fatalf("parseBearer() error = %v", err)
	}
	zero(secret)
	record := store.records[id]
	record.SecretHash[0] ^= 0xff
	store.records[record.ID] = record
	if _, err := service.Authenticate(context.Background(), bearer); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate() hash mismatch error = %v, want ErrUnauthorized", err)
	}
	delete(store.records, record.ID)
	if _, err := service.Authenticate(context.Background(), bearer); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate() missing key error = %v, want ErrUnauthorized", err)
	}
}

// TestServiceCreatesAndAuthenticatesScopedKeys verifies one-time bearer creation and scope recovery.
func TestServiceCreatesAndAuthenticatesScopedKeys(t *testing.T) {
	store := &fakeOperationsStore{records: make(map[string]persistence.APIKeyRecord)}
	service, err := NewService(store, testBootstrapAdminKey, 4)
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
	if authenticated.KeyID == "" || authenticated.Role != principal.Role ||
		authenticated.ProjectID != principal.ProjectID || authenticated.ApplicationID != principal.ApplicationID {
		t.Fatalf("Authenticate() = %#v, want stored key identity with scope %#v", authenticated, principal)
	}
	if _, err := service.Authenticate(context.Background(), bearer+"tampered"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tampered Authenticate() error = %v, want ErrUnauthorized", err)
	}
	admin, err := service.Authenticate(context.Background(), testBootstrapAdminKey)
	if err != nil || admin.Role != persistence.APIKeyRoleAdmin {
		t.Fatalf("bootstrap Authenticate() = %#v, %v", admin, err)
	}
}

// TestNewServiceRejectsShortBootstrapKey verifies installation credentials meet the minimum brute-force boundary.
func TestNewServiceRejectsShortBootstrapKey(t *testing.T) {
	store := &fakeOperationsStore{records: make(map[string]persistence.APIKeyRecord)}
	if _, err := NewService(store, "bootstrap-secret", 4); err == nil {
		t.Fatal("NewService() short bootstrap key error = nil, want validation error")
	}
	if _, err := NewService(store, "", 0); err == nil {
		t.Fatal("NewService() zero derivation capacity error = nil, want validation error")
	}
}

// TestServiceBoundsConcurrentDerivations verifies excess memory-hard work fails fast and then recovers.
func TestServiceBoundsConcurrentDerivations(t *testing.T) {
	store := &fakeOperationsStore{records: make(map[string]persistence.APIKeyRecord)}
	service, err := NewService(store, "", 1)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	bearer, err := service.Create(context.Background(), Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-1", ApplicationID: "application-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	service.derivationSlots <- struct{}{}
	if _, err := service.Authenticate(context.Background(), bearer); !errors.Is(err, ErrCapacity) {
		t.Fatalf("Authenticate() saturated error = %v, want ErrCapacity", err)
	}
	<-service.derivationSlots
	if _, err := service.Authenticate(context.Background(), bearer); err != nil {
		t.Fatalf("Authenticate() recovered error = %v", err)
	}
}

// TestCustomerSessionsBindCustomerExpiryAndIssuer verifies the complete short-lived bearer boundary.
func TestCustomerSessionsBindCustomerExpiryAndIssuer(t *testing.T) {
	store := &fakeOperationsStore{records: make(map[string]persistence.APIKeyRecord)}
	apiKeys, err := NewService(store, "", 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	bearer, err := apiKeys.Create(context.Background(), Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-1", ApplicationID: "application-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	issuer, err := apiKeys.Authenticate(context.Background(), bearer)
	if err != nil {
		t.Fatalf("Authenticate() issuer error = %v", err)
	}
	keyring := newCustomerSessionKeyring(t)
	sessions, err := NewCustomerSessions(store, keyring)
	if err != nil {
		t.Fatalf("NewCustomerSessions() error = %v", err)
	}
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	sessions.clock = func() time.Time { return now }

	session, err := sessions.Mint(context.Background(), issuer, "customer-external")
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	if !strings.HasPrefix(session.Token, customerSessionPrefix) ||
		!session.ExpiresAt.Equal(now.Add(defaultCustomerSessionTTL)) {
		t.Fatalf("Mint() = %#v", session)
	}
	principal, err := sessions.Authenticate(context.Background(), session.Token)
	if err != nil {
		t.Fatalf("Authenticate() session error = %v", err)
	}
	if principal.ExternalCustomerID != "customer-external" || principal.KeyID != issuer.KeyID ||
		principal.ProjectID != issuer.ProjectID || principal.ApplicationID != issuer.ApplicationID {
		t.Fatalf("Authenticate() session = %#v", principal)
	}
	store.err = persistence.ErrUnavailable
	if _, err := sessions.Authenticate(context.Background(), session.Token); !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("Authenticate() session storage error = %v, want ErrUnavailable", err)
	}
	store.err = nil

	replacement := byte('A')
	if session.Token[len(session.Token)-1] == replacement {
		replacement = 'B'
	}
	tampered := session.Token[:len(session.Token)-1] + string(replacement)
	if _, err := sessions.Authenticate(context.Background(), tampered); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tampered session error = %v, want unauthorized", err)
	}
	sessions.clock = func() time.Time { return session.ExpiresAt }
	if _, err := sessions.Authenticate(context.Background(), session.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session error = %v, want unauthorized", err)
	}
	sessions.clock = func() time.Time { return now }
	delete(store.records, issuer.KeyID)
	if _, err := sessions.Authenticate(context.Background(), session.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked issuer session error = %v, want unauthorized", err)
	}
}

// TestAdminSessionsPersistWithoutRetainingBearers verifies protected dashboard sessions survive independently of plaintext admin keys.
func TestAdminSessionsPersistWithoutRetainingBearers(t *testing.T) {
	store := &fakeOperationsStore{records: make(map[string]persistence.APIKeyRecord)}
	apiKeys, err := NewService(store, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	storedBearer, err := apiKeys.Create(context.Background(), Principal{Role: persistence.APIKeyRoleAdmin})
	if err != nil {
		t.Fatalf("Create() administrator key error = %v", err)
	}
	storedIssuer, err := apiKeys.Authenticate(context.Background(), storedBearer)
	if err != nil {
		t.Fatalf("Authenticate() stored issuer error = %v", err)
	}
	bootstrapIssuer, err := apiKeys.Authenticate(context.Background(), testBootstrapAdminKey)
	if err != nil {
		t.Fatalf("Authenticate() bootstrap issuer error = %v", err)
	}

	sessions, err := NewAdminSessions(store, newCustomerSessionKeyring(t))
	if err != nil {
		t.Fatalf("NewAdminSessions() error = %v", err)
	}
	now := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	sessions.clock = func() time.Time { return now }

	storedSession, err := sessions.Mint(context.Background(), storedIssuer)
	if err != nil {
		t.Fatalf("Mint() stored session error = %v", err)
	}
	if !strings.HasPrefix(storedSession.Token, adminSessionPrefix) ||
		!storedSession.ExpiresAt.Equal(now.Add(defaultAdminSessionTTL)) ||
		strings.Contains(storedSession.Token, storedBearer) {
		t.Fatalf("Mint() stored session metadata is invalid")
	}
	storedPrincipal, err := sessions.Authenticate(context.Background(), storedSession.Token)
	if err != nil {
		t.Fatalf("Authenticate() stored session error = %v", err)
	}
	if storedPrincipal.KeyID != storedIssuer.KeyID || storedPrincipal.Role != persistence.APIKeyRoleAdmin {
		t.Fatalf("Authenticate() stored principal = %#v", storedPrincipal)
	}
	store.err = persistence.ErrUnavailable
	if _, err := sessions.Authenticate(context.Background(), storedSession.Token); !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("Authenticate() administrator session storage error = %v, want ErrUnavailable", err)
	}
	store.err = nil

	bootstrapSession, err := sessions.Mint(context.Background(), bootstrapIssuer)
	if err != nil {
		t.Fatalf("Mint() bootstrap session error = %v", err)
	}
	bootstrapPrincipal, err := sessions.Authenticate(context.Background(), bootstrapSession.Token)
	if err != nil || bootstrapPrincipal.KeyID != "" || bootstrapPrincipal.Role != persistence.APIKeyRoleAdmin {
		t.Fatalf("Authenticate() bootstrap principal = %#v, %v", bootstrapPrincipal, err)
	}

	replacement := byte('A')
	if storedSession.Token[len(storedSession.Token)-1] == replacement {
		replacement = 'B'
	}
	tampered := storedSession.Token[:len(storedSession.Token)-1] + string(replacement)
	if _, err := sessions.Authenticate(context.Background(), tampered); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("tampered administrator session error = %v, want unauthorized", err)
	}
	sessions.clock = func() time.Time { return storedSession.ExpiresAt }
	if _, err := sessions.Authenticate(context.Background(), storedSession.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired administrator session error = %v, want unauthorized", err)
	}
	sessions.clock = func() time.Time { return now }
	delete(store.records, storedIssuer.KeyID)
	if _, err := sessions.Authenticate(context.Background(), storedSession.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked issuer session error = %v, want unauthorized", err)
	}
}

// TestAdminSessionsRejectApplicationIssuers verifies application keys cannot gain dashboard access.
func TestAdminSessionsRejectApplicationIssuers(t *testing.T) {
	store := &fakeOperationsStore{records: make(map[string]persistence.APIKeyRecord)}
	sessions, err := NewAdminSessions(store, newCustomerSessionKeyring(t))
	if err != nil {
		t.Fatalf("NewAdminSessions() error = %v", err)
	}
	_, err = sessions.Mint(context.Background(), Principal{
		KeyID: "application-key", Role: persistence.APIKeyRoleApplication,
		ProjectID: "project-1", ApplicationID: "application-1",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Mint() application issuer error = %v, want unauthorized", err)
	}
}

// newCustomerSessionKeyring constructs deterministic root material for session tests.
func newCustomerSessionKeyring(t *testing.T) *platformprotection.Keyring {
	t.Helper()

	config, err := platformprotection.NewConfig(
		"session-key", map[string][]byte{"session-key": bytes.Repeat([]byte{1}, 32)}, bytes.Repeat([]byte{2}, 32),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	keyring, err := platformprotection.New(config)
	if err != nil {
		t.Fatalf("New() keyring error = %v", err)
	}
	return keyring
}

// Operate executes one fake atomic operations callback.
func (store *fakeOperationsStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	if store.err != nil {
		return store.err
	}
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
