package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	platformprotection "github.com/imariman/iapstack/internal/platform/protection"
)

// identityAPIStore records alias and merge calls for handler tests.
type identityAPIStore struct {
	persistence.Store
	resolution persistence.CustomerResolution
	merge      persistence.CustomerMerge
	err        error
	aliasCalls int
	mergeCalls int
}

// identityAPITransaction serves the identity methods exercised by the HTTP handlers.
type identityAPITransaction struct {
	persistence.Transaction
	store *identityAPIStore
}

// TestCustomerIdentityHTTPRejectsTheWrongPrincipals verifies auth boundaries before any identity write.
func TestCustomerIdentityHTTPRejectsTheWrongPrincipals(t *testing.T) {
	t.Parallel()

	keys := &keyLifecycleStore{
		records: make(map[string]persistence.APIKeyRecord), revokedAt: make(map[string]time.Time),
	}
	authentication, err := auth.NewService(keys, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adminKey, err := authentication.Create(context.Background(), auth.Principal{Role: persistence.APIKeyRoleAdmin})
	if err != nil {
		t.Fatalf("Create() admin key error = %v", err)
	}
	applicationKey, err := authentication.Create(context.Background(), auth.Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-1", ApplicationID: "application-1",
	})
	if err != nil {
		t.Fatalf("Create() application key error = %v", err)
	}
	sessions, err := auth.NewCustomerSessions(keys, newIdentityTestKeyring(t))
	if err != nil {
		t.Fatalf("NewCustomerSessions() error = %v", err)
	}
	applicationPrincipal, err := authentication.Authenticate(context.Background(), applicationKey)
	if err != nil {
		t.Fatalf("Authenticate() application key error = %v", err)
	}
	session, err := sessions.Mint(context.Background(), applicationPrincipal, "customer-external")
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	store := &identityAPIStore{resolution: persistence.CustomerResolution{
		ProjectID: "project-1", ExternalID: "alias-external", CanonicalCustomerID: "customer-1",
		Match: persistence.CustomerMatchAlias,
	}}
	api := &API{
		store: store, authentication: authentication, customerSessions: sessions,
		bodyLimit: 1 << 20, clock: func() time.Time { return time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC) },
	}

	bootstrap := identityRequest(t, http.MethodPut, "/v1/admin/projects/project-1/customers/customer-1/aliases/alias-external", testBootstrapAdminKey, `{}`)
	bootstrap.SetPathValue("project_id", "project-1")
	bootstrap.SetPathValue("customer_id", "customer-1")
	bootstrap.SetPathValue("external_id", "alias-external")
	bootstrapRecorder := httptest.NewRecorder()
	api.putAdminCustomerAlias(bootstrapRecorder, bootstrap)
	if bootstrapRecorder.Code != http.StatusForbidden || !strings.Contains(bootstrapRecorder.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("bootstrap alias = %d %s", bootstrapRecorder.Code, bootstrapRecorder.Body.String())
	}

	sessionRequest := identityRequest(t, http.MethodPut, "/v1/admin/projects/project-1/customers/customer-1/aliases/alias-external", session.Token, `{}`)
	sessionRequest.SetPathValue("project_id", "project-1")
	sessionRequest.SetPathValue("customer_id", "customer-1")
	sessionRequest.SetPathValue("external_id", "alias-external")
	sessionRecorder := httptest.NewRecorder()
	api.putAdminCustomerAlias(sessionRecorder, sessionRequest)
	if sessionRecorder.Code != http.StatusUnauthorized || !strings.Contains(sessionRecorder.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("customer session alias = %d %s", sessionRecorder.Code, sessionRecorder.Body.String())
	}

	applicationMerge := identityRequest(t, http.MethodPost, "/v1/admin/projects/project-1/customer-merges", applicationKey, `{
		"idempotency_key":"merge-1","canonical_customer_id":"customer-1","source_customer_id":"customer-2"
	}`)
	applicationMerge.SetPathValue("project_id", "project-1")
	applicationRecorder := httptest.NewRecorder()
	api.postAdminCustomerMerge(applicationRecorder, applicationMerge)
	if applicationRecorder.Code != http.StatusUnauthorized || !strings.Contains(applicationRecorder.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("application merge = %d %s", applicationRecorder.Code, applicationRecorder.Body.String())
	}

	invalidMerge := identityRequest(t, http.MethodPost, "/v1/admin/projects/project-1/customer-merges", adminKey, `{
		"idempotency_key":"merge-1","canonical_customer_id":"customer-1","source_customer_id":"customer-1"
	}`)
	invalidMerge.SetPathValue("project_id", "project-1")
	invalidRecorder := httptest.NewRecorder()
	api.postAdminCustomerMerge(invalidRecorder, invalidMerge)
	if invalidRecorder.Code != http.StatusBadRequest || !strings.Contains(invalidRecorder.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("identical merge = %d %s", invalidRecorder.Code, invalidRecorder.Body.String())
	}

	alias := identityRequest(t, http.MethodPut, "/v1/admin/projects/project-1/customers/customer-1/aliases/alias-external", adminKey, `{}`)
	alias.SetPathValue("project_id", "project-1")
	alias.SetPathValue("customer_id", "customer-1")
	alias.SetPathValue("external_id", "alias-external")
	aliasRecorder := httptest.NewRecorder()
	api.putAdminCustomerAlias(aliasRecorder, alias)
	if aliasRecorder.Code != http.StatusOK || !strings.Contains(aliasRecorder.Body.String(), `"match":"alias"`) {
		t.Fatalf("admin alias = %d %s", aliasRecorder.Code, aliasRecorder.Body.String())
	}
	if store.aliasCalls != 1 {
		t.Fatalf("alias calls = %d, want 1", store.aliasCalls)
	}

	store.err = persistence.ErrNotFound
	missing := identityRequest(t, http.MethodGet, "/v1/applications/application-1/customer-resolutions/missing-external", applicationKey, "")
	missing.SetPathValue("application_id", "application-1")
	missing.SetPathValue("external_id", "missing-external")
	missingRecorder := httptest.NewRecorder()
	api.getApplicationCustomerResolution(missingRecorder, missing)
	if missingRecorder.Code != http.StatusNotFound || !strings.Contains(missingRecorder.Body.String(), `"code":"not_found"`) {
		t.Fatalf("cross-scope resolution = %d %s", missingRecorder.Code, missingRecorder.Body.String())
	}

	store.err = persistence.ErrEntitlementConflict
	conflict := identityRequest(t, http.MethodPost, "/v1/admin/projects/project-1/customer-merges", adminKey, `{
		"idempotency_key":"merge-2","canonical_customer_id":"customer-1","source_customer_id":"customer-2"
	}`)
	conflict.SetPathValue("project_id", "project-1")
	conflictRecorder := httptest.NewRecorder()
	api.postAdminCustomerMerge(conflictRecorder, conflict)
	if conflictRecorder.Code != http.StatusConflict || !strings.Contains(conflictRecorder.Body.String(), `"code":"entitlement_conflict"`) ||
		!strings.Contains(conflictRecorder.Body.String(), `"message":"the customers have conflicting active entitlements"`) {
		t.Fatalf("entitlement conflict = %d %s", conflictRecorder.Code, conflictRecorder.Body.String())
	}
}

