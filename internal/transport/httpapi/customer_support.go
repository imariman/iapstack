package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// manualGrantRevokeMarker is the custom-method suffix of the revoke route.
	manualGrantRevokeMarker = ":revoke"
	// manualGrantMaxDurationDays bounds a relative grant duration.
	manualGrantMaxDurationDays = 3660
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

// manualGrantResultResponse reports the stored grant and what the projection refresh changed.
type manualGrantResultResponse struct {
	Grant              controlplane.ManualGrant `json:"grant"`
	Replay             bool                     `json:"replay"`
	ProjectionsChanged int                      `json:"projections_changed"`
	WebhooksEnqueued   int                      `json:"webhooks_enqueued"`
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
	principal, ok := api.requireAdmin(writer, request)
	if !ok || !api.requireDurableActor(writer, request, principal) {
		return
	}
	var input manualGrantRequest
	if !api.decode(writer, request, &input) {
		return
	}
	now := api.now()
	endsAt, err := manualGrantEnd(input, now)
	if !api.requireValidInput(writer, request, err) {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	write := persistence.ManualGrantWrite{
		ProjectID:      projectID,
		CustomerID:     core.CustomerID(request.PathValue("customer_id")),
		ApplicationID:  input.ApplicationID,
		ProductID:      input.ProductID,
		EntitlementID:  input.EntitlementID,
		Reason:         input.Reason,
		ActorKeyID:     principal.ActorID(),
		IdempotencyKey: input.IdempotencyKey,
		StartsAt:       now,
		EndsAt:         endsAt,
	}
	if !api.requireValidInput(writer, request, write.Validate()) {
		return
	}
	var result persistence.ManualGrantResult
	var refresh verification.ProjectionRefreshResult
	err = api.store.Transact(request.Context(), func(repository persistence.Transaction) error {
		var grantErr error
		refresh = verification.ProjectionRefreshResult{}
		result, grantErr = repository.CreateManualGrant(request.Context(), write)
		if grantErr != nil || result.Replay {
			return grantErr
		}
		refresh, grantErr = verification.RefreshCustomerProjectionWithOptions(
			request.Context(), repository, verification.NewDefaultProjector(),
			projectID, result.CanonicalCustomerID, now, true,
		)
		return grantErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	if !result.Replay {
		slog.Info(
			"manual grant created",
			slog.String("project_id", string(projectID)),
			slog.String("grant_id", result.Grant.ID),
			slog.Int("projections_changed", refresh.ProjectionsChanged),
		)
	}
	writeJSON(writer, http.StatusOK, manualGrantResultFromRecord(result, refresh, now))
}

// revokeAdminManualGrant ends one operator grant early and refreshes the normal projection and outbox.
func (api *API) revokeAdminManualGrant(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireAdmin(writer, request)
	if !ok || !api.requireDurableActor(writer, request, principal) {
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
	now := api.now()
	projectID := core.ProjectID(request.PathValue("project_id"))
	revocation := persistence.ManualGrantRevocation{
		ProjectID:  projectID,
		CustomerID: core.CustomerID(request.PathValue("customer_id")),
		GrantID:    grantID,
		Reason:     input.Reason,
		ActorKeyID: principal.ActorID(),
		RevokedAt:  now,
	}
	if !api.requireValidInput(writer, request, revocation.Validate()) {
		return
	}
	var result persistence.ManualGrantResult
	var refresh verification.ProjectionRefreshResult
	err := api.store.Transact(request.Context(), func(repository persistence.Transaction) error {
		var revokeErr error
		refresh = verification.ProjectionRefreshResult{}
		result, revokeErr = repository.RevokeManualGrant(request.Context(), revocation)
		if revokeErr != nil || result.Replay {
			return revokeErr
		}
		refresh, revokeErr = verification.RefreshCustomerProjectionWithOptions(
			request.Context(), repository, verification.NewDefaultProjector(),
			projectID, result.CanonicalCustomerID, now, true,
		)
		return revokeErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	if !result.Replay {
		slog.Info(
			"manual grant revoked",
			slog.String("project_id", string(projectID)),
			slog.String("grant_id", result.Grant.ID),
			slog.Int("projections_changed", refresh.ProjectionsChanged),
		)
	}
	writeJSON(writer, http.StatusOK, manualGrantResultFromRecord(result, refresh, now))
}

// now returns the API clock in UTC.
func (api *API) now() time.Time {
	if api.clock == nil {
		return time.Now().UTC()
	}
	return api.clock().UTC()
}

// manualGrantEnd resolves exactly one of a relative duration or an absolute end after now.
func manualGrantEnd(input manualGrantRequest, now time.Time) (time.Time, error) {
	switch {
	case input.DurationDays != nil && input.EndsAt != nil:
		return time.Time{}, errors.New("manual grant takes duration_days or ends_at, not both")
	case input.DurationDays != nil:
		if *input.DurationDays < 1 || *input.DurationDays > manualGrantMaxDurationDays {
			return time.Time{}, errors.New("manual grant duration_days is out of range")
		}
		return now.AddDate(0, 0, *input.DurationDays), nil
	case input.EndsAt != nil:
		endsAt := input.EndsAt.UTC()
		if !endsAt.After(now) {
			return time.Time{}, errors.New("manual grant ends_at must be in the future")
		}
		if endsAt.Sub(now) > persistence.ManualGrantMaxDuration {
			return time.Time{}, errors.New("manual grant ends_at is too far in the future")
		}
		return endsAt, nil
	default:
		return time.Time{}, errors.New("manual grant requires duration_days or ends_at")
	}
}

// manualGrantResultFromRecord maps one grant write and its projection refresh counts.
func manualGrantResultFromRecord(
	result persistence.ManualGrantResult,
	refresh verification.ProjectionRefreshResult,
	now time.Time,
) manualGrantResultResponse {
	return manualGrantResultResponse{
		Grant:              controlplane.ManualGrantView(result.Grant, now),
		Replay:             result.Replay,
		ProjectionsChanged: refresh.ProjectionsChanged,
		WebhooksEnqueued:   refresh.WebhooksEnqueued,
	}
}
