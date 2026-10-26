package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// entitlementConflictMessage is the stable client text for dual allowed customer merges.
	entitlementConflictMessage = "the customers have conflicting active entitlements"
	// identityActorForbiddenMessage rejects principals that have no stored API key.
	identityActorForbiddenMessage = "stored credentials are required"
)

// customerResolutionResponse is the public canonical identity for one external id.
type customerResolutionResponse struct {
	ProjectID           core.ProjectID  `json:"project_id"`
	ExternalID          string          `json:"external_id"`
	CanonicalCustomerID core.CustomerID `json:"canonical_customer_id"`
	Match               string          `json:"match"`
}

// customerMergeRequest selects the customer pair and the replay key for one merge.
type customerMergeRequest struct {
	IdempotencyKey      string          `json:"idempotency_key"`
	CanonicalCustomerID core.CustomerID `json:"canonical_customer_id"`
	SourceCustomerID    core.CustomerID `json:"source_customer_id"`
}

// customerMergeResponse is the stored merge audit row.
type customerMergeResponse struct {
	ID                  string          `json:"id"`
	ProjectID           core.ProjectID  `json:"project_id"`
	CanonicalCustomerID core.CustomerID `json:"canonical_customer_id"`
	SourceCustomerID    core.CustomerID `json:"source_customer_id"`
	IdempotencyKey      string          `json:"idempotency_key"`
	CreatedAt           time.Time       `json:"created_at"`
}

// putAdminCustomerAlias attaches an external id through a stored admin or dashboard session.
func (api *API) putAdminCustomerAlias(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireAdmin(writer, request)
	if !ok || !api.requireDurableActor(writer, request, principal) {
		return
	}
	api.putCustomerAlias(writer, request, core.ProjectID(request.PathValue("project_id")), principal)
}

// putApplicationCustomerAlias attaches an external id inside the application principal's project.
func (api *API) putApplicationCustomerAlias(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireApplication(writer, request)
	if !ok || !api.requireDurableActor(writer, request, principal) {
		return
	}
	api.putCustomerAlias(writer, request, principal.ProjectID, principal)
}

// getAdminCustomerResolution resolves an external id inside one admin project path.
func (api *API) getAdminCustomerResolution(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	api.getCustomerResolution(writer, request, core.ProjectID(request.PathValue("project_id")), "")
}

// getApplicationCustomerResolution resolves an external id inside the application principal's project.
func (api *API) getApplicationCustomerResolution(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireApplication(writer, request)
	if !ok {
		return
	}
	api.getCustomerResolution(writer, request, principal.ProjectID, principal.ApplicationID)
}

