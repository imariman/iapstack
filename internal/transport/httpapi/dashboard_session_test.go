package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/persistence"
	platformprotection "github.com/imariman/iapstack/internal/platform/protection"
	"github.com/imariman/iapstack/internal/protection"
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
	if err := json.Unmarshal(loginRecorder.Body.Bytes(), &response); err != nil || response.ExpiresAt.IsZero() ||
		!response.Bootstrap || response.HasDurableAdmin {
		t.Fatalf("decode login response = %#v, %v", response, err)
	}
	cookie := dashboardCookie(t, loginRecorder)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode ||
		cookie.Path != dashboardSessionCookiePath || cookie.Expires.IsZero() || cookie.MaxAge <= 0 {
		t.Fatalf("dashboard cookie attributes = %#v", cookie)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/dashboard-session", nil)
	statusRequest.AddCookie(cookie)
	statusRecorder := httptest.NewRecorder()
	api.getDashboardSession(statusRecorder, statusRequest)
	if statusRecorder.Code != http.StatusOK {
		t.Fatalf("get dashboard session status = %d: %s", statusRecorder.Code, statusRecorder.Body.String())
	}
	var statusResponse dashboardSessionResponse
	if err := json.Unmarshal(statusRecorder.Body.Bytes(), &statusResponse); err != nil ||
		!statusResponse.Bootstrap || statusResponse.HasDurableAdmin || !statusResponse.ExpiresAt.Equal(response.ExpiresAt) {
		t.Fatalf("get dashboard session response = %#v, %v", statusResponse, err)
	}
	// The session read describes the cookie only, so a bearer is rejected with or without a cookie.
	for _, withCookie := range []bool{false, true} {
		bearerStatusRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/dashboard-session", nil)
		bearerStatusRequest.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
		if withCookie {
			bearerStatusRequest.AddCookie(cookie)
		}
		bearerStatusRecorder := httptest.NewRecorder()
		api.getDashboardSession(bearerStatusRecorder, bearerStatusRequest)
		if bearerStatusRecorder.Code != http.StatusUnauthorized {
			t.Fatalf("bearer get dashboard session (cookie %t) status = %d, want %d",
				withCookie, bearerStatusRecorder.Code, http.StatusUnauthorized)
		}
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

// TestDashboardSessionAutoRetiresBootstrapOnDurableAdminCreation verifies durable admin creation retires bootstrap bearers and cookies.
func TestDashboardSessionAutoRetiresBootstrapOnDurableAdminCreation(t *testing.T) {
	t.Parallel()

	api := newDashboardSessionTestAPI(t)
	bootstrapCookie := loginDashboardSession(t, api)

	createRequest := httptest.NewRequest(http.MethodPost, "/v1/admin/api-keys", strings.NewReader(`{"role":"admin"}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.AddCookie(bootstrapCookie)
	setSameOriginDashboardHeaders(createRequest)
	createRecorder := httptest.NewRecorder()
	api.createAPIKey(createRecorder, createRequest)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create durable admin key status = %d: %s", createRecorder.Code, createRecorder.Body.String())
	}
	var created struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &created); err != nil || created.Key == "" {
		t.Fatalf("decode created key = %#v, %v", created, err)
	}

	upgradeRequest := httptest.NewRequest(http.MethodPost, "/v1/admin/dashboard-session", nil)
	upgradeRequest.Header.Set("Authorization", "Bearer "+created.Key)
	upgradeRecorder := httptest.NewRecorder()
	api.createDashboardSession(upgradeRecorder, upgradeRequest)
	if upgradeRecorder.Code != http.StatusCreated {
		t.Fatalf("upgrade dashboard session status = %d: %s", upgradeRecorder.Code, upgradeRecorder.Body.String())
	}
	var upgradedResponse dashboardSessionResponse
	if err := json.Unmarshal(upgradeRecorder.Body.Bytes(), &upgradedResponse); err != nil ||
		upgradedResponse.Bootstrap || !upgradedResponse.HasDurableAdmin {
		t.Fatalf("upgraded session response = %#v, %v", upgradedResponse, err)
	}
	durableCookie := dashboardCookie(t, upgradeRecorder)

	retiredCookieRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/dashboard-session", nil)
	retiredCookieRequest.AddCookie(bootstrapCookie)
	retiredCookieRecorder := httptest.NewRecorder()
	api.getDashboardSession(retiredCookieRecorder, retiredCookieRequest)
	if retiredCookieRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("retired bootstrap cookie status = %d, want 401", retiredCookieRecorder.Code)
	}

	retiredBearerRequest := httptest.NewRequest(http.MethodPost, "/v1/admin/dashboard-session", nil)
	retiredBearerRequest.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
	retiredBearerRecorder := httptest.NewRecorder()
	api.createDashboardSession(retiredBearerRecorder, retiredBearerRequest)
	if retiredBearerRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("retired bootstrap bearer status = %d, want 401", retiredBearerRecorder.Code)
	}

	activeRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/dashboard-session", nil)
	activeRequest.AddCookie(durableCookie)
	activeRecorder := httptest.NewRecorder()
	api.getDashboardSession(activeRecorder, activeRequest)
	if activeRecorder.Code != http.StatusOK {
		t.Fatalf("upgraded durable cookie status = %d: %s", activeRecorder.Code, activeRecorder.Body.String())
	}
}

// newDashboardSessionTestAPI constructs dashboard authentication with deterministic protection keys.
func newDashboardSessionTestAPI(t *testing.T) *API {
	t.Helper()
	return newDashboardSessionTestAPIWithProtection(t, nil)
}

// newDashboardSessionTestAPIWithProtection builds the same API with the session keyring wrapped by wrap.
func newDashboardSessionTestAPIWithProtection(
	t *testing.T,
	wrap func(protection.Service) protection.Service,
) *API {
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
	var sessionProtection protection.Service = keyring
	if wrap != nil {
		sessionProtection = wrap(keyring)
	}
	sessions, err := auth.NewAdminSessions(store, sessionProtection)
	if err != nil {
		t.Fatalf("NewAdminSessions() error = %v", err)
	}
	return &API{
		admin: fakeAdminQueryStore{}, authentication: authentication, adminSessions: sessions,
		bodyLimit: 1 << 20, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
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
