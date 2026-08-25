package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/persistence"
	platformprotection "github.com/imariman/iapstack/internal/platform/protection"
)

// TestCustomerSessionEndpointBindsApplicationAndCustomer verifies the trusted-backend handoff boundary.
func TestCustomerSessionEndpointBindsApplicationAndCustomer(t *testing.T) {
	store := &keyLifecycleStore{
		records: make(map[string]persistence.APIKeyRecord), revokedAt: make(map[string]time.Time),
	}
	authentication, err := auth.NewService(store, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	applicationKey, err := authentication.Create(context.Background(), auth.Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-1", ApplicationID: "application-1",
	})
	if err != nil {
		t.Fatalf("Create() application key error = %v", err)
	}
	keyring := newCustomerSessionTestKeyring(t)
	sessions, err := auth.NewCustomerSessions(store, keyring)
	if err != nil {
		t.Fatalf("NewCustomerSessions() error = %v", err)
	}
	api := &API{authentication: authentication, customerSessions: sessions, bodyLimit: 1024}

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/applications/application-1/customer-sessions",
		strings.NewReader(`{"external_customer_id":"customer-external"}`),
	)
	request.SetPathValue("application_id", "application-1")
	request.Header.Set("Authorization", "Bearer "+applicationKey)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	api.createCustomerSession(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	var session customerSessionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode customer session: %v", err)
	}
	if session.Token == "" || session.ExpiresAt.IsZero() {
		t.Fatalf("customer session = %#v", session)
	}

	dataRequest := httptest.NewRequest(http.MethodGet, "/v1/applications/application-1/purchases:verify", nil)
	dataRequest.SetPathValue("application_id", "application-1")
	dataRequest.Header.Set("Authorization", "Bearer "+session.Token)
	dataRecorder := httptest.NewRecorder()
	principal, ok := api.requireCustomerSession(dataRecorder, dataRequest)
	if !ok || principal.ExternalCustomerID != "customer-external" {
		t.Fatalf("customer principal = %#v, ok = %t: %s", principal, ok, dataRecorder.Body.String())
	}

	wrongApplication := httptest.NewRequest(http.MethodGet, "/v1/applications/application-2/purchases:verify", nil)
	wrongApplication.SetPathValue("application_id", "application-2")
	wrongApplication.Header.Set("Authorization", "Bearer "+session.Token)
	wrongRecorder := httptest.NewRecorder()
	if _, ok := api.requireCustomerSession(wrongRecorder, wrongApplication); ok || wrongRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong application status = %d, ok = %t", wrongRecorder.Code, ok)
	}

	durableKeyRequest := httptest.NewRequest(http.MethodGet, "/v1/applications/application-1/purchases:verify", nil)
	durableKeyRequest.SetPathValue("application_id", "application-1")
	durableKeyRequest.Header.Set("Authorization", "Bearer "+applicationKey)
	durableKeyRecorder := httptest.NewRecorder()
	if _, ok := api.requireCustomerSession(durableKeyRecorder, durableKeyRequest); ok || durableKeyRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("durable key status = %d, ok = %t", durableKeyRecorder.Code, ok)
	}
}

// newCustomerSessionTestKeyring constructs deterministic root material for HTTP boundary tests.
func newCustomerSessionTestKeyring(t *testing.T) *platformprotection.Keyring {
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
