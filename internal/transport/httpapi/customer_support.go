package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// manualGrantRevokeMarker is the custom-method suffix of the revoke route.
	manualGrantRevokeMarker = ":revoke"
)

// manualGrantRequest asks for one audited grant for a relative duration or until an absolute end.
type manualGrantRequest struct {
	IdempotencyKey string             `json:"idempotency_key"`
	EntitlementID  core.EntitlementID `json:"entitlement_id"`
	ProductID      core.ProductID     `json:"product_id"`
	ApplicationID  core.ApplicationID `json:"application_id"`
	Reason         string             `json:"reason"`
	DurationDays   *int               `json:"duration_days,omitempty"`
	EndsAt         *time.Time         `json:"ends_at,omitempty"`
}

// manualGrantRevocationRequest carries the required reason for an early revocation.
type manualGrantRevocationRequest struct {
	Reason string `json:"reason"`
}

// searchAdminCustomers returns one page of project customers matching an identifier or transaction id.
func (api *API) searchAdminCustomers(writer http.ResponseWriter, request *http.Request) {
	actor, ok := api.requireAdminActor(writer, request)
	if !ok {
		return
	}
	values := request.URL.Query()
	input := controlplane.CustomerSearchInput{
		ProjectID: core.ProjectID(request.PathValue("project_id")),
		Query:     values.Get("query"),
		PageToken: values.Get("page_token"),
	}
	if rawLimit := values.Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			api.writeError(writer, request, validation.Wrap(errors.New("search limit is out of range")))
			return
		}
		input.Limit = &parsed
	}
	page, err := api.controlPlane().SearchCustomers(request.Context(), actor, input)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, page)
}

// getAdminCustomer returns identity, access sources, manual grants, and a bounded timeline for one customer.
func (api *API) getAdminCustomer(writer http.ResponseWriter, request *http.Request) {
	actor, ok := api.requireAdminActor(writer, request)
	if !ok {
		return
	}
	detail, err := api.controlPlane().Customer(
		request.Context(), actor,
		core.ProjectID(request.PathValue("project_id")), core.CustomerID(request.PathValue("customer_id")),
	)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

// createAdminManualGrant records one audited operator grant and refreshes the normal projection and outbox.
func (api *API) createAdminManualGrant(writer http.ResponseWriter, request *http.Request) {
	actor, ok := api.requireDurableAdminActor(writer, request)
	if !ok {
		return
	}
	var input manualGrantRequest
	if !api.decode(writer, request, &input) {
		return
	}
	result, err := api.controlPlane().CreateManualGrant(request.Context(), actor, controlplane.ManualGrantInput{
		ProjectID:      core.ProjectID(request.PathValue("project_id")),
		CustomerID:     core.CustomerID(request.PathValue("customer_id")),
		IdempotencyKey: input.IdempotencyKey,
		EntitlementID:  input.EntitlementID,
		ProductID:      input.ProductID,
		ApplicationID:  input.ApplicationID,
		Reason:         input.Reason,
		DurationDays:   input.DurationDays,
		EndsAt:         input.EndsAt,
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

// revokeAdminManualGrant ends one operator grant early and refreshes the normal projection and outbox.
func (api *API) revokeAdminManualGrant(writer http.ResponseWriter, request *http.Request) {
	actor, ok := api.requireDurableAdminActor(writer, request)
	if !ok {
		return
	}
	grantID, found := strings.CutSuffix(request.PathValue("grant_id"), manualGrantRevokeMarker)
	if !found || grantID == "" || strings.Contains(grantID, ":") {
		api.notFound(writer, request)
		return
	}
	var input manualGrantRevocationRequest
	if !api.decode(writer, request, &input) {
		return
	}
	result, err := api.controlPlane().RevokeManualGrant(request.Context(), actor, controlplane.ManualGrantRevocationInput{
		ProjectID:  core.ProjectID(request.PathValue("project_id")),
		CustomerID: core.CustomerID(request.PathValue("customer_id")),
		GrantID:    grantID,
		Reason:     input.Reason,
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

// now returns the API clock in UTC.
func (api *API) now() time.Time {
	if api.clock == nil {
		return time.Now().UTC()
	}
	return api.clock().UTC()
}
