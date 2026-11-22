package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/persistence"
)

// administratorRequest creates one administrator account that signs in with a password.
type administratorRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// administratorPasswordRequest replaces one account's password. CurrentPassword is required when an
// administrator changes their own password.
type administratorPasswordRequest struct {
	CurrentPassword string `json:"current_password,omitempty"`
	Password        string `json:"password"`
}

// administratorCollectionResponse lists administrator accounts without password verifiers.
type administratorCollectionResponse struct {
	Administrators []administratorResponse `json:"administrators"`
}

// administratorResponse describes one administrator account without its password verifier.
type administratorResponse struct {
	ID                string    `json:"id"`
	Username          string    `json:"username"`
	CreatedAt         time.Time `json:"created_at"`
	PasswordChangedAt time.Time `json:"password_changed_at"`
	// Current marks the account that authenticated this request.
	Current bool `json:"current"`
}

// listAdministrators returns the administrator accounts that can sign in to the dashboard.
func (api *API) listAdministrators(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	users, err := api.authentication.AdminUsers(request.Context())
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	responses := make([]administratorResponse, 0, len(users))
	for _, user := range users {
		responses = append(responses, administratorFromSummary(user, principal.UserID))
	}
	writeJSON(writer, http.StatusOK, administratorCollectionResponse{Administrators: responses})
}

// createAdministrator stores one administrator account. Creating the first one retires the bootstrap key.
func (api *API) createAdministrator(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	var input administratorRequest
	if !api.decode(writer, request, &input) {
		return
	}
	user, err := api.authentication.CreateAdminUser(request.Context(), input.Username, input.Password)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, administratorFromSummary(user, principal.UserID))
}

// setAdministratorPassword replaces one account's password. An administrator changing their own password
// must supply the current one and receives a fresh session cookie, because the change ends earlier sessions.
// If that cookie cannot be issued, the stale one is cleared and the response still reports the change.
func (api *API) setAdministratorPassword(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	var input administratorPasswordRequest
	if !api.decode(writer, request, &input) {
		return
	}
	id := request.PathValue("administrator_id")
	self := principal.UserID != "" && principal.UserID == id
	user, err := api.authentication.ChangeAdminUserPassword(
		request.Context(), id, input.CurrentPassword, self, input.Password,
	)
	if err != nil {
		api.writeAdministratorError(writer, request, err)
		return
	}
	if self {
		// The change is committed and has ended the caller's session, so the response must say it
		// succeeded either way. Without a replacement session the stale cookie is cleared and the
		// dashboard asks for a new sign-in with the new password.
		principal.AccountGeneration = user.SessionGeneration
		session, err := api.adminSessions.Mint(request.Context(), principal)
		if err != nil {
			api.logger.Warn("dashboard session was not reissued after a password change", "administrator_id", user.ID)
			clearDashboardSessionCookie(writer)
		} else {
			setDashboardSessionCookie(writer, session)
		}
		writer.Header().Set("Cache-Control", "no-store")
	}
	writeJSON(writer, http.StatusOK, administratorFromSummary(user, principal.UserID))
}

// deleteAdministrator removes another administrator's account while one administrator credential remains.
func (api *API) deleteAdministrator(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	id := request.PathValue("administrator_id")
	if principal.UserID != "" && principal.UserID == id {
		api.writeError(writer, request, fmt.Errorf("delete current administrator account: %w", persistence.ErrConflict))
		return
	}
	if err := api.authentication.DeleteAdminUser(request.Context(), id); err != nil {
		api.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// writeAdministratorError reports password-check failures distinctly and delegates every other error.
func (api *API) writeAdministratorError(writer http.ResponseWriter, request *http.Request, err error) {
	var locked *auth.LockedError
	if errors.As(err, &locked) || errors.Is(err, auth.ErrPasswordMismatch) {
		api.writePasswordFailure(writer, request, err)
		return
	}
	api.writeError(writer, request, err)
}

// administratorFromSummary maps one account and marks the one that authenticated the request.
func administratorFromSummary(summary persistence.AdminUserSummary, currentUserID string) administratorResponse {
	return administratorResponse{
		ID: summary.ID, Username: summary.Username, CreatedAt: summary.CreatedAt,
		PasswordChangedAt: summary.PasswordChangedAt,
		Current:           currentUserID != "" && summary.ID == currentUserID,
	}
}
