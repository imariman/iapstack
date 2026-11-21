package httpapi

import (
	"net/http"
	"net/url"
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
}

// getDashboardSession reports the current administrator session status and whether bootstrap handover is still required.
func (api *API) getDashboardSession(writer http.ResponseWriter, request *http.Request) {
	session, _, err := api.authenticateAdminSession(request)
	if err != nil || session.Role != persistence.APIKeyRoleAdmin {
		api.writeAuthenticationFailure(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, dashboardSessionResponseFromPrincipal(session))
}

// createDashboardSession exchanges one administrator bearer for a protected HttpOnly browser cookie.
func (api *API) createDashboardSession(writer http.ResponseWriter, request *http.Request) {
	principal, err := api.authenticate(request)
	if err != nil || principal.Role != persistence.APIKeyRoleAdmin {
		api.writeAuthenticationFailure(writer, request, err)
		return
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
	session, dashboardSession, err := api.authenticateAdminSession(request)
	if err != nil {
		return auth.Principal{}, dashboardSession, err
	}
	return session.Principal, dashboardSession, nil
}

// authenticateAdminSession authenticates one administrator bearer or encrypted dashboard cookie with expiry metadata.
func (api *API) authenticateAdminSession(request *http.Request) (auth.AdminPrincipal, bool, error) {
	if request.Header.Get("Authorization") != "" {
		principal, err := api.authenticate(request)
		if err != nil {
			return auth.AdminPrincipal{}, false, err
		}
		now := time.Now().UTC()
		if api.clock != nil {
			now = api.clock().UTC()
		}
		return auth.AdminPrincipal{Principal: principal, ExpiresAt: now}, false, nil
	}
	cookie, err := request.Cookie(dashboardSessionCookieName)
	if err != nil || cookie.Value == "" {
		return auth.AdminPrincipal{}, true, auth.ErrUnauthorized
	}
	principal, err := api.adminSessions.Authenticate(request.Context(), cookie.Value)
	if err != nil {
		return auth.AdminPrincipal{}, true, err
	}
	return principal, true, nil
}

// dashboardSessionResponseFromPrincipal builds the secret-free session status payload.
func dashboardSessionResponseFromPrincipal(session auth.AdminPrincipal) dashboardSessionResponse {
	// Bootstrap credentials authenticate only when zero active stored administrator keys exist.
	bootstrap := session.KeyID == ""
	return dashboardSessionResponse{
		ExpiresAt:       session.ExpiresAt,
		Bootstrap:       bootstrap,
		HasDurableAdmin: !bootstrap,
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
