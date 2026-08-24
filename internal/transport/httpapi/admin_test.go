package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// fakeAdminQueryStore provides deterministic secret-free dashboard records.
type fakeAdminQueryStore struct{}

// authOperationsStore satisfies authentication construction for bootstrap-only tests.
type authOperationsStore struct{}

// AdminProjects returns one deterministic dashboard project.
func (fakeAdminQueryStore) AdminProjects(context.Context) ([]persistence.AdminProject, error) {
	return []persistence.AdminProject{{
		ID: "project-1", ApplicationCount: 1, CustomerCount: 2, ProductCount: 3,
		CreatedAt: time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC),
	}}, nil
}

// AdminProjectOverview returns one empty bounded overview for the requested project.
func (fakeAdminQueryStore) AdminProjectOverview(
	_ context.Context,
	projectID core.ProjectID,
) (persistence.AdminProjectOverview, error) {
	return persistence.AdminProjectOverview{
		Project:      persistence.AdminProject{ID: projectID, CreatedAt: time.Date(2026, time.August, 25, 9, 0, 0, 0, time.UTC)},
		Applications: []persistence.AdminApplication{}, Products: []persistence.AdminProduct{},
		Customers: []persistence.AdminCustomer{}, RecentTransactions: []persistence.AdminTransaction{},
		Queues: []persistence.AdminQueue{}, RecentWebhookEvents: []persistence.AdminWebhookEvent{},
	}, nil
}

// Operate rejects durable work because bootstrap authentication does not require it.
func (authOperationsStore) Operate(context.Context, persistence.OperationsFunc) error {
	return persistence.ErrUnavailable
}

// TestAdminProjectsRequiresAdminAndReturnsStableJSON verifies authorization and the safe collection contract.
func TestAdminProjectsRequiresAdminAndReturnsStableJSON(t *testing.T) {
	t.Parallel()

	authentication, err := auth.NewService(authOperationsStore{}, "bootstrap-secret")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	api := &API{admin: fakeAdminQueryStore{}, authentication: authentication}

	unauthorized := httptest.NewRecorder()
	api.adminProjects(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/admin/projects", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/admin/projects", nil)
	request.Header.Set("Authorization", "Bearer bootstrap-secret")
	recorder := httptest.NewRecorder()
	api.adminProjects(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response adminProjectsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Projects) != 1 || response.Projects[0].ID != "project-1" {
		t.Fatalf("projects response = %#v, want deterministic project", response)
	}
	for _, forbidden := range []string{"ciphertext", "fingerprint", "secret", "payload", "token"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), forbidden) {
			t.Fatalf("projects response contains forbidden field %q: %s", forbidden, recorder.Body.String())
		}
	}
}

// TestAdminOverviewSerializesEmptyCollections verifies dashboard clients never receive null collections.
func TestAdminOverviewSerializesEmptyCollections(t *testing.T) {
	t.Parallel()

	overview := adminOverviewFromRecord(persistence.AdminProjectOverview{
		Project:      persistence.AdminProject{ID: "project-1", CreatedAt: time.Now().UTC()},
		Applications: []persistence.AdminApplication{}, Products: []persistence.AdminProduct{},
		Customers: []persistence.AdminCustomer{}, RecentTransactions: []persistence.AdminTransaction{},
		Queues: []persistence.AdminQueue{}, RecentWebhookEvents: []persistence.AdminWebhookEvent{},
	})
	payload, err := json.Marshal(overview)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, field := range []string{
		`"applications":[]`, `"products":[]`, `"customers":[]`,
		`"recent_transactions":[]`, `"queues":[]`, `"recent_webhook_events":[]`,
	} {
		if !strings.Contains(string(payload), field) {
			t.Fatalf("overview JSON = %s, want %s", payload, field)
		}
	}
}