// Transact runs one identity callback against the fake transaction.
func (store *identityAPIStore) Transact(_ context.Context, operation persistence.TransactionFunc) error {
	return operation(&identityAPITransaction{store: store})
}

// Application returns the application principal's project scope.
func (transaction *identityAPITransaction) Application(
	_ context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) (core.Application, error) {
	if projectID != "project-1" || applicationID != "application-1" {
		return core.Application{}, persistence.ErrNotFound
	}
	return core.Application{
		ID: applicationID, ProjectID: projectID,
		Store: core.StoreApplication{
			Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest, ID: "com.example.test",
		},
	}, nil
}

// PutCustomerAlias returns the configured resolution or injected failure.
func (transaction *identityAPITransaction) PutCustomerAlias(
	context.Context,
	persistence.CustomerAliasWrite,
) (persistence.CustomerResolution, error) {
	transaction.store.aliasCalls++
	if transaction.store.err != nil {
		return persistence.CustomerResolution{}, transaction.store.err
	}
	return transaction.store.resolution, nil
}

// ResolveCustomer returns the configured resolution or injected failure.
func (transaction *identityAPITransaction) ResolveCustomer(
	context.Context,
	core.ProjectID,
	string,
) (persistence.CustomerResolution, error) {
	if transaction.store.err != nil {
		return persistence.CustomerResolution{}, transaction.store.err
	}
	return transaction.store.resolution, nil
}

// MergeCustomers returns the configured audit row or injected failure.
func (transaction *identityAPITransaction) MergeCustomers(
	context.Context,
	persistence.CustomerMergeWrite,
) (persistence.CustomerMergeResult, error) {
	transaction.store.mergeCalls++
	if transaction.store.err != nil {
		return persistence.CustomerMergeResult{}, transaction.store.err
	}
	return persistence.CustomerMergeResult{Merge: transaction.store.merge}, nil
}

// identityRequest builds one JSON request with a bearer and optional body.
func identityRequest(t *testing.T, method, path, bearer, body string) *http.Request {
	t.Helper()
	var reader *bytes.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Authorization", "Bearer "+bearer)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

// newIdentityTestKeyring constructs deterministic customer-session root material.
func newIdentityTestKeyring(t *testing.T) *platformprotection.Keyring {
	t.Helper()
	config, err := platformprotection.NewConfig(
		"identity-key",
		map[string][]byte{"identity-key": bytes.Repeat([]byte{3}, 32)},
		bytes.Repeat([]byte{4}, 32),
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
