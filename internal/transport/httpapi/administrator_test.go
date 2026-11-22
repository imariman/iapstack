package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

const (
	// testAdministratorPassword satisfies the administrator password policy.
	testAdministratorPassword = "correct horse battery"
	// testAdministratorReplacementPassword is a second valid administrator password.
	testAdministratorReplacementPassword = "another long passphrase"
)

// switchableSessionProtection wraps the session keyring and can refuse to protect new sessions.
type switchableSessionProtection struct {
	protection.Service
	failProtect bool
}

// TestAdministratorAccountReplacesBootstrapSignIn verifies the first account retires the bootstrap session
// and that a username and password then open a dashboard session.
func TestAdministratorAccountReplacesBootstrapSignIn(t *testing.T) {
	t.Parallel()

	api := newDashboardSessionTestAPI(t)
	bootstrapCookie := loginDashboardSession(t, api)
	created := createTestAdministrator(t, api, bootstrapCookie, "  Owner@Example.com ", testAdministratorPassword)
	if created.Username != "owner@example.com" || created.Current || len(created.ID) != 24 {
		t.Fatalf("created administrator = %#v", created)
	}

	statusRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/dashboard-session", nil)
	statusRequest.AddCookie(bootstrapCookie)
	statusRecorder := httptest.NewRecorder()
	api.getDashboardSession(statusRecorder, statusRequest)
	if statusRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap session after account creation status = %d, want %d", statusRecorder.Code, http.StatusUnauthorized)
	}

	noCredentials := httptest.NewRequest(http.MethodPost, "/v1/admin/dashboard-session", nil)
	noCredentialsRecorder := httptest.NewRecorder()
	api.createDashboardSession(noCredentialsRecorder, noCredentials)
	if noCredentialsRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("sign-in without credentials status = %d, want %d", noCredentialsRecorder.Code, http.StatusUnauthorized)
	}

	crossOrigin := dashboardJSONRequest(http.MethodPost, "/v1/admin/dashboard-session",
		`{"username":"owner@example.com","password":"`+testAdministratorPassword+`"}`, nil)
	crossOrigin.Header.Set("Origin", "https://attacker.example")
	crossOriginRecorder := httptest.NewRecorder()
	api.createDashboardSession(crossOriginRecorder, crossOrigin)
	if crossOriginRecorder.Code != http.StatusForbidden || !strings.Contains(crossOriginRecorder.Body.String(), "csrf_rejected") {
		t.Fatalf("cross-origin sign-in = %d %s, want csrf_rejected", crossOriginRecorder.Code, crossOriginRecorder.Body.String())
	}

	wrongRecorder := signInTestAdministrator(api, "owner@example.com", "wrong horse battery")
	if wrongRecorder.Code != http.StatusUnauthorized || !strings.Contains(wrongRecorder.Body.String(), "invalid_credentials") {
		t.Fatalf("wrong password sign-in = %d %s, want invalid_credentials", wrongRecorder.Code, wrongRecorder.Body.String())
	}

	signInRecorder := signInTestAdministrator(api, "OWNER@example.com", testAdministratorPassword)
	if signInRecorder.Code != http.StatusCreated {
		t.Fatalf("password sign-in status = %d: %s", signInRecorder.Code, signInRecorder.Body.String())
	}
	if strings.Contains(signInRecorder.Body.String(), testAdministratorPassword) {
		t.Fatalf("sign-in response exposes the password: %s", signInRecorder.Body.String())
	}
	var session dashboardSessionResponse
	if err := json.Unmarshal(signInRecorder.Body.Bytes(), &session); err != nil ||
		session.Bootstrap || !session.HasDurableAdmin || session.Username != "owner@example.com" {
		t.Fatalf("password sign-in response = %#v, %v", session, err)
	}
	cookie := dashboardCookie(t, signInRecorder)

	listRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/administrators", nil)
	listRequest.AddCookie(cookie)
	listRecorder := httptest.NewRecorder()
	api.listAdministrators(listRecorder, listRequest)
	var collection administratorCollectionResponse
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &collection); err != nil || listRecorder.Code != http.StatusOK ||
		len(collection.Administrators) != 1 || !collection.Administrators[0].Current {
		t.Fatalf("administrator list = %d %#v, %v", listRecorder.Code, collection, err)
	}

	// The account can sign in, so the only administrator key is no longer the last way in.
	keyRequest := dashboardJSONRequest(http.MethodPost, "/v1/admin/api-keys", `{"role":"admin"}`, cookie)
	keyRecorder := httptest.NewRecorder()
	api.createAPIKey(keyRecorder, keyRequest)
	var createdKey map[string]string
	if err := json.Unmarshal(keyRecorder.Body.Bytes(), &createdKey); err != nil || keyRecorder.Code != http.StatusCreated {
		t.Fatalf("create administrator key = %d, %v", keyRecorder.Code, err)
	}
	revokeRequest := dashboardJSONRequest(http.MethodDelete, "/v1/admin/api-keys/"+bearerKeyID(createdKey["key"]), "", cookie)
	revokeRequest.SetPathValue("key_id", bearerKeyID(createdKey["key"]))
	revokeRecorder := httptest.NewRecorder()
	api.revokeAPIKey(revokeRecorder, revokeRequest)
	if revokeRecorder.Code != http.StatusOK {
		t.Fatalf("revoke only administrator key status = %d: %s", revokeRecorder.Code, revokeRecorder.Body.String())
	}
}

