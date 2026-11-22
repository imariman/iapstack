package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// recentConsumptionRequestLimit is how many consumption requests the administrative view returns.
	recentConsumptionRequestLimit = 20
)

var (
	// errConsumptionRequiresApple rejects consumption settings and consent for non-App Store applications.
	errConsumptionRequiresApple = errors.New("consumption answers apply only to App Store applications")
)

// appleConsumptionSettingsRequest is one operator write of an application's consumption settings.
// Booleans are pointers so a missing field is rejected instead of read as false.
type appleConsumptionSettingsRequest struct {
	Enabled               *bool  `json:"enabled"`
	RefundPreference      string `json:"refund_preference"`
	SampleContentProvided *bool  `json:"sample_content_provided"`
	ExpectedRevision      int64  `json:"expected_revision"`
}

// appleConsumptionSettingsResponse is the public view of one application's consumption settings.
type appleConsumptionSettingsResponse struct {
	ProjectID             core.ProjectID     `json:"project_id"`
	ApplicationID         core.ApplicationID `json:"application_id"`
	Enabled               bool               `json:"enabled"`
	RefundPreference      string             `json:"refund_preference"`
	SampleContentProvided bool               `json:"sample_content_provided"`
	Revision              int64              `json:"revision"`
	UpdatedAt             *time.Time         `json:"updated_at,omitempty"`
}

// appleConsumptionStatusResponse combines settings with the newest consumption requests.
type appleConsumptionStatusResponse struct {
	Settings       appleConsumptionSettingsResponse  `json:"settings"`
	RecentRequests []appleConsumptionRequestResponse `json:"recent_requests"`
}

// appleConsumptionRequestResponse is one consumption request without protected identifiers.
type appleConsumptionRequestResponse struct {
	ID                 string                 `json:"id"`
	CustomerID         core.CustomerID        `json:"customer_id,omitempty"`
	ProviderProductID  core.ProviderProductID `json:"provider_product_id"`
	ProductType        string                 `json:"product_type"`
	CustomerReason     string                 `json:"customer_reason,omitempty"`
	Status             string                 `json:"status"`
	StatusReason       string                 `json:"status_reason,omitempty"`
	AttemptCount       int                    `json:"attempt_count"`
	SignedAt           time.Time              `json:"signed_at"`
	ReceivedAt         time.Time              `json:"received_at"`
	DeadlineAt         time.Time              `json:"deadline_at"`
	CompletedAt        *time.Time             `json:"completed_at,omitempty"`
	LastHTTPStatus     *int                   `json:"last_http_status,omitempty"`
	LastAppleErrorCode *int64                 `json:"last_apple_error_code,omitempty"`
}

// appleConsumptionConsentRequest records one customer's consent decision.
type appleConsumptionConsentRequest struct {
	Consented *bool `json:"consented"`
}

// appleConsumptionConsentResponse is the stored consent decision for one customer.
type appleConsumptionConsentResponse struct {
	ApplicationID      core.ApplicationID `json:"application_id"`
	CustomerID         core.CustomerID    `json:"customer_id"`
	ExternalCustomerID string             `json:"external_customer_id"`
	Consented          bool               `json:"consented"`
	UpdatedAt          time.Time          `json:"updated_at"`
}

