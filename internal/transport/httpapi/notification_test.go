package httpapi

import (
	"context"
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
