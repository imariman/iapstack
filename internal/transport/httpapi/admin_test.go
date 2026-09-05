package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// testBootstrapAdminKey is a sufficiently long installation credential used by HTTP API tests.
	testBootstrapAdminKey = "bootstrap-administrator-secret-32-bytes"
)

// fakeAdminQueryStore provides deterministic secret-free dashboard records.
type fakeAdminQueryStore struct{}

// authOperationsStore satisfies authentication construction for bootstrap-only tests.
type authOperationsStore struct{}

// keyLifecycleStore retains active and revoked API keys for HTTP lifecycle tests.
type keyLifecycleStore struct {
	records      map[string]persistence.APIKeyRecord
	revokedAt    map[string]time.Time
	operateError error
}

// keyLifecycleTransaction implements the API key repository against deterministic test state.
type keyLifecycleTransaction struct {
	persistence.OperationsTransaction
	store *keyLifecycleStore
}

// fakeAdminAnalytics returns one complete fixed-size dashboard activity window.
func fakeAdminAnalytics() persistence.AdminAnalytics {
	generatedAt := time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)
	zero := int64(0)
	activity := make([]persistence.AdminDailyActivity, 0, 30)
	for offset := 29; offset >= 0; offset-- {
		activity = append(activity, persistence.AdminDailyActivity{
			Date: generatedAt.AddDate(0, 0, -offset).Format(time.DateOnly),
		})
	}
	return persistence.AdminAnalytics{
		WindowDays: 30, GeneratedAt: generatedAt,
		Revenue: persistence.AdminRevenue{
			Status: persistence.AdminRevenueSandboxOnly, RecognizedMinorUnits: &zero,
			TestApplicationCount: 1,
		},
		SandboxValue: persistence.AdminSandboxValue{
			Amounts:          []persistence.AdminMoney{{Milliunits: 4990, Currency: "USD"}},
			TransactionCount: 1,
		},
		DailyActivity: activity,
	}
}

// AdminProjects returns one deterministic dashboard project.
func (fakeAdminQueryStore) AdminProjects(context.Context) ([]persistence.AdminProject, error) {
	return []persistence.AdminProject{{
		ID: "project-1", ApplicationCount: 1, CustomerCount: 2, ProductCount: 3,
		CreatedAt: time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC),
	}}, nil
}

// AdminProjectOverview returns one empty bounded overview for the requested project.
func (fakeAdminQueryStore) AdminProjectOverview(
	_ context.Context,
	projectID core.ProjectID,
) (persistence.AdminProjectOverview, error) {
	return persistence.AdminProjectOverview{
		Project:      persistence.AdminProject{ID: projectID, CreatedAt: time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)},
		Analytics:    fakeAdminAnalytics(),
		Applications: []persistence.AdminApplication{}, Products: []persistence.AdminProduct{},
		Customers: []persistence.AdminCustomer{}, RecentTransactions: []persistence.AdminTransaction{},
		Queues: []persistence.AdminQueue{}, RecentWebhookEvents: []persistence.AdminWebhookEvent{},
	}, nil
}

// Operate rejects durable work because bootstrap authentication does not require it.
func (authOperationsStore) Operate(context.Context, persistence.OperationsFunc) error {
	return persistence.ErrUnavailable
}