// getAdminAppleConsumption returns consumption settings and recent requests for one App Store application.
func (api *API) getAdminAppleConsumption(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	applicationID := core.ApplicationID(request.PathValue("application_id"))
	if !api.requireValidInput(writer, request, errors.Join(projectID.Validate(), applicationID.Validate())) {
		return
	}
	var response appleConsumptionStatusResponse
	err := api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		application, loadErr := repository.Application(request.Context(), projectID, applicationID)
		if loadErr != nil {
			return loadErr
		}
		if application.Store.Provider != core.ProviderAppleAppStore {
			return validation.Wrap(errConsumptionRequiresApple)
		}
		settings, loadErr := repository.AppleConsumptionSettings(request.Context(), projectID, applicationID)
		if errors.Is(loadErr, persistence.ErrNotFound) {
			settings, loadErr = persistence.DefaultAppleConsumptionSettings(projectID, applicationID), nil
		}
		if loadErr != nil {
			return loadErr
		}
		requests, loadErr := repository.RecentAppleConsumptionRequests(
			request.Context(), projectID, applicationID, recentConsumptionRequestLimit,
		)
		if loadErr != nil {
			return loadErr
		}
		response = appleConsumptionStatusResponse{
			Settings:       consumptionSettingsResponse(settings),
			RecentRequests: make([]appleConsumptionRequestResponse, 0, len(requests)),
		}
		for _, summary := range requests {
			response.RecentRequests = append(response.RecentRequests, consumptionRequestResponse(summary))
		}
		return nil
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

// putAdminAppleConsumption saves one App Store application's consumption settings with revision control.
func (api *API) putAdminAppleConsumption(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireAdmin(writer, request)
	if !ok {
		return
	}
	var input appleConsumptionSettingsRequest
	if !api.decode(writer, request, &input) {
		return
	}
	if input.Enabled == nil || input.SampleContentProvided == nil {
		api.writeError(writer, request, validation.Wrap(errors.New("enabled and sample_content_provided are required")))
		return
	}
	write := persistence.AppleConsumptionSettingsWrite{
		ProjectID:             core.ProjectID(request.PathValue("project_id")),
		ApplicationID:         core.ApplicationID(request.PathValue("application_id")),
		Enabled:               *input.Enabled,
		RefundPreference:      input.RefundPreference,
		SampleContentProvided: *input.SampleContentProvided,
		ExpectedRevision:      input.ExpectedRevision,
		ActorKeyID:            principal.ActorID(),
	}
	if !api.requireValidInput(writer, request, write.Validate()) {
		return
	}
	var settings persistence.AppleConsumptionSettings
	err := api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		application, loadErr := repository.Application(request.Context(), write.ProjectID, write.ApplicationID)
		if loadErr != nil {
			return loadErr
		}
		if application.Store.Provider != core.ProviderAppleAppStore {
			return validation.Wrap(errConsumptionRequiresApple)
		}
		var putErr error
		settings, putErr = repository.PutAppleConsumptionSettings(request.Context(), write)
		return putErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	api.logger.Info("apple consumption settings saved",
		"project_id", string(settings.ProjectID), "application_id", string(settings.ApplicationID),
		"enabled", settings.Enabled, "refund_preference", settings.RefundPreference, "revision", settings.Revision,
	)
	writeJSON(writer, http.StatusOK, consumptionSettingsResponse(settings))
}

// putApplicationAppleConsumptionConsent records one customer's consent through the durable application bearer.
// Only the trusted host backend may write consent. requireApplication accepts stored API keys only, so a
// short-lived customer session, which the mobile app holds, is rejected with 401.
func (api *API) putApplicationAppleConsumptionConsent(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireApplication(writer, request)
	if !ok || !api.requireDurableActor(writer, request, principal) {
		return
	}
	var input appleConsumptionConsentRequest
	if !api.decode(writer, request, &input) {
		return
	}
	externalCustomerID := request.PathValue("external_customer_id")
	if input.Consented == nil {
		api.writeError(writer, request, validation.Wrap(errors.New("consented is required")))
		return
	}
	if !api.requireValidInput(writer, request, core.ValidateExternalCustomerID(externalCustomerID)) {
		return
	}
	var consent persistence.AppleConsumptionConsent
	err := api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		application, loadErr := repository.Application(request.Context(), principal.ProjectID, principal.ApplicationID)
		if loadErr != nil {
			return loadErr
		}
		if application.Store.Provider != core.ProviderAppleAppStore {
			return validation.Wrap(errConsumptionRequiresApple)
		}
		customer, loadErr := repository.CustomerByExternalID(request.Context(), principal.ProjectID, externalCustomerID)
		if loadErr != nil {
			return loadErr
		}
		var putErr error
		consent, putErr = repository.PutAppleConsumptionConsent(request.Context(), persistence.AppleConsumptionConsentWrite{
			ProjectID: principal.ProjectID, ApplicationID: principal.ApplicationID,
			CustomerID: customer.ID, Consented: *input.Consented, ActorKeyID: principal.ActorID(),
		})
		return putErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, appleConsumptionConsentResponse{
		ApplicationID: consent.ApplicationID, CustomerID: consent.CustomerID,
		ExternalCustomerID: externalCustomerID, Consented: consent.Consented, UpdatedAt: consent.UpdatedAt,
	})
}

// consumptionSettingsResponse maps stored or default settings onto the public contract.
func consumptionSettingsResponse(settings persistence.AppleConsumptionSettings) appleConsumptionSettingsResponse {
	response := appleConsumptionSettingsResponse{
		ProjectID: settings.ProjectID, ApplicationID: settings.ApplicationID, Enabled: settings.Enabled,
		RefundPreference:      settings.RefundPreference,
		SampleContentProvided: settings.SampleContentProvided, Revision: settings.Revision,
	}
	if !settings.UpdatedAt.IsZero() {
		updatedAt := settings.UpdatedAt.UTC()
		response.UpdatedAt = &updatedAt
	}
	return response
}

// consumptionRequestResponse maps one stored request summary onto the public contract.
func consumptionRequestResponse(summary persistence.AppleConsumptionRequestSummary) appleConsumptionRequestResponse {
	response := appleConsumptionRequestResponse{
		ID: summary.ID, CustomerID: summary.CustomerID, ProviderProductID: summary.ProviderProductID,
		ProductType: summary.ProductType, CustomerReason: summary.CustomerReason, Status: summary.Status,
		StatusReason: summary.StatusReason, AttemptCount: summary.AttemptCount,
		SignedAt: summary.SignedAt.UTC(), ReceivedAt: summary.ReceivedAt.UTC(), DeadlineAt: summary.DeadlineAt.UTC(),
	}
	if summary.CompletedAt != nil {
		completedAt := summary.CompletedAt.UTC()
		response.CompletedAt = &completedAt
	}
	if summary.LastHTTPStatus != 0 {
		status := summary.LastHTTPStatus
		response.LastHTTPStatus = &status
	}
	if summary.LastAppleErrorCode != 0 {
		code := summary.LastAppleErrorCode
		response.LastAppleErrorCode = &code
	}
	return response
}
