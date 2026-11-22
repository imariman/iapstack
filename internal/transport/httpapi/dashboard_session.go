package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/persistence"
)

const (
	// dashboardSessionCookieName uses the Secure prefix so browsers reject an insecure origin assignment.
	dashboardSessionCookieName = "__Secure-iapstack_admin"
	// dashboardSessionCookiePath limits the administrator cookie to control-plane API calls.
	dashboardSessionCookiePath = "/v1/admin"
	// dashboardRequestHeader marks same-origin JavaScript mutations that cannot be emitted by cross-site forms.
	dashboardRequestHeader = "X-IAPStack-Dashboard"
	// dashboardRequestHeaderValue is the exact accepted mutation marker.
	dashboardRequestHeaderValue = "1"
)

// dashboardSessionResponse reports browser-session expiry and bootstrap handover state without exposing the cookie value.
type dashboardSessionResponse struct {
	ExpiresAt       time.Time `json:"expires_at"`
	Bootstrap       bool      `json:"bootstrap"`
	HasDurableAdmin bool      `json:"has_durable_admin"`
	// Username names the administrator account behind a password sign-in. Key and bootstrap sessions omit it.
	Username string `json:"username,omitempty"`
}

// dashboardSignInRequest carries one administrator account's username and password.
type dashboardSignInRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// getDashboardSession reports the browser session expiry and whether bootstrap handover is still required.
func (api *API) getDashboardSession(writer http.ResponseWriter, request *http.Request) {
	// The response describes the cookie session itself, so an explicit bearer is rejected rather than ignored.
	if request.Header.Get("Authorization") != "" {
		api.writeAuthenticationFailure(writer, request, auth.ErrUnauthorized)
		return
	}
	session, err := api.authenticateDashboardCookie(request)
	if err != nil {
		api.writeAuthenticationFailure(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, dashboardSessionResponseFromPrincipal(session))
}

// createDashboardSession exchanges one administrator bearer, or one administrator account's username
// and password, for a protected HttpOnly browser cookie.
func (api *API) createDashboardSession(writer http.ResponseWriter, request *http.Request) {
	var principal auth.Principal
	if request.Header.Get("Authorization") != "" {
		var err error
		principal, err = api.authenticate(request)
		if err != nil || principal.Role != persistence.APIKeyRoleAdmin {
			api.writeAuthenticationFailure(writer, request, err)
			return
		}
	} else {
		var ok bool
		if principal, ok = api.signInWithPassword(writer, request); !ok {
			return
		}
	}
	session, err := api.adminSessions.Mint(request.Context(), principal)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	setDashboardSessionCookie(writer, session)
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusCreated, dashboardSessionResponseFromPrincipal(auth.AdminPrincipal{
		Principal: principal, ExpiresAt: session.ExpiresAt,
	}))
}

// signInWithPassword authenticates the username and password in the request body. It writes the
// failure response itself and reports whether sign-in succeeded.
func (api *API) signInWithPassword(writer http.ResponseWriter, request *http.Request) (auth.Principal, bool) {
	// A request with no bearer and no body carries no credentials at all.
	if request.ContentLength == 0 {
		api.writeAuthenticationFailure(writer, request, auth.ErrUnauthorized)
		return auth.Principal{}, false
	}
	// Password sign-in is a browser flow, so it obeys the same same-origin boundary as cookie mutations.
	if !validDashboardMutation(request) {
		writeAPIError(writer, request, http.StatusForbidden, "csrf_rejected", "dashboard request origin could not be verified")
		return auth.Principal{}, false
	}
	var input dashboardSignInRequest
	if !api.decode(writer, request, &input) {
		return auth.Principal{}, false
	}
	principal, err := api.authentication.AuthenticatePassword(request.Context(), input.Username, input.Password)
	if err != nil {
		api.writePasswordFailure(writer, request, err)
		return auth.Principal{}, false
	}
	return principal, true
}

