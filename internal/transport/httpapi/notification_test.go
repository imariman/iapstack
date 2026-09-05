package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
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

// failingReader returns one deterministic transport-level request body failure.
type failingReader struct{}

// Read reports that the request body could not be consumed.
func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("request body read failed")
}

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

// TestReadBodyDistinguishesReadFailuresFromOversizeBodies verifies transport failures are not reported as 413.
func TestReadBodyDistinguishesReadFailuresFromOversizeBodies(t *testing.T) {
	t.Parallel()

	api := &API{bodyLimit: 1024}
	request := httptest.NewRequest(http.MethodPost, "/notifications", failingReader{})
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	if _, ok := api.readBody(recorder, request); ok {
		t.Fatal("readBody() ok = true, want false")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("read failure status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if !strings.Contains(recorder.Body.String(), `"code":"invalid_request"`) ||
		strings.Contains(recorder.Body.String(), "request_too_large") {
		t.Fatalf("read failure response = %s", recorder.Body.String())
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
