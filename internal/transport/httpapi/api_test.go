package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/webhooks"
)

const (
	// webhookAPITestBodyLimit bounds the focused handler test request.
	webhookAPITestBodyLimit int64 = 1 << 10
)

// webhookAPIStore exposes one application through the transactional read boundary.
type webhookAPIStore struct {
	persistence.Store
	application core.Application
}

// webhookAPITransaction exposes one authoritative application to the handler.
type webhookAPITransaction struct {
	persistence.Transaction
	application core.Application
}

// webhookAPIProtection records unexpected secret protection attempts.
type webhookAPIProtection struct {
	protectCalls int
}

// TestPutWebhookRejectsShortSigningSecret verifies the public endpoint returns a secret-free validation response.
func TestPutWebhookRejectsShortSigningSecret(t *testing.T) {
	t.Parallel()

	authentication, err := auth.NewService(authOperationsStore{}, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() authentication error = %v", err)
	}
	protector := &webhookAPIProtection{}
	webhookService, err := webhooks.NewService(authOperationsStore{}, protector, time.Second, false)
	if err != nil {
		t.Fatalf("NewService() webhook error = %v", err)
	}
	store := &webhookAPIStore{application: webhookAPIApplication()}
	api := &API{
		store: store, authentication: authentication, webhooks: webhookService,
		bodyLimit: webhookAPITestBodyLimit,
	}
	secret := strings.Repeat("sensitive-short-secret-", 1)
	request := httptest.NewRequest(
		http.MethodPut,
		"/v1/admin/projects/project-1/applications/application-1/webhook",
		strings.NewReader(`{"url":"https://example.com/hooks","signing_secret":"`+secret+`","expected_revision":0}`),
	)
	request.SetPathValue("project_id", "project-1")
	request.SetPathValue("application_id", "application-1")
	request.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	api.putWebhook(recorder, request)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("putWebhook() response = %d %s, want invalid-request HTTP 400", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), secret) {
		t.Fatalf("putWebhook() response exposed signing secret: %s", recorder.Body.String())
	}
	if protector.protectCalls != 0 {
		t.Fatalf("putWebhook() protection calls = %d, want 0", protector.protectCalls)
	}
}

// Transact executes one application read against the fake transaction.
func (store *webhookAPIStore) Transact(_ context.Context, operation persistence.TransactionFunc) error {
	return operation(&webhookAPITransaction{application: store.application})
}

// Application returns the configured application in its exact scope.
func (transaction *webhookAPITransaction) Application(
	_ context.Context,
	projectID core.ProjectID,
	applicationID core.ApplicationID,
) (core.Application, error) {
	if transaction.application.ProjectID != projectID || transaction.application.ID != applicationID {
		return core.Application{}, persistence.ErrNotFound
	}
	return transaction.application, nil
}

// Protect records an unexpected call because short secrets must fail before protection.
func (service *webhookAPIProtection) Protect(
	_ context.Context,
	_ protection.Request,
) (protection.Value, error) {
	service.protectCalls++
	return protection.Value{}, errors.New("unexpected protection call")
}

// Open rejects unused webhook reads in the focused configuration test.
func (*webhookAPIProtection) Open(context.Context, protection.OpenRequest) ([]byte, error) {
	return nil, errors.New("unexpected open call")
}

// webhookAPIApplication returns one valid project-scoped application.
func webhookAPIApplication() core.Application {
	return core.Application{
		ID: "application-1", ProjectID: "project-1",
		Store: core.StoreApplication{
			Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest,
			ID: "com.example.iapstack",
		},
	}
}