// postAdminCustomerMerge merges two customers inside one project.
func (api *API) postAdminCustomerMerge(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireAdmin(writer, request)
	if !ok || !api.requireDurableActor(writer, request, principal) {
		return
	}
	var input customerMergeRequest
	if !api.decode(writer, request, &input) {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	write := persistence.CustomerMergeWrite{
		ProjectID:           projectID,
		CanonicalCustomerID: input.CanonicalCustomerID,
		SourceCustomerID:    input.SourceCustomerID,
		IdempotencyKey:      input.IdempotencyKey,
		ActorKeyID:          principal.KeyID,
	}
	if input.CanonicalCustomerID != "" && input.CanonicalCustomerID == input.SourceCustomerID {
		api.writeError(writer, request, validation.Wrap(errors.New("canonical and source customers must differ")))
		return
	}
	if !api.requireValidInput(writer, request, write.Validate()) {
		return
	}
	occurredAt := time.Now().UTC()
	if api.clock != nil {
		occurredAt = api.clock().UTC()
	}
	var merge persistence.CustomerMerge
	err := api.store.Transact(request.Context(), func(repository persistence.Transaction) error {
		result, mergeErr := repository.MergeCustomers(request.Context(), write)
		if mergeErr != nil {
			return mergeErr
		}
		merge = result.Merge
		if result.Replay {
			return nil
		}
		return verification.RefreshCustomerProjection(
			request.Context(),
			repository,
			verification.NewDefaultProjector(),
			write.ProjectID,
			write.CanonicalCustomerID,
			occurredAt,
		)
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	slog.Info(
		"customer merge completed",
		slog.String("project_id", string(merge.ProjectID)),
		slog.String("canonical_customer_id", string(merge.CanonicalCustomerID)),
		slog.String("source_customer_id", string(merge.SourceCustomerID)),
		slog.String("merge_id", merge.ID),
	)
	writeJSON(writer, http.StatusOK, customerMergeFromRecord(merge))
}

// putCustomerAlias validates the path and stores one alias in the authenticated project.
func (api *API) putCustomerAlias(
	writer http.ResponseWriter,
	request *http.Request,
	projectID core.ProjectID,
	principal auth.Principal,
) {
	var ignored struct{}
	if !api.decode(writer, request, &ignored) {
		return
	}
	customerID := core.CustomerID(request.PathValue("customer_id"))
	externalID := request.PathValue("external_id")
	write := persistence.CustomerAliasWrite{
		ProjectID:           projectID,
		CanonicalCustomerID: customerID,
		ExternalID:          externalID,
		ActorRole:           principal.Role,
		ActorKeyID:          principal.KeyID,
	}
	if !api.requireValidInput(writer, request, write.Validate()) {
		return
	}
	var resolution persistence.CustomerResolution
	err := api.store.Transact(request.Context(), func(repository persistence.Transaction) error {
		if principal.Role == persistence.APIKeyRoleApplication {
			if _, loadErr := repository.Application(request.Context(), projectID, principal.ApplicationID); loadErr != nil {
				return loadErr
			}
		}
		var putErr error
		resolution, putErr = repository.PutCustomerAlias(request.Context(), write)
		return putErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, customerResolutionFromRecord(resolution))
}

// getCustomerResolution reads one canonical customer for an external id in the authenticated project.
func (api *API) getCustomerResolution(
	writer http.ResponseWriter,
	request *http.Request,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) {
	externalID := request.PathValue("external_id")
	if !api.requireValidInput(writer, request, errors.Join(projectID.Validate(), core.ValidateExternalCustomerID(externalID))) {
		return
	}
	var resolution persistence.CustomerResolution
	err := api.store.Transact(request.Context(), func(repository persistence.Transaction) error {
		if applicationID != "" {
			if _, loadErr := repository.Application(request.Context(), projectID, applicationID); loadErr != nil {
				return loadErr
			}
		}
		var resolveErr error
		resolution, resolveErr = repository.ResolveCustomer(request.Context(), projectID, externalID)
		return resolveErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, customerResolutionFromRecord(resolution))
}

// requireDurableActor rejects the bootstrap administrator because it has no stored key id.
func (api *API) requireDurableActor(
	writer http.ResponseWriter,
	request *http.Request,
	principal auth.Principal,
) bool {
	if principal.KeyID == "" {
		writeAPIError(writer, request, http.StatusForbidden, "forbidden", identityActorForbiddenMessage)
		return false
	}
	return true
}

// customerResolutionFromRecord maps one stored resolution onto the public JSON contract.
func customerResolutionFromRecord(resolution persistence.CustomerResolution) customerResolutionResponse {
	return customerResolutionResponse{
		ProjectID:           resolution.ProjectID,
		ExternalID:          resolution.ExternalID,
		CanonicalCustomerID: resolution.CanonicalCustomerID,
		Match:               resolution.Match,
	}
}

// customerMergeFromRecord maps one stored merge audit row onto the public JSON contract.
func customerMergeFromRecord(merge persistence.CustomerMerge) customerMergeResponse {
	return customerMergeResponse{
		ID:                  merge.ID,
		ProjectID:           merge.ProjectID,
		CanonicalCustomerID: merge.CanonicalCustomerID,
		SourceCustomerID:    merge.SourceCustomerID,
		IdempotencyKey:      merge.IdempotencyKey,
		CreatedAt:           merge.CreatedAt,
	}
}