// TestAdminAPIKeyLifecycleListsAndSafelyRevokes verifies secret-free rotation and current/final-admin guards.
func TestAdminAPIKeyLifecycleListsAndSafelyRevokes(t *testing.T) {
	store := &keyLifecycleStore{
		records: make(map[string]persistence.APIKeyRecord), revokedAt: make(map[string]time.Time),
	}
	authentication, err := auth.NewService(store, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	firstAdmin, err := authentication.Create(context.Background(), auth.Principal{Role: persistence.APIKeyRoleAdmin})
	if err != nil {
		t.Fatalf("Create() first admin error = %v", err)
	}
	secondAdmin, err := authentication.Create(context.Background(), auth.Principal{Role: persistence.APIKeyRoleAdmin})
	if err != nil {
		t.Fatalf("Create() second admin error = %v", err)
	}
	applicationKey, err := authentication.Create(context.Background(), auth.Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-1", ApplicationID: "application-1",
	})
	if err != nil {
		t.Fatalf("Create() application key error = %v", err)
	}
	api := &API{authentication: authentication}

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/api-keys", nil)
	listRequest.Header.Set("Authorization", "Bearer "+firstAdmin)
	listRecorder := httptest.NewRecorder()
	api.listAPIKeys(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d: %s", listRecorder.Code, http.StatusOK, listRecorder.Body.String())
	}
	var collection apiKeyCollectionResponse
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &collection); err != nil {
		t.Fatalf("decode key collection: %v", err)
	}
	if len(collection.APIKeys) != 3 {
		t.Fatalf("API key collection = %#v, want three summaries", collection.APIKeys)
	}
	firstAdminID := bearerKeyID(firstAdmin)
	secondAdminID := bearerKeyID(secondAdmin)
	applicationKeyID := bearerKeyID(applicationKey)
	if !containsCurrentAPIKey(collection.APIKeys, firstAdminID) {
		t.Fatalf("API key collection = %#v, want current key %q", collection.APIKeys, firstAdminID)
	}
	for _, forbidden := range []string{"secret", "salt", "hash", "verifier", firstAdmin, secondAdmin, applicationKey} {
		if strings.Contains(listRecorder.Body.String(), forbidden) {
			t.Fatalf("API key collection contains forbidden value %q: %s", forbidden, listRecorder.Body.String())
		}
	}

	currentRequest := httptest.NewRequest(http.MethodDelete, "/v1/admin/api-keys/"+firstAdminID, nil)
	currentRequest.SetPathValue("key_id", firstAdminID)
	currentRequest.Header.Set("Authorization", "Bearer "+firstAdmin)
	currentRecorder := httptest.NewRecorder()
	api.revokeAPIKey(currentRecorder, currentRequest)
	if currentRecorder.Code != http.StatusConflict {
		t.Fatalf("current revoke status = %d, want %d", currentRecorder.Code, http.StatusConflict)
	}

	applicationRequest := httptest.NewRequest(http.MethodDelete, "/v1/admin/api-keys/"+applicationKeyID, nil)
	applicationRequest.SetPathValue("key_id", applicationKeyID)
	applicationRequest.Header.Set("Authorization", "Bearer "+firstAdmin)
	applicationRecorder := httptest.NewRecorder()
	api.revokeAPIKey(applicationRecorder, applicationRequest)
	if applicationRecorder.Code != http.StatusOK {
		t.Fatalf("application revoke status = %d, want %d", applicationRecorder.Code, http.StatusOK)
	}

	secondRequest := httptest.NewRequest(http.MethodDelete, "/v1/admin/api-keys/"+secondAdminID, nil)
	secondRequest.SetPathValue("key_id", secondAdminID)
	secondRequest.Header.Set("Authorization", "Bearer "+firstAdmin)
	secondRecorder := httptest.NewRecorder()
	api.revokeAPIKey(secondRecorder, secondRequest)
	if secondRecorder.Code != http.StatusOK {
		t.Fatalf("second admin revoke status = %d, want %d", secondRecorder.Code, http.StatusOK)
	}
	if _, err := authentication.Authenticate(context.Background(), secondAdmin); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("revoked Authenticate() error = %v, want ErrUnauthorized", err)
	}

	finalRequest := httptest.NewRequest(http.MethodDelete, "/v1/admin/api-keys/"+firstAdminID, nil)
	finalRequest.SetPathValue("key_id", firstAdminID)
	finalRequest.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
	finalRecorder := httptest.NewRecorder()
	api.revokeAPIKey(finalRecorder, finalRequest)
	if finalRecorder.Code != http.StatusConflict {
		t.Fatalf("final admin revoke status = %d, want %d", finalRecorder.Code, http.StatusConflict)
	}
}

// TestAdminProjectsRequiresAdminAndReturnsStableJSON verifies authorization and the safe collection contract.
func TestAdminProjectsRequiresAdminAndReturnsStableJSON(t *testing.T) {
	t.Parallel()

	authentication, err := auth.NewService(authOperationsStore{}, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	api := &API{admin: fakeAdminQueryStore{}, authentication: authentication}

	unauthorized := httptest.NewRecorder()
	api.adminProjects(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/admin/projects", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/admin/projects", nil)
	request.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
	recorder := httptest.NewRecorder()
	api.adminProjects(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response adminProjectsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Projects) != 1 || response.Projects[0].ID != "project-1" {
		t.Fatalf("projects response = %#v, want deterministic project", response)
	}
	for _, forbidden := range []string{"ciphertext", "fingerprint", "secret", "payload", "token"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), forbidden) {
			t.Fatalf("projects response contains forbidden field %q: %s", forbidden, recorder.Body.String())
		}
	}
}

