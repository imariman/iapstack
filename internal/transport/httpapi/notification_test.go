package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/huawei"
)

// notificationScopeStore records the application scope requested by a provider notification handler.
type notificationScopeStore struct {
	projectID     core.ProjectID
	applicationID core.ApplicationID
}

// notificationScopeTransaction implements only application lookup for route-scope tests.
type notificationScopeTransaction struct {
	persistence.OperationsTransaction
	store *notificationScopeStore
}

// huaweiAdmissionStore records unsigned ORDER authentication and inbox writes.
type huaweiAdmissionStore struct {
	application     core.Application
	purchase        persistence.PurchaseReferenceContext
	purchaseError   error
	lookup          persistence.ProviderReferenceLookup
	inbox           *persistence.InboxMessage
	operations      int
	lookupOperation int
	inboxOperation  int
}

// huaweiAdmissionTransaction implements the Huawei notification admission test ports.
type huaweiAdmissionTransaction struct {
	persistence.OperationsTransaction
	store     *huaweiAdmissionStore
	operation int
}

// unavailableNotificationCredentialSource fails if an unsigned ORDER unexpectedly requests Huawei credentials.
type unavailableNotificationCredentialSource struct{}

// TestHuaweiNotificationUsesPathProjectScope verifies AppGallery-compatible scope without custom headers.
func TestHuaweiNotificationUsesPathProjectScope(t *testing.T) {
	t.Parallel()

	store := &notificationScopeStore{}
	api := &API{operations: store, bodyLimit: 1024}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/providers/huawei/projects/project-1/applications/application-1/notifications",
		strings.NewReader(`{"version":"v2"}`),
	)
	request.SetPathValue("project_id", "project-1")
	request.SetPathValue("application_id", "application-1")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-IAPStack-Project-ID", "wrong-header-project")
	recorder := httptest.NewRecorder()
	api.huaweiNotification(recorder, request)
	if store.projectID != "project-1" || store.applicationID != "application-1" {
		t.Fatalf("application lookup scope = (%q, %q)", store.projectID, store.applicationID)
	}
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("notification status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

// TestHuaweiNotificationAuthenticatesUnsignedOrderBeforeInbox verifies guessed tokens cannot create durable work.
func TestHuaweiNotificationAuthenticatesUnsignedOrderBeforeInbox(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 5, 15, 0, 0, 0, time.UTC)
	application := core.Application{
		ID: "application-1", ProjectID: "project-1",
		Store: core.StoreApplication{
			Provider: core.ProviderHuaweiAppGallery, Environment: core.EnvironmentSandbox, ID: "provider-app-1",
		},
	}
	payload, err := json.Marshal(map[string]any{
		"version": "v2", "eventType": "ORDER", "notifyTime": now.UnixMilli(),
		"applicationId": "provider-app-1",
		"orderNotification": map[string]any{
			"version": "v2", "notificationType": 2,
			"purchaseToken": "random-purchase-token", "productId": "premium_lifetime",
		},
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, testCase := range []struct {
		name          string
		purchase      persistence.PurchaseReferenceContext
		purchaseError error
		wantStatus    int
		wantInbox     bool
	}{
		{name: "unknown token", purchaseError: persistence.ErrNotFound, wantStatus: http.StatusUnprocessableEntity},
		{
			name: "wrong product",
			purchase: persistence.PurchaseReferenceContext{
				ProviderProductID: "another_product", ProductKind: core.ProductKindNonConsumable,
			},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{name: "storage unavailable", purchaseError: persistence.ErrUnavailable, wantStatus: http.StatusServiceUnavailable},
		{
			name: "known matching purchase",
			purchase: persistence.PurchaseReferenceContext{
				ProviderProductID: "premium_lifetime", ProductKind: core.ProductKindNonConsumable,
			},
			wantStatus: http.StatusOK, wantInbox: true,
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			store := &huaweiAdmissionStore{
				application: application, purchase: testCase.purchase, purchaseError: testCase.purchaseError,
			}
			adapter, newErr := huawei.New(unavailableNotificationCredentialSource{}, time.Second, false)
			if newErr != nil {
				t.Fatalf("New() Huawei adapter error = %v", newErr)
			}
			keyring := newCustomerSessionTestKeyring(t)
			api := &API{
				operations: store, huawei: adapter, protection: keyring, bodyLimit: 1024,
				clock: func() time.Time { return now },
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/providers/huawei/projects/project-1/applications/application-1/notifications",
				strings.NewReader(string(payload)),
			)
			request.SetPathValue("project_id", "project-1")
			request.SetPathValue("application_id", "application-1")
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()

			api.huaweiNotification(recorder, request)

			if recorder.Code != testCase.wantStatus {
				t.Fatalf("notification status = %d, want %d: %s", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if (store.inbox != nil) != testCase.wantInbox {
				t.Fatalf("inbox saved = %t, want %t", store.inbox != nil, testCase.wantInbox)
			}
			if store.lookup.ProjectID != application.ProjectID || store.lookup.ApplicationID != application.ID ||
				store.lookup.Role != core.ReferenceQuery || store.lookup.Kind != "purchase_token" ||
				store.lookup.Fingerprint == ([32]byte{}) {
				t.Fatalf("purchase lookup = %#v", store.lookup)
			}
			token := []byte("random-purchase-token")
			expected, protectErr := protection.Protect(t.Context(), keyring, protection.Scope{
				ProjectID: application.ProjectID, ApplicationID: application.ID,
				Purpose: "provider_reference:query:purchase_token",
			}, token)
			zero(token)
			if protectErr != nil {
				t.Fatalf("Protect() expected fingerprint error = %v", protectErr)
			}
			defer zero(expected.Ciphertext)
			if store.lookup.Fingerprint != expected.Fingerprint {
				t.Fatal("purchase lookup used a fingerprint outside verification scope")
			}
			if testCase.wantInbox && (store.lookupOperation == 0 || store.lookupOperation != store.inboxOperation) {
				t.Fatalf("lookup/inbox operations = (%d, %d), want one atomic operation", store.lookupOperation, store.inboxOperation)
			}
		})
	}
}

// TestGooglePlayNotificationUsesPathProjectScope verifies Pub/Sub-compatible scope without custom headers.
func TestGooglePlayNotificationUsesPathProjectScope(t *testing.T) {
	t.Parallel()

	store := &notificationScopeStore{}
	api := &API{operations: store, bodyLimit: 1024}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/providers/google-play/projects/project-1/applications/application-1/notifications",
		strings.NewReader(`{"message":{}}`),
	)
	request.SetPathValue("project_id", "project-1")
	request.SetPathValue("application_id", "application-1")
	request.Header.Set("Authorization", "Bearer signed-pubsub-token")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-IAPStack-Project-ID", "wrong-header-project")
	recorder := httptest.NewRecorder()
	api.googlePlayNotification(recorder, request)
	if store.projectID != "project-1" || store.applicationID != "application-1" {
		t.Fatalf("application lookup scope = (%q, %q)", store.projectID, store.applicationID)
	}
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("notification status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

// TestAppleNotificationUsesPathProjectScope verifies App Store Connect-compatible scope without headers.
func TestAppleNotificationUsesPathProjectScope(t *testing.T) {
	t.Parallel()

	store := &notificationScopeStore{}
	api := &API{operations: store, bodyLimit: 1024}
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/providers/apple/projects/project-1/applications/application-1/notifications",
		strings.NewReader(`{"signedPayload":"header.payload.signature"}`),
	)
	request.SetPathValue("project_id", "project-1")
	request.SetPathValue("application_id", "application-1")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-IAPStack-Project-ID", "wrong-header-project")
	recorder := httptest.NewRecorder()
	api.appleNotification(recorder, request)
	if store.projectID != "project-1" || store.applicationID != "application-1" {
		t.Fatalf("application lookup scope = (%q, %q)", store.projectID, store.applicationID)
	}
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("notification status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

// TestGooglePlayNotificationRejectsMalformedBearerBeforeReadingBody verifies Pub/Sub auth is fail fast.
func TestGooglePlayNotificationRejectsMalformedBearerBeforeReadingBody(t *testing.T) {
	t.Parallel()

	for _, authorization := range []string{
		"",
		"Basic signed-pubsub-token",
		"bearer signed-pubsub-token",
		"Bearer ",
		"Bearer signed token",
		"Bearer signed-pubsub-token\nsecond-token",
	} {
		authorization := authorization
		t.Run(authorization, func(t *testing.T) {
			t.Parallel()

			store := &notificationScopeStore{}
			api := &API{operations: store, bodyLimit: 1}
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/providers/google-play/projects/project-1/applications/application-1/notifications",
				strings.NewReader("oversized body that must not be read"),
			)
			request.Header.Set("Authorization", authorization)
			recorder := httptest.NewRecorder()

			api.googlePlayNotification(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("notification status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
			if store.projectID != "" || store.applicationID != "" {
				t.Fatalf("application lookup occurred before authentication: %#v", store)
			}
		})
	}
}

// TestProviderNotificationsEnforceJSONBodyContract verifies media type and size limits consistently.
func TestProviderNotificationsEnforceJSONBodyContract(t *testing.T) {
	t.Parallel()

	handlers := []struct {
		name    string
		handler func(*API, http.ResponseWriter, *http.Request)
	}{
		{name: "Huawei", handler: func(api *API, writer http.ResponseWriter, request *http.Request) {
			api.huaweiNotification(writer, request)
		}},
		{name: "Google Play", handler: func(api *API, writer http.ResponseWriter, request *http.Request) {
			api.googlePlayNotification(writer, request)
		}},
		{name: "Apple", handler: func(api *API, writer http.ResponseWriter, request *http.Request) {
			api.appleNotification(writer, request)
		}},
	}
	for _, handler := range handlers {
		handler := handler
		t.Run(handler.name, func(t *testing.T) {
			t.Parallel()

			for _, testCase := range []struct {
				name        string
				contentType string
				body        string
				wantStatus  int
			}{
				{name: "missing media type", body: `{}`, wantStatus: http.StatusUnsupportedMediaType},
				{name: "wrong media type", contentType: "text/plain", body: `{}`, wantStatus: http.StatusUnsupportedMediaType},
				{name: "oversized body", contentType: "application/json; charset=utf-8", body: `{"too":"large"}`, wantStatus: http.StatusRequestEntityTooLarge},
			} {
				testCase := testCase
				t.Run(testCase.name, func(t *testing.T) {
					t.Parallel()

					store := &notificationScopeStore{}
					api := &API{operations: store, bodyLimit: 4}
					request := httptest.NewRequest(http.MethodPost, "/notifications", strings.NewReader(testCase.body))
					request.Header.Set("Content-Type", testCase.contentType)
					request.Header.Set("Authorization", "Bearer signed-pubsub-token")
					recorder := httptest.NewRecorder()

					handler.handler(api, recorder, request)

					if recorder.Code != testCase.wantStatus {
						t.Fatalf("notification status = %d, want %d", recorder.Code, testCase.wantStatus)
					}
					if store.projectID != "" || store.applicationID != "" {
						t.Fatalf("application lookup occurred before body validation: %#v", store)
					}
				})
			}
		})
	}
}

// Operate executes one scope-capturing application lookup callback.
func (store *notificationScopeStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	return operation(&notificationScopeTransaction{store: store})
}

// Application records the requested scope and stops the handler before provider validation.
func (transaction *notificationScopeTransaction) Application(
	_ context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) (core.Application, error) {
	transaction.store.projectID = projectID
	transaction.store.applicationID = applicationID
	return core.Application{}, persistence.ErrNotFound
}

// Operate executes one recorded Huawei admission transaction.
func (store *huaweiAdmissionStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	store.operations++
	return operation(&huaweiAdmissionTransaction{store: store, operation: store.operations})
}

// Application returns the Huawei application configured for admission tests.
func (transaction *huaweiAdmissionTransaction) Application(
	_ context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) (core.Application, error) {
	if projectID != transaction.store.application.ProjectID || applicationID != transaction.store.application.ID {
		return core.Application{}, persistence.ErrNotFound
	}
	return transaction.store.application, nil
}

// PurchaseContextByReference records and resolves the protected ORDER token.
func (transaction *huaweiAdmissionTransaction) PurchaseContextByReference(
	_ context.Context,
	lookup persistence.ProviderReferenceLookup,
) (persistence.PurchaseReferenceContext, error) {
	transaction.store.lookup = lookup
	transaction.store.lookupOperation = transaction.operation
	return transaction.store.purchase, transaction.store.purchaseError
}

// SaveInboxMessage records the protected Huawei message accepted by the handler.
func (transaction *huaweiAdmissionTransaction) SaveInboxMessage(
	_ context.Context,
	message persistence.InboxMessage,
) (string, error) {
	stored := message
	transaction.store.inbox = &stored
	transaction.store.inboxOperation = transaction.operation
	return message.ID, nil
}

// Credential rejects unexpected provider-credential access for unsigned ORDER admission.
func (unavailableNotificationCredentialSource) Credential(
	_ context.Context,
	_ core.Application,
	_ stores.CredentialKind,
) (stores.Credential, error) {
	return stores.Credential{}, stores.ErrCredentialNotFound
}
