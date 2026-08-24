// Package httpapi exposes IAPStack's versioned administration and application JSON contracts.
package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/credentials"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/huawei"
	"github.com/imariman/iapstack/internal/verification"
	"github.com/imariman/iapstack/internal/webhooks"
)

const (
	// inboxProtectionPurpose authenticates protected Huawei notification bytes.
	inboxProtectionPurpose = "inbox_notification"
	// reconciliationProtectionPurpose authenticates protected periodic verification requests.
	reconciliationProtectionPurpose = "reconciliation_request"
	// defaultReconciliationDelay schedules a conservative daily authoritative refresh.
	defaultReconciliationDelay = 24 * time.Hour
)

// API owns versioned routes and application services without direct database access.
type API struct {
	store          persistence.Store
	operations     persistence.OperationsStore
	authentication *auth.Service
	credentials    *credentials.Service
	webhooks       *webhooks.Service
	verification   *verification.Service
	huawei         *huawei.Adapter
	protection     protection.Service
	bodyLimit      int64
	clock          func() time.Time
	handler        http.Handler
}

// Dependencies contains the services required by the public HTTP contract.
type Dependencies struct {
	Store          persistence.Store
	Operations     persistence.OperationsStore
	Authentication *auth.Service
	Credentials    *credentials.Service
	Webhooks       *webhooks.Service
	Verification   *verification.Service
	Huawei         *huawei.Adapter
	Protection     protection.Service
	BodyLimit      int64
}

// errorEnvelope is the stable v1 failure contract.
type errorEnvelope struct {
	Error apiError `json:"error"`
}

// apiError describes one safe machine-readable HTTP failure.
type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// applicationRequest describes one store-scoped application configuration.
type applicationRequest struct {
	Provider              core.Provider              `json:"provider"`
	Environment           core.Environment           `json:"environment"`
	ProviderApplicationID core.ProviderApplicationID `json:"provider_application_id"`
}

// customerRequest creates one stable external customer binding.
type customerRequest struct {
	ExternalID string `json:"external_id"`
}

// entitlementRequest creates one named entitlement definition.
type entitlementRequest struct {
	Key string `json:"key"`
}

// productRequest creates one product and its entitlement grants.
type productRequest struct {
	Kind           core.ProductKind     `json:"kind"`
	EntitlementIDs []core.EntitlementID `json:"entitlement_ids"`
}

// storeProductRequest creates one provider product mapping.
type storeProductRequest struct {
	ProductID core.ProductID `json:"product_id"`
}

// credentialRequest carries one opaque provider-owned credential revision.
type credentialRequest struct {
	ContentType      string          `json:"content_type"`
	SchemaVersion    int             `json:"schema_version"`
	ExpectedRevision int64           `json:"expected_revision"`
	Payload          json.RawMessage `json:"payload"`
}