// TestAuthenticationCapacityReturnsServiceUnavailable verifies overload is not misreported as invalid credentials.
func TestAuthenticationCapacityReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()

	api := &API{}
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/projects", nil)
	recorder := httptest.NewRecorder()
	api.writeAuthenticationFailure(recorder, request, auth.ErrCapacity)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("capacity status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"authentication_unavailable"`) {
		t.Fatalf("capacity response = %s", recorder.Body.String())
	}
}

// TestAuthenticationStorageFailureReturnsServiceUnavailable verifies database outages remain retryable at the HTTP boundary.
func TestAuthenticationStorageFailureReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()

	api := &API{}
	request := httptest.NewRequest(http.MethodGet, "/v1/admin/projects", nil)
	recorder := httptest.NewRecorder()
	api.writeAuthenticationFailure(recorder, request, persistence.ErrUnavailable)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("storage failure status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"authentication_unavailable"`) {
		t.Fatalf("storage failure response = %s", recorder.Body.String())
	}
}

// TestRequestDeadlineReturnsRetryableServiceUnavailable verifies bounded restore work has a stable response.
func TestRequestDeadlineReturnsRetryableServiceUnavailable(t *testing.T) {
	t.Parallel()

	api := &API{}
	request := httptest.NewRequest(http.MethodPost, "/v1/applications/application-1/purchases:restore", nil)
	recorder := httptest.NewRecorder()
	api.writeError(recorder, request, context.DeadlineExceeded)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("deadline status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"request_timeout"`) {
		t.Fatalf("deadline response = %s", recorder.Body.String())
	}
}

// TestWriteErrorRequiresTypedValidation verifies operational error wording cannot change HTTP status.
func TestWriteErrorRequiresTypedValidation(t *testing.T) {
	t.Parallel()

	api := &API{}
	request := httptest.NewRequest(http.MethodPut, "/v1/admin/projects/project-1", nil)
	typedRecorder := httptest.NewRecorder()
	api.writeError(typedRecorder, request, validation.Wrap(errors.New("opaque rejected value")))
	if typedRecorder.Code != http.StatusBadRequest ||
		!strings.Contains(typedRecorder.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("typed validation response = %d %s", typedRecorder.Code, typedRecorder.Body.String())
	}

	operationalRecorder := httptest.NewRecorder()
	api.writeError(operationalRecorder, request, errors.New("invalid connection state"))
	if operationalRecorder.Code != http.StatusInternalServerError ||
		!strings.Contains(operationalRecorder.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("operational response = %d %s", operationalRecorder.Code, operationalRecorder.Body.String())
	}
}

// Operate executes one deterministic API key lifecycle callback.
func (store *keyLifecycleStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	if store.operateError != nil {
		return store.operateError
	}
	return operation(&keyLifecycleTransaction{store: store})
}

// APIKey returns one active verifier by public identity.
func (transaction *keyLifecycleTransaction) APIKey(
	_ context.Context,
	id string,
) (persistence.APIKeyRecord, error) {
	record, exists := transaction.store.records[id]
	if !exists {
		return persistence.APIKeyRecord{}, persistence.ErrNotFound
	}
	if _, revoked := transaction.store.revokedAt[id]; revoked {
		return persistence.APIKeyRecord{}, persistence.ErrNotFound
	}
	return record, nil
}

// APIKeys returns newest-first secret-free lifecycle metadata.
func (transaction *keyLifecycleTransaction) APIKeys(context.Context) ([]persistence.APIKeySummary, error) {
	summaries := make([]persistence.APIKeySummary, 0, len(transaction.store.records))
	for id, record := range transaction.store.records {
		summary := keySummary(record, transaction.store.revokedAt[id])
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(left, right int) bool {
		if summaries[left].CreatedAt.Equal(summaries[right].CreatedAt) {
			return summaries[left].ID < summaries[right].ID
		}
		return summaries[left].CreatedAt.After(summaries[right].CreatedAt)
	})
	return summaries, nil
}