// TestAdministratorPasswordChangeRotatesSession verifies the current-password check and the replacement cookie.
func TestAdministratorPasswordChangeRotatesSession(t *testing.T) {
	t.Parallel()

	api := newDashboardSessionTestAPI(t)
	owner := createTestAdministrator(t, api, loginDashboardSession(t, api), "owner", testAdministratorPassword)
	cookie := dashboardCookie(t, signInTestAdministrator(api, "owner", testAdministratorPassword))

	changePassword := func(id, body string, sessionCookie *http.Cookie) *httptest.ResponseRecorder {
		request := dashboardJSONRequest(http.MethodPut, "/v1/admin/administrators/"+id+"/password", body, sessionCookie)
		request.SetPathValue("administrator_id", id)
		recorder := httptest.NewRecorder()
		api.setAdministratorPassword(recorder, request)
		return recorder
	}
	missingCurrent := changePassword(owner.ID, `{"password":"`+testAdministratorReplacementPassword+`"}`, cookie)
	if missingCurrent.Code != http.StatusForbidden || !strings.Contains(missingCurrent.Body.String(), "password_mismatch") {
		t.Fatalf("own password change without the current password = %d %s", missingCurrent.Code, missingCurrent.Body.String())
	}
	weak := changePassword(owner.ID, `{"current_password":"`+testAdministratorPassword+`","password":"short"}`, cookie)
	if weak.Code != http.StatusBadRequest {
		t.Fatalf("weak password change status = %d, want %d", weak.Code, http.StatusBadRequest)
	}
	changed := changePassword(owner.ID,
		`{"current_password":"`+testAdministratorPassword+`","password":"`+testAdministratorReplacementPassword+`"}`, cookie)
	if changed.Code != http.StatusOK {
		t.Fatalf("own password change status = %d: %s", changed.Code, changed.Body.String())
	}
	replacementCookie := dashboardCookie(t, changed)
	if replacementCookie.Value == cookie.Value {
		t.Fatal("own password change kept the earlier session cookie")
	}
	if recorder := signInTestAdministrator(api, "owner", testAdministratorPassword); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("sign-in with the replaced password status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	// A second administrator replaces another account's password without knowing it.
	second := createTestAdministrator(t, api, replacementCookie, "second", testAdministratorPassword)
	reset := changePassword(second.ID, `{"password":"`+testAdministratorReplacementPassword+`"}`, replacementCookie)
	if reset.Code != http.StatusOK || len(reset.Result().Cookies()) != 0 {
		t.Fatalf("other account password change = %d with %d cookies", reset.Code, len(reset.Result().Cookies()))
	}
	if missing := changePassword("0123456789abcdef01234567", `{"password":"`+testAdministratorReplacementPassword+`"}`, replacementCookie); missing.Code != http.StatusNotFound {
		t.Fatalf("missing account password change status = %d, want %d", missing.Code, http.StatusNotFound)
	}

	deleteAdministrator := func(id string) *httptest.ResponseRecorder {
		request := dashboardJSONRequest(http.MethodDelete, "/v1/admin/administrators/"+id, "", replacementCookie)
		request.SetPathValue("administrator_id", id)
		recorder := httptest.NewRecorder()
		api.deleteAdministrator(recorder, request)
		return recorder
	}
	if recorder := deleteAdministrator(owner.ID); recorder.Code != http.StatusConflict {
		t.Fatalf("delete own account status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if recorder := deleteAdministrator(second.ID); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete other account status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if recorder := deleteAdministrator(second.ID); recorder.Code != http.StatusNotFound {
		t.Fatalf("delete missing account status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

// TestAdministratorPasswordChangeSucceedsWhenSessionCannotBeReissued verifies a committed change is reported
// as a success and the stale cookie is cleared when no replacement session can be minted.
func TestAdministratorPasswordChangeSucceedsWhenSessionCannotBeReissued(t *testing.T) {
	t.Parallel()

	protector := &switchableSessionProtection{}
	api := newDashboardSessionTestAPIWithProtection(t, func(service protection.Service) protection.Service {
		protector.Service = service
		return protector
	})
	owner := createTestAdministrator(t, api, loginDashboardSession(t, api), "owner", testAdministratorPassword)
	cookie := dashboardCookie(t, signInTestAdministrator(api, "owner", testAdministratorPassword))

	protector.failProtect = true
	request := dashboardJSONRequest(http.MethodPut, "/v1/admin/administrators/"+owner.ID+"/password",
		`{"current_password":"`+testAdministratorPassword+`","password":"`+testAdministratorReplacementPassword+`"}`, cookie)
	request.SetPathValue("administrator_id", owner.ID)
	recorder := httptest.NewRecorder()
	api.setAdministratorPassword(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("password change without a replacement session status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if cleared := dashboardCookie(t, recorder); cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Fatalf("stale session cookie after the change = %#v, want it cleared", cleared)
	}

	protector.failProtect = false
	statusRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/dashboard-session", nil)
	statusRequest.AddCookie(cookie)
	statusRecorder := httptest.NewRecorder()
	api.getDashboardSession(statusRecorder, statusRequest)
	if statusRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("earlier session after the change status = %d, want %d", statusRecorder.Code, http.StatusUnauthorized)
	}
	if recorder := signInTestAdministrator(api, "owner", testAdministratorReplacementPassword); recorder.Code != http.StatusCreated {
		t.Fatalf("sign-in with the new password status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

// TestAdministratorSignInLocksAfterRepeatedFailures verifies the lock response and its Retry-After header.
func TestAdministratorSignInLocksAfterRepeatedFailures(t *testing.T) {
	t.Parallel()

	api := newDashboardSessionTestAPI(t)
	createTestAdministrator(t, api, loginDashboardSession(t, api), "owner", testAdministratorPassword)
	for attempt := 1; attempt < 5; attempt++ {
		if recorder := signInTestAdministrator(api, "owner", "wrong horse battery"); recorder.Code != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d status = %d, want %d", attempt, recorder.Code, http.StatusUnauthorized)
		}
	}
	for _, password := range []string{"wrong horse battery", testAdministratorPassword} {
		recorder := signInTestAdministrator(api, "owner", password)
		if recorder.Code != http.StatusTooManyRequests || !strings.Contains(recorder.Body.String(), "too_many_attempts") ||
			recorder.Header().Get("Retry-After") == "" {
			t.Fatalf("locked sign-in = %d %s (Retry-After %q)", recorder.Code, recorder.Body.String(), recorder.Header().Get("Retry-After"))
		}
	}
}

// createTestAdministrator creates one account through the HTTP handler and returns its response.
func createTestAdministrator(
	t *testing.T,
	api *API,
	cookie *http.Cookie,
	username string,
	password string,
) administratorResponse {
	t.Helper()

	body, err := json.Marshal(administratorRequest{Username: username, Password: password})
	if err != nil {
		t.Fatalf("encode administrator request: %v", err)
	}
	recorder := httptest.NewRecorder()
	api.createAdministrator(recorder, dashboardJSONRequest(http.MethodPost, "/v1/admin/administrators", string(body), cookie))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create administrator status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var response administratorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode administrator response: %v", err)
	}
	return response
}

// signInTestAdministrator posts one same-origin username and password sign-in.
func signInTestAdministrator(api *API, username string, password string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(dashboardSignInRequest{Username: username, Password: password})
	recorder := httptest.NewRecorder()
	api.createDashboardSession(recorder, dashboardJSONRequest(http.MethodPost, "/v1/admin/dashboard-session", string(body), nil))
	return recorder
}

// dashboardJSONRequest builds one same-origin dashboard request with an optional JSON body and session cookie.
func dashboardJSONRequest(method string, target string, body string, cookie *http.Cookie) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	setSameOriginDashboardHeaders(request)
	return request
}

// TestAccountSessionsAreDurableAuditActors verifies audited writes accept an account and still reject bootstrap.
func TestAccountSessionsAreDurableAuditActors(t *testing.T) {
	t.Parallel()

	api := &API{}
	for name, test := range map[string]struct {
		principal auth.Principal
		allowed   bool
	}{
		"stored key": {auth.Principal{KeyID: "0123456789abcdef01234567", Role: persistence.APIKeyRoleAdmin}, true},
		"account":    {auth.Principal{UserID: "account-1", Username: "owner", Role: persistence.APIKeyRoleAdmin}, true},
		"bootstrap":  {auth.Principal{Role: persistence.APIKeyRoleAdmin}, false},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/v1/admin/projects/project-1/customers/customer-1/manual-grants", nil)
		if allowed := api.requireDurableActor(recorder, request, test.principal); allowed != test.allowed {
			t.Fatalf("%s requireDurableActor() = %t, want %t", name, allowed, test.allowed)
		}
		if !test.allowed && recorder.Code != http.StatusForbidden {
			t.Fatalf("%s rejection status = %d, want %d", name, recorder.Code, http.StatusForbidden)
		}
	}
}

// Protect fails while failProtect is set and delegates otherwise.
func (service *switchableSessionProtection) Protect(ctx context.Context, request protection.Request) (protection.Value, error) {
	if service.failProtect {
		return protection.Value{}, errors.New("session protection is unavailable")
	}
	return service.Service.Protect(ctx, request)
}