// webhookRequest carries a plaintext signing secret only across the explicit protection boundary.
type webhookRequest struct {
	URL              string `json:"url"`
	SigningSecret    string `json:"signing_secret"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// keyRequest selects the durable role and optional application scope for a new bearer.
type keyRequest struct {
	Role          persistence.APIKeyRole `json:"role"`
	ProjectID     core.ProjectID         `json:"project_id,omitempty"`
	ApplicationID core.ApplicationID     `json:"application_id,omitempty"`
}

// verificationRequest is the public alias of the protected worker verification payload.
type verificationRequest = jobs.VerificationPayload

// restoreRequest groups purchase submissions for deterministic sequential processing.
type restoreRequest struct {
	Purchases []verificationRequest `json:"purchases"`
}

// entitlementResponse is one stable current entitlement projection.
type entitlementResponse struct {
	Key               string            `json:"key"`
	Access            core.AccessStatus `json:"access"`
	Reason            core.AccessReason `json:"reason"`
	EffectiveStartsAt *time.Time        `json:"effective_starts_at,omitempty"`
	EffectiveEndsAt   *time.Time        `json:"effective_ends_at,omitempty"`
	Version           int64             `json:"version"`
}

// verificationResponse contains the resulting customer entitlement snapshot.
type verificationResponse struct {
	VerifiedAt   time.Time             `json:"verified_at"`
	CustomerID   core.CustomerID       `json:"customer_id"`
	Entitlements []entitlementResponse `json:"entitlements"`
}

// New validates dependencies and registers the complete v1 route surface.
func New(dependencies Dependencies) (*API, error) {
	if dependencies.Store == nil || dependencies.Operations == nil || dependencies.Authentication == nil ||
		dependencies.Credentials == nil || dependencies.Webhooks == nil || dependencies.Verification == nil ||
		dependencies.Huawei == nil || dependencies.Protection == nil || dependencies.BodyLimit <= 0 {
		return nil, errors.New("HTTP API dependencies are incomplete")
	}
	api := &API{
		store: dependencies.Store, operations: dependencies.Operations,
		authentication: dependencies.Authentication, credentials: dependencies.Credentials,
		webhooks: dependencies.Webhooks, verification: dependencies.Verification,
		huawei: dependencies.Huawei, protection: dependencies.Protection,
		bodyLimit: dependencies.BodyLimit, clock: time.Now,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/admin/api-keys", api.createAPIKey)
	mux.HandleFunc("PUT /v1/admin/projects/{project_id}", api.putProject)
	mux.HandleFunc("PUT /v1/admin/projects/{project_id}/applications/{application_id}", api.putApplication)
	mux.HandleFunc("PUT /v1/admin/projects/{project_id}/customers/{customer_id}", api.putCustomer)
	mux.HandleFunc("PUT /v1/admin/projects/{project_id}/entitlements/{entitlement_id}", api.putEntitlement)
	mux.HandleFunc("PUT /v1/admin/projects/{project_id}/products/{product_id}", api.putProduct)
	mux.HandleFunc("PUT /v1/admin/projects/{project_id}/applications/{application_id}/store-products/{provider_product_id}", api.putStoreProduct)
	mux.HandleFunc("PUT /v1/admin/projects/{project_id}/applications/{application_id}/credentials/{kind}", api.putCredential)
	mux.HandleFunc("PUT /v1/admin/projects/{project_id}/applications/{application_id}/webhook", api.putWebhook)
	mux.HandleFunc("POST /v1/applications/{application_id}/purchases:verify", api.verifyPurchase)
	mux.HandleFunc("POST /v1/applications/{application_id}/purchases:restore", api.restorePurchases)
	mux.HandleFunc("GET /v1/applications/{application_id}/customers/{external_customer_id}/entitlements", api.customerEntitlements)
	mux.HandleFunc("POST /v1/providers/huawei/applications/{application_id}/notifications", api.huaweiNotification)
	mux.HandleFunc("/v1/", api.notFound)
	api.handler = mux
	return api, nil
}

// ServeHTTP dispatches one versioned API request.
func (api *API) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	api.handler.ServeHTTP(writer, request)
}

// notFound returns the stable JSON envelope for unmatched versioned routes.
func (api *API) notFound(writer http.ResponseWriter, request *http.Request) {
	writeAPIError(writer, request, http.StatusNotFound, "not_found", "the requested resource was not found")
}

// createAPIKey creates a stored administrator or application bearer and returns it once.
func (api *API) createAPIKey(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	var input keyRequest
	if !api.decode(writer, request, &input) {
		return
	}
	bearer, err := api.authentication.Create(request.Context(), auth.Principal{
		Role: input.Role, ProjectID: input.ProjectID, ApplicationID: input.ApplicationID,
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]string{"key": bearer})
}

// putProject idempotently creates one project identity.
func (api *API) putProject(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	err := api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		return repository.PutProject(request.Context(), projectID)
	})
	api.writeMutation(writer, request, err)
}

// putApplication idempotently creates one provider application.
func (api *API) putApplication(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	var input applicationRequest
	if !api.decode(writer, request, &input) {
		return
	}
	application := core.Application{
		ID:        core.ApplicationID(request.PathValue("application_id")),
		ProjectID: core.ProjectID(request.PathValue("project_id")),
		Store:     core.StoreApplication{Provider: input.Provider, Environment: input.Environment, ID: input.ProviderApplicationID},
	}
	err := api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		return repository.PutApplication(request.Context(), application)
	})
	api.writeMutation(writer, request, err)
}

// putCustomer idempotently creates one project-scoped external customer identity.
func (api *API) putCustomer(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	var input customerRequest
	if !api.decode(writer, request, &input) {
		return
	}
	customer := core.Customer{ID: core.CustomerID(request.PathValue("customer_id")),
		ProjectID: core.ProjectID(request.PathValue("project_id")), ExternalID: input.ExternalID}
	var stored core.Customer
	err := api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		var putErr error
		stored, putErr = repository.PutCustomer(request.Context(), customer)
		return putErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{
		"id": string(stored.ID), "project_id": string(stored.ProjectID), "external_id": stored.ExternalID,
	})
}

// putEntitlement idempotently creates one named entitlement definition.
func (api *API) putEntitlement(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	var input entitlementRequest
	if !api.decode(writer, request, &input) {
		return
	}
	entitlement := core.Entitlement{ID: core.EntitlementID(request.PathValue("entitlement_id")),
		ProjectID: core.ProjectID(request.PathValue("project_id")), Key: input.Key}
	err := api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		return repository.PutEntitlementDefinition(request.Context(), entitlement)
	})
	api.writeMutation(writer, request, err)
}

// putProduct idempotently creates one product and entitlement grant set.
func (api *API) putProduct(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	var input productRequest
	if !api.decode(writer, request, &input) {
		return
	}
	product := core.Product{ID: core.ProductID(request.PathValue("product_id")),
		ProjectID: core.ProjectID(request.PathValue("project_id")), Kind: input.Kind,
		EntitlementIDs: input.EntitlementIDs}
	err := api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		return repository.PutProduct(request.Context(), product)
	})
	api.writeMutation(writer, request, err)
}

// putStoreProduct idempotently maps one provider product to an internal product.
func (api *API) putStoreProduct(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	var input storeProductRequest
	if !api.decode(writer, request, &input) {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	mapping := core.StoreProduct{ApplicationID: core.ApplicationID(request.PathValue("application_id")),
		ProductID: input.ProductID, ProviderID: core.ProviderProductID(request.PathValue("provider_product_id"))}
	err := api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		return repository.PutStoreProduct(request.Context(), projectID, mapping)
	})
	api.writeMutation(writer, request, err)
}

// putCredential protects and rotates one opaque provider credential package.
func (api *API) putCredential(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	var input credentialRequest
	if !api.decode(writer, request, &input) {
		return
	}
	defer zero(input.Payload)
	application, err := api.application(request.Context(), request.PathValue("project_id"), request.PathValue("application_id"))
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	credential, err := stores.NewCredential(stores.CredentialKind(request.PathValue("kind")),
		input.ContentType, input.SchemaVersion, input.Payload)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	metadata, err := api.credentials.Put(request.Context(), application, credential, input.ExpectedRevision)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, metadata)
}

// putWebhook protects and rotates one application webhook configuration.
func (api *API) putWebhook(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	var input webhookRequest
	if !api.decode(writer, request, &input) {
		return
	}
	application, err := api.application(request.Context(), request.PathValue("project_id"), request.PathValue("application_id"))
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	secret := []byte(input.SigningSecret)
	defer zero(secret)
	record, err := api.webhooks.Configure(request.Context(), application, input.URL, secret, input.ExpectedRevision)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"url": record.URL, "revision": record.Revision})
}

// verifyPurchase verifies one signed purchase and returns the current entitlement snapshot.
func (api *API) verifyPurchase(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireApplication(writer, request)
	if !ok {
		return
	}
	var input verificationRequest
	if !api.decode(writer, request, &input) {
		return
	}
	defer zero(input.Evidence)
	result, err := api.verify(request.Context(), principal, input)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, responseFromResult(result))
}

// restorePurchases verifies a bounded list sequentially so each item remains independently idempotent.
func (api *API) restorePurchases(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireApplication(writer, request)
	if !ok {
		return
	}
	var input restoreRequest
	if !api.decode(writer, request, &input) {
		return
	}
	if len(input.Purchases) == 0 || len(input.Purchases) > 100 {
		writeAPIError(writer, request, http.StatusBadRequest, "invalid_request", "purchases must contain between 1 and 100 items")
		return
	}
	defer func() {
		for index := range input.Purchases {
			zero(input.Purchases[index].Evidence)
		}
	}()
	results := make([]verificationResponse, 0, len(input.Purchases))
	for _, purchase := range input.Purchases {
		result, err := api.verify(request.Context(), principal, purchase)
		if err != nil {
			api.writeError(writer, request, err)
			return
		}
		results = append(results, responseFromResult(result))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"results": results})
}

// customerEntitlements returns one application-scoped customer's current projection snapshot.
func (api *API) customerEntitlements(writer http.ResponseWriter, request *http.Request) {
	principal, ok := api.requireApplication(writer, request)
	if !ok {
		return
	}
	externalID := request.PathValue("external_customer_id")
	var customer core.Customer
	var entitlements []persistence.CustomerEntitlement
	err := api.store.Transact(request.Context(), func(repository persistence.Transaction) error {
		var loadErr error
		customer, loadErr = repository.CustomerByExternalID(request.Context(), principal.ProjectID, externalID)
		if loadErr != nil {
			return loadErr
		}
		entitlements, loadErr = repository.CustomerEntitlements(request.Context(), principal.ProjectID, customer.ID)
		return loadErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"customer_id": customer.ID, "entitlements": entitlementResponses(entitlements)})
}

// huaweiNotification verifies local signature and scope before durable protected inbox insertion.
func (api *API) huaweiNotification(writer http.ResponseWriter, request *http.Request) {
	payload, ok := api.readBody(writer, request)
	if !ok {
		return
	}
	defer zero(payload)
	applicationID := core.ApplicationID(request.PathValue("application_id"))
	projectID := core.ProjectID(strings.TrimSpace(request.Header.Get("X-IAPStack-Project-ID")))
	var application core.Application
	err := api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		var loadErr error
		application, loadErr = repository.Application(request.Context(), projectID, applicationID)
		return loadErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	if _, err := api.huawei.ValidateNotification(request.Context(), application, payload); err != nil {
		api.writeError(writer, request, err)
		return
	}
	protected, err := api.protect(request.Context(), application, inboxProtectionPurpose, payload)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	now := api.clock().UTC()
	id := deterministicID("inbox", protected.Fingerprint)
	err = api.operations.Operate(request.Context(), func(repository persistence.OperationsTransaction) error {
		_, saveErr := repository.SaveInboxMessage(request.Context(), persistence.InboxMessage{
			ID: id, ProjectID: application.ProjectID, ApplicationID: application.ID,
			Provider: application.Store.Provider, Kind: "purchase_lifecycle",
			ContentType: "application/json", Payload: protected, ReceivedAt: now, AvailableAt: now,
		})
		return saveErr
	})
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "accepted"})
}

// verify converts one API item into the provider-neutral verification command.
func (api *API) verify(
	ctx context.Context,
	principal auth.Principal,
	input verificationRequest,
) (verification.Result, error) {
	evidence, err := stores.NewEvidence(huawei.EvidenceContentType, input.Evidence)
	if err != nil {
		return verification.Result{}, err
	}
	bindings := make([]core.StoreReference, 0, len(input.CustomerBindings)+1)
	authoritativeBinding, err := core.NewStoreReference(
		core.ReferenceCustomerBinding,
		"developer_payload",
		input.ExternalCustomerID,
	)
	if err != nil {
		return verification.Result{}, err
	}
	bindings = append(bindings, authoritativeBinding)
	for _, binding := range input.CustomerBindings {
		if binding.Kind == "developer_payload" {
			if binding.Value != input.ExternalCustomerID {
				return verification.Result{}, errors.New("developer payload customer binding mismatch")
			}
			continue
		}
		reference, err := core.NewStoreReference(core.ReferenceCustomerBinding, binding.Kind, binding.Value)
		if err != nil {
			return verification.Result{}, err
		}
		bindings = append(bindings, reference)
	}
	result, err := api.verification.Verify(ctx, verification.Command{
		ProjectID: principal.ProjectID, ApplicationID: principal.ApplicationID,
		ExternalCustomerID: input.ExternalCustomerID, ClaimedProducts: input.ClaimedProducts,
		ExpectedCustomerBindings: bindings, Evidence: evidence,
	})
	if err != nil {
		return verification.Result{}, err
	}
	if requiresReconciliation(input.Evidence) {
		if err := api.scheduleReconciliation(ctx, principal, result.Customer.ID, input); err != nil {
			return verification.Result{}, err
		}
	}
	return result, nil
}

// scheduleReconciliation stores a protected idempotent daily authoritative refresh request.
func (api *API) scheduleReconciliation(
	ctx context.Context,
	principal auth.Principal,
	customerID core.CustomerID,
	input verificationRequest,
) error {
	availableAt := nextReconciliationTime(api.clock().UTC())
	payload, err := json.Marshal(jobs.ReconciliationPayload{Verification: input, ScheduledFor: availableAt})
	if err != nil {
		return err
	}
	application, err := api.application(ctx, string(principal.ProjectID), string(principal.ApplicationID))
	if err != nil {
		return err
	}
	protected, err := api.protect(ctx, application, reconciliationProtectionPurpose, payload)
	if err != nil {
		return err
	}
	return api.operations.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		_, saveErr := repository.SaveReconciliationJob(ctx, persistence.ReconciliationJob{
			ID: deterministicID("reconcile", protected.Fingerprint), ProjectID: principal.ProjectID,
			ApplicationID: principal.ApplicationID, CustomerID: customerID,
			Payload: protected, AvailableAt: availableAt,
		})
		return saveErr
	})
}

// requireAdmin authenticates one control-plane bearer.
func (api *API) requireAdmin(writer http.ResponseWriter, request *http.Request) (auth.Principal, bool) {
	principal, err := api.authenticate(request)
	if err != nil || principal.Role != persistence.APIKeyRoleAdmin {
		writeAPIError(writer, request, http.StatusUnauthorized, "unauthorized", "authentication required")
		return auth.Principal{}, false
	}
	return principal, true
}

// requireApplication authenticates and path-binds one application bearer.
func (api *API) requireApplication(writer http.ResponseWriter, request *http.Request) (auth.Principal, bool) {
	principal, err := api.authenticate(request)
	if err != nil || principal.Role != persistence.APIKeyRoleApplication ||
		string(principal.ApplicationID) != request.PathValue("application_id") {
		writeAPIError(writer, request, http.StatusUnauthorized, "unauthorized", "authentication required")
		return auth.Principal{}, false
	}
	return principal, true
}

// authenticate extracts one strict bearer token without logging it.
func (api *API) authenticate(request *http.Request) (auth.Principal, error) {
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || strings.Contains(strings.TrimPrefix(header, "Bearer "), " ") {
		return auth.Principal{}, auth.ErrUnauthorized
	}
	return api.authentication.Authenticate(request.Context(), strings.TrimPrefix(header, "Bearer "))
}

// application loads one authoritative project-scoped application.
func (api *API) application(ctx context.Context, project, application string) (core.Application, error) {
	var result core.Application
	err := api.store.Transact(ctx, func(repository persistence.Transaction) error {
		var loadErr error
		result, loadErr = repository.Application(ctx, core.ProjectID(project), core.ApplicationID(application))
		return loadErr
	})
	return result, err
}

// protect applies one exact application scope to durable queue plaintext.
func (api *API) protect(
	ctx context.Context,
	application core.Application,
	purpose string,
	payload []byte,
) (protection.Value, error) {
	request, err := protection.NewRequest(protection.Scope{
		ProjectID: application.ProjectID, ApplicationID: application.ID, Purpose: purpose,
	}, payload)
	if err != nil {
		return protection.Value{}, err
	}
	return api.protection.Protect(ctx, request)
}

// decode reads and strictly decodes one bounded JSON request body.
func (api *API) decode(writer http.ResponseWriter, request *http.Request, destination any) bool {
	payload, ok := api.readBody(writer, request)
	if !ok {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeAPIError(writer, request, http.StatusBadRequest, "invalid_json", "request body is invalid")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeAPIError(writer, request, http.StatusBadRequest, "invalid_json", "request body must contain one JSON value")
		return false
	}
	return true
}

// readBody enforces JSON media type and maximum request size.
func (api *API) readBody(writer http.ResponseWriter, request *http.Request) ([]byte, bool) {
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		writeAPIError(writer, request, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return nil, false
	}
	payload, err := io.ReadAll(io.LimitReader(request.Body, api.bodyLimit+1))
	if err != nil || int64(len(payload)) > api.bodyLimit {
		writeAPIError(writer, request, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the configured limit")
		return nil, false
	}
	return payload, true
}

// writeMutation writes a consistent idempotent mutation result or safe error.
func (api *API) writeMutation(writer http.ResponseWriter, request *http.Request, err error) {
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

// writeError maps domain and provider failures onto the stable v1 error envelope.
func (api *API) writeError(writer http.ResponseWriter, request *http.Request, err error) {
	status, code, message := http.StatusInternalServerError, "internal_error", "the request could not be completed"
	if errors.Is(err, persistence.ErrNotFound) {
		status, code, message = http.StatusNotFound, "not_found", "the requested resource was not found"
	} else if errors.Is(err, persistence.ErrConflict) {
		status, code, message = http.StatusConflict, "conflict", "the request conflicts with current state"
	} else if errors.Is(err, persistence.ErrUnavailable) {
		status, code, message = http.StatusServiceUnavailable, "storage_unavailable", "durable storage is temporarily unavailable"
	} else {
		var providerFailure *stores.Failure
		if errors.As(err, &providerFailure) {
			switch providerFailure.Kind {
			case stores.FailureInvalidEvidence:
				status, code, message = http.StatusUnprocessableEntity, "invalid_purchase", "purchase evidence could not be verified"
			case stores.FailureUnauthorized:
				status, code, message = http.StatusBadGateway, "provider_unauthorized", "provider credentials were rejected"
			case stores.FailureNotFound:
				status, code, message = http.StatusNotFound, "purchase_not_found", "the provider did not find the purchase"
			case stores.FailureRateLimited, stores.FailureTemporary:
				status, code, message = http.StatusServiceUnavailable, "provider_unavailable", "the provider is temporarily unavailable"
			default:
				status, code, message = http.StatusBadGateway, "provider_error", "the provider request failed"
			}
		} else if isValidationError(err) {
			status, code, message = http.StatusBadRequest, "invalid_request", "request values are invalid"
		}
	}
	writeAPIError(writer, request, status, code, message)
}

// responseFromResult maps internal projections to the stable public response.
func responseFromResult(result verification.Result) verificationResponse {
	return verificationResponse{VerifiedAt: result.VerifiedAt, CustomerID: result.Customer.ID,
		Entitlements: entitlementResponses(result.Entitlements)}
}

// entitlementResponses removes internal source identities from public entitlement snapshots.
func entitlementResponses(values []persistence.CustomerEntitlement) []entitlementResponse {
	result := make([]entitlementResponse, 0, len(values))
	for _, value := range values {
		var startsAt *time.Time
		if !value.Projection.EffectivePeriod.StartsAt.IsZero() {
			start := value.Projection.EffectivePeriod.StartsAt
			startsAt = &start
		}
		result = append(result, entitlementResponse{
			Key: value.Key, Access: value.Projection.Access, Reason: value.Projection.AccessReason,
			EffectiveStartsAt: startsAt, EffectiveEndsAt: value.Projection.EffectivePeriod.EndsAt,
			Version: value.Version,
		})
	}
	return result
}

// requiresReconciliation reports whether signed evidence describes a time-sensitive subscription.
func requiresReconciliation(evidence json.RawMessage) bool {
	var metadata struct {
		ProductKind core.ProductKind `json:"product_kind"`
	}
	return json.Unmarshal(evidence, &metadata) == nil && metadata.ProductKind == core.ProductKindSubscription
}

// nextReconciliationTime returns one deterministic UTC generation boundary for idempotent scheduling.
func nextReconciliationTime(now time.Time) time.Time {
	return now.UTC().Truncate(defaultReconciliationDelay).Add(defaultReconciliationDelay)
}

// deterministicID derives a non-sensitive durable identity from an authenticated fingerprint.
func deterministicID(prefix string, fingerprint [sha256.Size]byte) string {
	digest := sha256.Sum256(append([]byte(prefix+"\x00"), fingerprint[:]...))
	return prefix + "-" + hex.EncodeToString(digest[:16])
}

// isValidationError conservatively identifies errors produced before durable or provider effects.
func isValidationError(err error) bool {
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, operational := range []string{
		"postgresql", "connection", "transaction", "protect ", "open protected", "query queue", "store api key",
	} {
		if strings.Contains(message, operational) {
			return false
		}
	}
	for _, validation := range []string{
		" is required", " must ", "unsupported ", "duplicate ", "mismatch", "cannot ", "invalid ",
	} {
		if strings.Contains(message, validation) {
			return true
		}
	}
	return false
}

// writeAPIError writes one stable JSON error envelope.
func writeAPIError(writer http.ResponseWriter, request *http.Request, status int, code, message string) {
	writeJSON(writer, status, errorEnvelope{Error: apiError{
		Code: code, Message: message, RequestID: writer.Header().Get("X-Request-ID"),
	}})
}

// writeJSON writes one JSON response with a stable content type.
func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// zero overwrites temporary plaintext request secrets after protection.
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