// PutAPIKey retains one verifier and rejects duplicate identities.
func (transaction *keyLifecycleTransaction) PutAPIKey(_ context.Context, record persistence.APIKeyRecord) error {
	if _, exists := transaction.store.records[record.ID]; exists {
		return persistence.ErrConflict
	}
	transaction.store.records[record.ID] = record
	return nil
}

// RevokeAPIKey idempotently records revocation while preserving the final active administrator.
func (transaction *keyLifecycleTransaction) RevokeAPIKey(
	_ context.Context,
	id string,
	revokedAt time.Time,
) (persistence.APIKeySummary, error) {
	record, exists := transaction.store.records[id]
	if !exists {
		return persistence.APIKeySummary{}, persistence.ErrNotFound
	}
	if existing, revoked := transaction.store.revokedAt[id]; revoked {
		return keySummary(record, existing), nil
	}
	if record.Role == persistence.APIKeyRoleAdmin {
		activeAdministrators := 0
		for candidateID, candidate := range transaction.store.records {
			_, revoked := transaction.store.revokedAt[candidateID]
			if candidate.Role == persistence.APIKeyRoleAdmin && !revoked {
				activeAdministrators++
			}
		}
		if activeAdministrators <= 1 {
			return persistence.APIKeySummary{}, persistence.ErrConflict
		}
	}
	transaction.store.revokedAt[id] = revokedAt
	return keySummary(record, revokedAt), nil
}

// bearerKeyID extracts the non-secret public identity from a generated test bearer.
func bearerKeyID(bearer string) string {
	identity, _, _ := strings.Cut(strings.TrimPrefix(bearer, "iap_"), ".")
	return identity
}

// containsCurrentAPIKey reports whether one response identifies the expected request key.
func containsCurrentAPIKey(values []apiKeyResponse, id string) bool {
	for _, value := range values {
		if value.ID == id && value.Current {
			return true
		}
	}
	return false
}

// keySummary converts one verifier record into lifecycle metadata for test responses.
func keySummary(record persistence.APIKeyRecord, revokedAt time.Time) persistence.APIKeySummary {
	var revoked *time.Time
	if !revokedAt.IsZero() {
		value := revokedAt
		revoked = &value
	}
	return persistence.APIKeySummary{
		ID: record.ID, Role: record.Role, ProjectID: record.ProjectID,
		ApplicationID: record.ApplicationID, CreatedAt: record.CreatedAt, RevokedAt: revoked,
	}
}

// TestAdminOverviewSerializesEmptyCollections verifies dashboard clients never receive null collections.
func TestAdminOverviewSerializesEmptyCollections(t *testing.T) {
	t.Parallel()

	overview := adminOverviewFromRecord(persistence.AdminProjectOverview{
		Project:   persistence.AdminProject{ID: "project-1", CreatedAt: time.Now().UTC()},
		Analytics: fakeAdminAnalytics(),
		Applications: []persistence.AdminApplication{{
			ID: "application-1", CredentialConfigured: true, CredentialRevision: 3,
			WebhookConfigured: true, WebhookRevision: 2,
		}}, Products: []persistence.AdminProduct{},
		Customers: []persistence.AdminCustomer{}, RecentTransactions: []persistence.AdminTransaction{},
		Queues: []persistence.AdminQueue{}, RecentWebhookEvents: []persistence.AdminWebhookEvent{},
	})
	payload, err := json.Marshal(overview)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, field := range []string{
		`"credential_revision":3`, `"webhook_revision":2`, `"products":[]`, `"customers":[]`,
		`"recent_transactions":[]`, `"queues":[]`, `"recent_webhook_events":[]`,
		`"status":"sandbox_only"`, `"recognized_minor_units":0`, `"daily_activity":[`,
		`"sandbox_value":{"amounts":[{"milliunits":4990,"currency":"USD"}],"transaction_count":1,"missing_price_count":0}`,
	} {
		if !strings.Contains(string(payload), field) {
			t.Fatalf("overview JSON = %s, want %s", payload, field)
		}
	}
	for _, forbidden := range []string{"ciphertext", "fingerprint", "signing_secret", "client_secret", "payload"} {
		if strings.Contains(strings.ToLower(string(payload)), forbidden) {
			t.Fatalf("overview JSON contains forbidden field %q: %s", forbidden, payload)
		}
	}
}
