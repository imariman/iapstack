package httpapi

import (
	"bytes"
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

// TestDashboardSessionExchangesBearerForSecureCookie verifies login, cookie-only reads, and explicit logout.
func TestDashboardSessionExchangesBearerForSecureCookie(t *testing.T) {
	t.Parallel()

	api := newDashboardSessionTestAPI(t)
	loginRequest := httptest.NewRequest(http.MethodPost, "/v1/admin/dashboard-session", nil)
	loginRequest.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
	loginRecorder := httptest.NewRecorder()
	api.createDashboardSession(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusCreated {
		t.Fatalf("login status = %d, want %d: %s", loginRecorder.Code, http.StatusCreated, loginRecorder.Body.String())
	}
	if strings.Contains(loginRecorder.Body.String(), testBootstrapAdminKey) || strings.Contains(loginRecorder.Body.String(), `"token"`) {
		t.Fatalf("login response exposes secret material: %s", loginRecorder.Body.String())
	}
	var response dashboardSessionResponse
	if err := json.Unmarshal(loginRecorder.Body.Bytes(), &response); err != nil || response.ExpiresAt.IsZero() {
		t.Fatalf("decode login response = %#v, %v", response, err)
	}
	cookie := dashboardCookie(t, loginRecorder)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode ||
		cookie.Path != dashboardSessionCookiePath || cookie.Expires.IsZero() || cookie.MaxAge <= 0 {
		t.Fatalf("dashboard cookie attributes = %#v", cookie)
	}

	projectsRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/projects", nil)
	projectsRequest.AddCookie(cookie)
	projectsRecorder := httptest.NewRecorder()
	api.adminProjects(projectsRecorder, projectsRequest)
	if projectsRecorder.Code != http.StatusOK {
		t.Fatalf("cookie-authenticated projects status = %d: %s", projectsRecorder.Code, projectsRecorder.Body.String())
	}

	logoutRequest := httptest.NewRequest(http.MethodDelete, "/v1/admin/dashboard-session", nil)
	logoutRequest.AddCookie(cookie)
	setSameOriginDashboardHeaders(logoutRequest)
	logoutRecorder := httptest.NewRecorder()
	api.deleteDashboardSession(logoutRecorder, logoutRequest)
	if logoutRecorder.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d: %s", logoutRecorder.Code, http.StatusNoContent, logoutRecorder.Body.String())
	}
	cleared := dashboardCookie(t, logoutRecorder)
	if cleared.Value != "" || cleared.MaxAge >= 0 || !cleared.Expires.Before(time.Now()) {
		t.Fatalf("cleared dashboard cookie = %#v", cleared)
	}
}

// TestDashboardSessionRequiresSameOriginProofForMutations verifies cookies cannot authorize cross-site writes.
func TestDashboardSessionRequiresSameOriginProofForMutations(t *testing.T) {
	t.Parallel()

	api := newDashboardSessionTestAPI(t)
	cookie := loginDashboardSession(t, api)
	tests := []struct {
		name   string
		origin string
		header string
		status int
	}{
		{name: "missing proof", status: http.StatusForbidden},
		{name: "cross-site origin", origin: "https://attacker.example", header: dashboardRequestHeaderValue, status: http.StatusForbidden},
		{name: "same-origin", origin: "https://example.com", header: dashboardRequestHeaderValue, status: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "/v1/admin/projects/project-1", nil)
			request.AddCookie(cookie)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.header != "" {
				request.Header.Set(dashboardRequestHeader, test.header)
			}
			recorder := httptest.NewRecorder()
			if _, ok := api.requireAdmin(recorder, request); ok {
				recorder.WriteHeader(http.StatusOK)
			}
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}

	explicitBearerRequest := httptest.NewRequest(http.MethodPut, "/v1/admin/projects/project-1", nil)
	explicitBearerRequest.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
	explicitBearerRecorder := httptest.NewRecorder()
	if _, ok := api.requireAdmin(explicitBearerRecorder, explicitBearerRequest); !ok {
		t.Fatalf("explicit bearer unexpectedly required browser CSRF proof: %s", explicitBearerRecorder.Body.String())
	}

	ambiguousRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/projects", nil)
	ambiguousRequest.AddCookie(cookie)
	ambiguousRequest.Header.Set("Authorization", "Bearer invalid")
	ambiguousRecorder := httptest.NewRecorder()
	if _, ok := api.requireAdmin(ambiguousRecorder, ambiguousRequest); ok || ambiguousRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("invalid explicit bearer fell back to cookie: status=%d ok=%t", ambiguousRecorder.Code, ok)
	}
}

// newDashboardSessionTestAPI constructs dashboard authentication with deterministic protection keys.
func newDashboardSessionTestAPI(t *testing.T) *API {
	t.Helper()
	store := &keyLifecycleStore{
		records: make(map[string]persistence.APIKeyRecord), revokedAt: make(map[string]time.Time),
	}
	authentication, err := auth.NewService(store, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	config, err := platformprotection.NewConfig(
		"dashboard-key",
		map[string][]byte{"dashboard-key": bytes.Repeat([]byte{3}, 32)},
		bytes.Repeat([]byte{4}, 32),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	keyring, err := platformprotection.New(config)
	if err != nil {
		t.Fatalf("New() keyring error = %v", err)
	}
	sessions, err := auth.NewAdminSessions(store, keyring)
	if err != nil {
		t.Fatalf("NewAdminSessions() error = %v", err)
	}
	return &API{
		admin: fakeAdminQueryStore{}, authentication: authentication, adminSessions: sessions,
	}
}

// loginDashboardSession creates one cookie for HTTP boundary tests.
func loginDashboardSession(t *testing.T, api *API) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/dashboard-session", nil)
	request.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
	recorder := httptest.NewRecorder()
	api.createDashboardSession(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create dashboard session status = %d: %s", recorder.Code, recorder.Body.String())
	}
	return dashboardCookie(t, recorder)
}

// dashboardCookie extracts the exact administrator cookie from one recorded response.
func dashboardCookie(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == dashboardSessionCookieName {
			return cookie
		}
	}
	t.Fatal("dashboard session cookie is missing")
	return nil
}

// setSameOriginDashboardHeaders adds the browser proof required by cookie-authenticated mutations.
func setSameOriginDashboardHeaders(request *http.Request) {
	request.Header.Set("Origin", "https://"+request.Host)
	request.Header.Set(dashboardRequestHeader, dashboardRequestHeaderValue)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
}
