package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/persistence"
)

var (
	// routeParameterPattern matches one path wildcard in the v1 route registry.
	routeParameterPattern = regexp.MustCompile(`\{([a-z_]+)\}`)
)

// TestAgentKeysAreRejectedByEveryV1Route verifies an agent key gets the same response as a wrong-role key
// on every API-key-authenticated route, whether it is read-only or allowed to write.
func TestAgentKeysAreRejectedByEveryV1Route(t *testing.T) {
	store := &keyLifecycleStore{
		records: make(map[string]persistence.APIKeyRecord), revokedAt: make(map[string]time.Time),
	}
	authentication, err := auth.NewService(store, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	createKey := func(principal auth.Principal) string {
		t.Helper()
		bearer, err := authentication.Create(context.Background(), principal)
		if err != nil {
			t.Fatalf("Create(%s) error = %v", principal.Role, err)
		}
		return bearer
	}
	adminKey := createKey(auth.Principal{Role: persistence.APIKeyRoleAdmin})
	applicationKey := createKey(auth.Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-1", ApplicationID: "application-1",
	})
	agentKeys := map[string]string{
		"read-only agent": createKey(auth.Principal{Role: persistence.APIKeyRoleAgent, ProjectID: "project-1"}),
		"writing agent": createKey(auth.Principal{
			Role: persistence.APIKeyRoleAgent, ProjectID: "project-1", AllowWrites: true,
		}),
	}
	keyring := newCustomerSessionTestKeyring(t)
	customerSessions, err := auth.NewCustomerSessions(store, keyring)
	if err != nil {
		t.Fatalf("NewCustomerSessions() error = %v", err)
	}
	adminSessions, err := auth.NewAdminSessions(store, keyring)
	if err != nil {
		t.Fatalf("NewAdminSessions() error = %v", err)
	}
	api := &API{
		authentication: authentication, customerSessions: customerSessions, adminSessions: adminSessions,
		admin: fakeAdminQueryStore{}, bodyLimit: 1024,
	}
	mux := http.NewServeMux()
	for _, route := range api.v1Routes() {
		mux.HandleFunc(route.Method+" "+serveMuxPath(route.Path), route.Handler)
	}

	checked := 0
	for _, route := range api.v1Routes() {
		if strings.HasPrefix(route.Path, "/v1/providers/") || route.OperationID == "getApplicationOffering" {
			// Provider notification routes authenticate the store's signature and the offering
			// document is public, so neither reads an API key.
			continue
		}
		wrongRoleKey := applicationKey
		if !strings.HasPrefix(route.Path, "/v1/admin/") {
			wrongRoleKey = adminKey
		}
		path := routeParameterPattern.ReplaceAllString(route.Path, "$1-1")
		wrongStatus, wrongBody := serveWithBearer(mux, route.Method, path, wrongRoleKey)
		for name, agentKey := range agentKeys {
			status, body := serveWithBearer(mux, route.Method, path, agentKey)
			if status != http.StatusUnauthorized || !strings.Contains(body, `"code":"unauthorized"`) {
				t.Fatalf("%s %s with %s key = %d %s, want 401 unauthorized", route.Method, path, name, status, body)
			}
			if status != wrongStatus || body != wrongBody {
				t.Fatalf("%s %s with %s key = %d %s, want the wrong-role response %d %s",
					route.Method, path, name, status, body, wrongStatus, wrongBody)
			}
		}
		checked++
	}
	if checked < 30 {
		t.Fatalf("checked %d routes, want every API-key-authenticated v1 route", checked)
	}
}

// TestAgentAPIKeyCreationAndListing verifies admins create project-scoped agent keys, the write flag is
// explicit and agent-only, and lifecycle responses expose it only for agent keys.
func TestAgentAPIKeyCreationAndListing(t *testing.T) {
	store := &keyLifecycleStore{
		records: make(map[string]persistence.APIKeyRecord), revokedAt: make(map[string]time.Time),
	}
	authentication, err := auth.NewService(store, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adminKey, err := authentication.Create(context.Background(), auth.Principal{Role: persistence.APIKeyRoleAdmin})
	if err != nil {
		t.Fatalf("Create() admin error = %v", err)
	}
	api := &API{authentication: authentication, bodyLimit: 1024}
	create := func(body string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/v1/admin/api-keys", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+adminKey)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		api.createAPIKey(recorder, request)
		return recorder.Code, recorder.Body.String()
	}

	for _, body := range []string{
		`{"role":"agent","project_id":"project-1"}`,
		`{"role":"agent","project_id":"project-1","allow_writes":false}`,
		`{"role":"agent","project_id":"project-1","allow_writes":true}`,
	} {
		if status, response := create(body); status != http.StatusCreated || !strings.Contains(response, `"key":"iap_`) {
			t.Fatalf("create %s = %d %s, want 201 with a bearer", body, status, response)
		}
	}
	for _, body := range []string{
		`{"role":"agent"}`,
		`{"role":"agent","project_id":"project-1","application_id":"application-1"}`,
		`{"role":"admin","allow_writes":true}`,
		`{"role":"application","project_id":"project-1","application_id":"application-1","allow_writes":true}`,
		`{"role":"agent","project_id":"project-1","allow_writes":"yes"}`,
	} {
		if status, response := create(body); status != http.StatusBadRequest {
			t.Fatalf("create %s = %d %s, want 400", body, status, response)
		}
	}

	collection := listAPIKeyPage(t, api, adminKey, "")
	if len(collection.APIKeys) != 4 {
		t.Fatalf("API keys = %#v, want one admin and three agent keys", collection.APIKeys)
	}
	writers := 0
	var readOnlyAgentID string
	for _, key := range collection.APIKeys {
		switch key.Role {
		case persistence.APIKeyRoleAdmin:
			if key.AllowWrites != nil {
				t.Fatalf("admin key = %#v, want no allow_writes field", key)
			}
		case persistence.APIKeyRoleAgent:
			if key.AllowWrites == nil || key.ProjectID != "project-1" || key.ApplicationID != "" {
				t.Fatalf("agent key = %#v, want project scope and allow_writes", key)
			}
			if *key.AllowWrites {
				writers++
			} else {
				readOnlyAgentID = key.ID
			}
		default:
			t.Fatalf("unexpected key = %#v", key)
		}
	}
	if writers != 1 || readOnlyAgentID == "" {
		t.Fatalf("API keys = %#v, want exactly one writing agent", collection.APIKeys)
	}

	request := httptest.NewRequest(http.MethodDelete, "/v1/admin/api-keys/"+readOnlyAgentID, nil)
	request.SetPathValue("key_id", readOnlyAgentID)
	request.Header.Set("Authorization", "Bearer "+adminKey)
	recorder := httptest.NewRecorder()
	api.revokeAPIKey(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("revoke agent = %d %s", recorder.Code, recorder.Body.String())
	}
	var revoked map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &revoked); err != nil {
		t.Fatalf("decode revoked key: %v", err)
	}
	if revoked["role"] != "agent" || revoked["allow_writes"] != false || revoked["revoked_at"] == nil {
		t.Fatalf("revoked agent key = %#v, want an agent with allow_writes false", revoked)
	}
}

// serveWithBearer sends one empty JSON request with a bearer through the route mux.
func serveWithBearer(handler http.Handler, method, path, bearer string) (int, string) {
	request := httptest.NewRequest(method, path, strings.NewReader("{}"))
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code, recorder.Body.String()
}
