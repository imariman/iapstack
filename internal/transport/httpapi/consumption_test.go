package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/persistence"
)

// TestAppleConsumptionConsentRejectsCustomerSessions verifies only the durable application bearer writes consent.
// The API has no operations store, so a request that passed authentication would panic instead of returning 401.
func TestAppleConsumptionConsentRejectsCustomerSessions(t *testing.T) {
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
	sessions, err := auth.NewCustomerSessions(store, newCustomerSessionTestKeyring(t))
	if err != nil {
		t.Fatalf("NewCustomerSessions() error = %v", err)
	}
	api := &API{authentication: authentication, customerSessions: sessions, bodyLimit: 1024}
	sessionRequest := httptest.NewRequest(http.MethodPost, "/v1/applications/application-1/customer-sessions",
		strings.NewReader(`{"external_customer_id":"4f753b4a-32c1-4fe4-a8e6-06efb0b35db7"}`))
	sessionRequest.SetPathValue("application_id", "application-1")
	sessionRequest.Header.Set("Authorization", "Bearer "+applicationKey)
	sessionRequest.Header.Set("Content-Type", "application/json")
	sessionRecorder := httptest.NewRecorder()
	api.createCustomerSession(sessionRecorder, sessionRequest)
	var session customerSessionResponse
	if sessionRecorder.Code != http.StatusCreated || json.Unmarshal(sessionRecorder.Body.Bytes(), &session) != nil || session.Token == "" {
		t.Fatalf("customer session = %d %s", sessionRecorder.Code, sessionRecorder.Body.String())
	}

	for name, bearer := range map[string]string{
		"customer session": session.Token,
		"bootstrap admin":  testBootstrapAdminKey,
		"missing":          "",
	} {
		request := httptest.NewRequest(http.MethodPut,
			"/v1/applications/application-1/customers/4f753b4a-32c1-4fe4-a8e6-06efb0b35db7/apple-consumption-consent",
			strings.NewReader(`{"consented":true}`))
		request.SetPathValue("application_id", "application-1")
		request.SetPathValue("external_customer_id", "4f753b4a-32c1-4fe4-a8e6-06efb0b35db7")
		request.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		recorder := httptest.NewRecorder()
		api.putApplicationAppleConsumptionConsent(recorder, request)
		if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), `"code":"unauthorized"`) {
			t.Fatalf("%s consent write = %d %s, want 401 unauthorized", name, recorder.Code, recorder.Body.String())
		}
	}
}