// writePasswordFailure maps a failed password check onto a lock, a wrong-credentials, or a service response.
func (api *API) writePasswordFailure(writer http.ResponseWriter, request *http.Request, err error) {
	var locked *auth.LockedError
	switch {
	case errors.As(err, &locked):
		// Round up so a client that waits the advertised seconds is past the lock.
		seconds := int64((locked.RetryAfter + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		writer.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		writeAPIError(
			writer, request, http.StatusTooManyRequests,
			"too_many_attempts", "too many failed sign-in attempts; try again later",
		)
	case errors.Is(err, auth.ErrPasswordMismatch):
		writeAPIError(writer, request, http.StatusForbidden, "password_mismatch", "the current password is incorrect")
	case errors.Is(err, auth.ErrUnauthorized):
		writeAPIError(writer, request, http.StatusUnauthorized, "invalid_credentials", "the username or password is incorrect")
	default:
		api.writeAuthenticationFailure(writer, request, err)
	}
}

// deleteDashboardSession authenticates the current administrator and expires its browser cookie.
func (api *API) deleteDashboardSession(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	clearDashboardSessionCookie(writer)
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

// authenticateAdmin accepts an explicit API bearer or an encrypted dashboard cookie without ambiguous fallback.
func (api *API) authenticateAdmin(request *http.Request) (auth.Principal, bool, error) {
	if request.Header.Get("Authorization") != "" {
		principal, err := api.authenticate(request)
		return principal, false, err
	}
	session, err := api.authenticateDashboardCookie(request)
	if err != nil {
		return auth.Principal{}, true, err
	}
	return session.Principal, true, nil
}

// authenticateDashboardCookie opens the encrypted dashboard cookie together with its expiry metadata.
func (api *API) authenticateDashboardCookie(request *http.Request) (auth.AdminPrincipal, error) {
	cookie, err := request.Cookie(dashboardSessionCookieName)
	if err != nil || cookie.Value == "" {
		return auth.AdminPrincipal{}, auth.ErrUnauthorized
	}
	return api.adminSessions.Authenticate(request.Context(), cookie.Value)
}

// dashboardSessionResponseFromPrincipal builds the secret-free session status payload.
func dashboardSessionResponseFromPrincipal(session auth.AdminPrincipal) dashboardSessionResponse {
	// Bootstrap credentials authenticate only while no stored administrator key or account exists.
	bootstrap := session.KeyID == "" && session.UserID == ""
	return dashboardSessionResponse{
		ExpiresAt:       session.ExpiresAt,
		Bootstrap:       bootstrap,
		HasDurableAdmin: !bootstrap,
		Username:        session.Username,
	}
}

// setDashboardSessionCookie writes a persistent, origin-bound browser cookie without exposing its token to JavaScript.
func setDashboardSessionCookie(writer http.ResponseWriter, session auth.AdminSession) {
	maxAge := int(time.Until(session.ExpiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(writer, &http.Cookie{
		Name:     dashboardSessionCookieName,
		Value:    session.Token,
		Path:     dashboardSessionCookiePath,
		Expires:  session.ExpiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearDashboardSessionCookie expires the exact cookie scope used by dashboard login.
func clearDashboardSessionCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{
		Name:     dashboardSessionCookieName,
		Value:    "",
		Path:     dashboardSessionCookiePath,
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// safeRequestMethod identifies control-plane reads that cannot mutate server state.
func safeRequestMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// validDashboardMutation enforces a same-origin custom-header boundary for cookie-authenticated writes.
func validDashboardMutation(request *http.Request) bool {
	if request.Header.Get(dashboardRequestHeader) != dashboardRequestHeaderValue {
		return false
	}
	if fetchSite := request.Header.Get("Sec-Fetch-Site"); fetchSite != "" && fetchSite != "same-origin" && fetchSite != "none" {
		return false
	}
	origin, err := url.Parse(request.Header.Get("Origin"))
	if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") || origin.User != nil ||
		origin.Host == "" || !strings.EqualFold(origin.Host, request.Host) ||
		origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	return true
}
