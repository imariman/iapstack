package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
)

// recordingAdminStore returns fixed admin records and counts every read so tests can prove a
// forbidden actor never reaches storage.
type recordingAdminStore struct {
	calls         int
	overviewCalls int
	projects      []persistence.AdminProject
	project       persistence.AdminProject
	overview      persistence.AdminProjectOverview
}

// recordingSetupStore returns one fixed project setup and counts reads.
type recordingSetupStore struct {
	calls int
	setup persistence.AdminProjectSetup
}

// recordingReportLister counts financial report reads.
type recordingReportLister struct {
	calls int
}

// TestActorAuthorizesProjectsByRole verifies the shared project and write gates for every role.
func TestActorAuthorizesProjectsByRole(t *testing.T) {
	t.Parallel()

	admin := Actor{Role: persistence.APIKeyRoleAdmin, KeyID: "admin-key"}
	agent := Actor{Role: persistence.APIKeyRoleAgent, KeyID: "agent-key", ProjectID: "project-1"}
	writer := Actor{Role: persistence.APIKeyRoleAgent, KeyID: "agent-key-2", ProjectID: "project-1", AllowWrites: true}
	application := Actor{
		Role: persistence.APIKeyRoleApplication, KeyID: "application-key", ProjectID: "project-1", ApplicationID: "application-1",
	}
	for _, testCase := range []struct {
		name      string
		actor     Actor
		projectID core.ProjectID
		read      bool
		write     bool
	}{
		{name: "admin own", actor: admin, projectID: "project-1", read: true, write: true},
		{name: "admin other", actor: admin, projectID: "project-2", read: true, write: true},
		{name: "read-only agent own", actor: agent, projectID: "project-1", read: true},
		{name: "read-only agent other", actor: agent, projectID: "project-2"},
		{name: "writing agent own", actor: writer, projectID: "project-1", read: true, write: true},
		{name: "writing agent other", actor: writer, projectID: "project-2"},
		{name: "agent without project", actor: Actor{Role: persistence.APIKeyRoleAgent}, projectID: ""},
		{name: "application own", actor: application, projectID: "project-1"},
		{name: "unknown role", actor: Actor{Role: "operator", ProjectID: "project-1"}, projectID: "project-1"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			readErr := testCase.actor.AuthorizeProject(testCase.projectID)
			if (readErr == nil) != testCase.read || (readErr != nil && !errors.Is(readErr, ErrForbidden)) {
				t.Fatalf("AuthorizeProject() error = %v, want allowed %t", readErr, testCase.read)
			}
			writeErr := testCase.actor.AuthorizeWrite(testCase.projectID)
			if (writeErr == nil) != testCase.write || (writeErr != nil && !errors.Is(writeErr, ErrForbidden)) {
				t.Fatalf("AuthorizeWrite() error = %v, want allowed %t", writeErr, testCase.write)
			}
		})
	}
}

// TestForbiddenActorsNeverReachStorage verifies cross-project and data-plane actors fail before any lookup.
func TestForbiddenActorsNeverReachStorage(t *testing.T) {
	t.Parallel()

	admin := &recordingAdminStore{}
	setup := &recordingSetupStore{}
	reports := &recordingReportLister{}
	service := New(Dependencies{Admin: admin, Setup: setup, FinancialReports: reports})
	ctx := context.Background()
	for name, actor := range map[string]Actor{
		"other project agent": {Role: persistence.APIKeyRoleAgent, KeyID: "agent-key", ProjectID: "project-2"},
		"application":         {Role: persistence.APIKeyRoleApplication, KeyID: "app-key", ProjectID: "project-1", ApplicationID: "application-1"},
	} {
		checks := map[string]error{}
		_, checks["overview"] = service.ProjectOverview(ctx, actor, ProjectOverviewInput{ProjectID: "project-1"})
		_, checks["metrics"] = service.SubscriptionMetrics(ctx, actor, SubscriptionMetricsInput{ProjectID: "project-1"})
		_, checks["search"] = service.SearchCustomers(ctx, actor, CustomerSearchInput{ProjectID: "project-1", Query: "user-1"})
		_, checks["customer"] = service.Customer(ctx, actor, "project-1", "customer-1")
		_, checks["reports"] = service.FinancialReports(ctx, actor, "project-1")
		_, checks["setup"] = service.SetupStatus(ctx, actor, "project-1")
		if actor.Role != persistence.APIKeyRoleAgent {
			_, checks["projects"] = service.Projects(ctx, actor)
		}
		for operation, err := range checks {
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s %s error = %v, want ErrForbidden", name, operation, err)
			}
		}
	}
	// A different-project agent may still resolve in its own project only, never in project-1.
	if _, err := service.ResolveCustomer(ctx, Actor{Role: persistence.APIKeyRoleAgent, ProjectID: "project-2"}, "project-1", "user-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ResolveCustomer() cross-project error = %v, want ErrForbidden", err)
	}
	if _, err := service.ResolveCustomer(ctx, Actor{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-2", ApplicationID: "application-2",
	}, "project-1", "user-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ResolveCustomer() cross-project application error = %v, want ErrForbidden", err)
	}
	if admin.calls != 0 || setup.calls != 0 || reports.calls != 0 {
		t.Fatalf("storage calls = admin %d, setup %d, reports %d, want none", admin.calls, setup.calls, reports.calls)
	}
}

// TestProjectsShowsAnAgentOnlyItsProject verifies the project collection is filtered for agents.
func TestProjectsShowsAnAgentOnlyItsProject(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	admin := &recordingAdminStore{
		projects: []persistence.AdminProject{{ID: "project-1", CreatedAt: createdAt}, {ID: "project-2", CreatedAt: createdAt}},
		project:  persistence.AdminProject{ID: "project-3", CreatedAt: createdAt},
	}
	service := New(Dependencies{Admin: admin})
	all, err := service.Projects(context.Background(), Actor{Role: persistence.APIKeyRoleAdmin})
	if err != nil || len(all.Projects) != 2 {
		t.Fatalf("admin Projects() = %#v, %v, want both projects", all, err)
	}
	own, err := service.Projects(context.Background(), Actor{Role: persistence.APIKeyRoleAgent, ProjectID: "project-2"})
	if err != nil || len(own.Projects) != 1 || own.Projects[0].ID != "project-2" {
		t.Fatalf("agent Projects() = %#v, %v, want only project-2", own, err)
	}
	beyondBound, err := service.Projects(context.Background(), Actor{Role: persistence.APIKeyRoleAgent, ProjectID: "project-3"})
	if err != nil || len(beyondBound.Projects) != 1 || beyondBound.Projects[0].ID != "project-3" {
		t.Fatalf("agent Projects() beyond the bound = %#v, %v, want project-3 read directly", beyondBound, err)
	}
	if admin.overviewCalls != 0 {
		t.Fatalf("Projects() ran the project overview %d times, want the narrow project read", admin.overviewCalls)
	}
}

// TestSetupStatusReportsPresenceOnlyAndNotificationURLs verifies setup status composition, that a webhook
// URL is reduced to its origin, that full notification URLs come only from the configured public URL, and
// that an application without a provider adapter is reported as unsupported rather than missing a credential.
func TestSetupStatusReportsPresenceOnlyAndNotificationURLs(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	failedAt := createdAt.Add(time.Hour)
	admin := &recordingAdminStore{}
	setup := &recordingSetupStore{setup: persistence.AdminProjectSetup{
		Applications: []persistence.AdminApplication{
			{ID: "ios-app", Provider: core.ProviderAppleAppStore, Environment: core.EnvironmentProduction, ProviderApplicationID: "com.example.app"},
			{ID: "android-app", Provider: core.ProviderGooglePlay, Environment: core.EnvironmentProduction, ProviderApplicationID: "com.example.app"},
			{ID: "amazon-app", Provider: core.ProviderAmazonAppstore, Environment: core.EnvironmentProduction, ProviderApplicationID: "com.example.app"},
		},
		Queues: []persistence.AdminQueue{{Name: persistence.QueueOutbox, Failed: 2}, {Name: persistence.QueueInbox}},
		RecentWebhookEvents: []persistence.AdminWebhookEvent{
			{ID: "event-1", ApplicationID: "ios-app", EventType: "entitlement.changed", Status: "failed", ErrorCode: "http_500", OccurredAt: createdAt, FailedAt: &failedAt},
			{ID: "event-2", ApplicationID: "ios-app", EventType: "entitlement.changed", Status: "delivered", OccurredAt: createdAt},
		},
		ApplicationSetups: []persistence.AdminApplicationSetup{
			{
				ApplicationID: "ios-app",
				Credentials:   []persistence.AdminCredentialStatus{{Kind: "apple_app_store_server_api", Revision: 3, UpdatedAt: createdAt}},
				Webhook: &persistence.AdminWebhookSetup{
					URL:      "https://user:pass@hooks.example.com:8443/hooks/catch/123/sentinel-path-token/?token=sentinel-query-token#frag",
					Revision: 2, UpdatedAt: createdAt,
				},
			},
			{ApplicationID: "android-app", Credentials: []persistence.AdminCredentialStatus{{Kind: "unrelated", Revision: 1, UpdatedAt: createdAt}}},
			{ApplicationID: "amazon-app", Credentials: []persistence.AdminCredentialStatus{{Kind: "amazon_shared_secret", Revision: 1, UpdatedAt: createdAt}}},
		},
	}}
	actor := Actor{Role: persistence.APIKeyRoleAgent, ProjectID: "project 1"}

	withoutURL, err := New(Dependencies{Admin: admin, Setup: setup}).SetupStatus(context.Background(), actor, "project 1")
	if err != nil {
		t.Fatalf("SetupStatus() error = %v", err)
	}
	if withoutURL.PublicURLConfigured || withoutURL.Applications[0].StoreNotifications == nil ||
		withoutURL.Applications[0].StoreNotifications.URL != "" ||
		withoutURL.Applications[0].StoreNotifications.Path != "/v1/providers/apple/projects/project%201/applications/ios-app/notifications" {
		t.Fatalf("setup without public URL = %#v", withoutURL.Applications[0].StoreNotifications)
	}
	if len(withoutURL.Issues) == 0 || withoutURL.Issues[0].Code != SetupIssuePublicURLNotConfigured {
		t.Fatalf("issues without public URL = %#v, want public_url_not_configured first", withoutURL.Issues)
	}
	if admin.calls != 0 {
		t.Fatalf("SetupStatus() made %d admin store reads, want only the narrow setup read", admin.calls)
	}

	status, err := New(Dependencies{Admin: admin, Setup: setup, PublicURL: "https://iap.example.com/base/"}).
		SetupStatus(context.Background(), actor, "project 1")
	if err != nil {
		t.Fatalf("SetupStatus() error = %v", err)
	}
	ios, android, amazon := status.Applications[0], status.Applications[1], status.Applications[2]
	if !ios.Credentials.Configured || ios.Credentials.RequiredKind != "apple_app_store_server_api" || len(ios.Credentials.Stored) != 1 {
		t.Fatalf("iOS credentials = %#v", ios.Credentials)
	}
	if !ios.Webhook.Configured || ios.Webhook.Origin != "https://hooks.example.com:8443" || ios.Webhook.Revision != 2 {
		t.Fatalf("iOS webhook = %#v, want only the origin of the stored URL", ios.Webhook)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal setup status: %v", err)
	}
	for _, secret := range []string{"sentinel-path-token", "sentinel-query-token", "user:pass", "/hooks/catch"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("setup status contains %q from the webhook URL: %s", secret, encoded)
		}
	}
	if ios.StoreNotifications.URL != "https://iap.example.com/base/v1/providers/apple/projects/project%201/applications/ios-app/notifications" {
		t.Fatalf("iOS notification URL = %q", ios.StoreNotifications.URL)
	}
	if android.Credentials.Configured || android.Webhook.Configured || android.StoreNotifications == nil ||
		!strings.Contains(android.StoreNotifications.Path, "/v1/providers/google-play/") {
		t.Fatalf("Android setup = %#v, want missing credential and webhook", android)
	}
	if amazon.Credentials.RequiredKind != "" || amazon.Credentials.Configured || amazon.StoreNotifications != nil ||
		len(amazon.Credentials.Stored) != 1 || strings.Contains(string(encoded), `"store_notifications":{"destination":""`) {
		t.Fatalf("Amazon setup = %#v, want no required kind and no store notifications", amazon)
	}
	for _, issue := range status.Issues {
		if issue.ApplicationID == "amazon-app" && issue.Code == SetupIssueCredentialMissing {
			t.Fatalf("issues = %#v, want no credential_missing for a provider without an adapter", status.Issues)
		}
	}
	codes := map[string]int{}
	for _, issue := range status.Issues {
		codes[issue.Code]++
	}
	if codes[SetupIssueCredentialMissing] != 1 || codes[SetupIssueWebhookNotConfigured] != 2 ||
		codes[SetupIssueProviderNotSupported] != 1 ||
		codes[SetupIssueQueueFailures] != 1 || codes[SetupIssueWebhookDeliveryFailed] != 1 ||
		codes[SetupIssuePublicURLNotConfigured] != 0 {
		t.Fatalf("issues = %#v", status.Issues)
	}
	if len(status.RecentWebhookFailures) != 1 || status.RecentWebhookFailures[0].ErrorCode != "http_500" {
		t.Fatalf("webhook failures = %#v", status.RecentWebhookFailures)
	}
}

// TestClassifyUsesStableCodes verifies the shared classification used by HTTP and MCP.
func TestClassifyUsesStableCodes(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		err    error
		status int
		code   string
	}{
		{err: ErrForbidden, status: http.StatusForbidden, code: "forbidden"},
		{err: persistence.ErrNotFound, status: http.StatusNotFound, code: "not_found"},
		{err: validation.Wrap(errors.New("opaque")), status: http.StatusBadRequest, code: "invalid_request"},
		{err: persistence.ErrUnavailable, status: http.StatusServiceUnavailable, code: "storage_unavailable"},
		{err: errors.New("secret detail"), status: http.StatusInternalServerError, code: "internal_error"},
	} {
		problem := Classify(testCase.err)
		if problem.Status != testCase.status || problem.Code != testCase.code || strings.Contains(problem.Message, "secret") {
			t.Fatalf("Classify(%v) = %#v, want %d %s", testCase.err, problem, testCase.status, testCase.code)
		}
	}
}

// AdminProjects returns the fixed projects.
func (store *recordingAdminStore) AdminProjects(context.Context) ([]persistence.AdminProject, error) {
	store.calls++
	return store.projects, nil
}

// AdminProject returns the fixed project.
func (store *recordingAdminStore) AdminProject(context.Context, core.ProjectID) (persistence.AdminProject, error) {
	store.calls++
	return store.project, nil
}

// AdminProjectOverview returns the fixed overview and counts the heavy read separately.
func (store *recordingAdminStore) AdminProjectOverview(
	context.Context,
	core.ProjectID,
	persistence.AdminRevenueQuery,
) (persistence.AdminProjectOverview, error) {
	store.calls++
	store.overviewCalls++
	return store.overview, nil
}

// AdminSearchCustomers returns an empty page.
func (store *recordingAdminStore) AdminSearchCustomers(
	context.Context,
	persistence.AdminCustomerSearch,
) (persistence.AdminCustomerSearchPage, error) {
	store.calls++
	return persistence.AdminCustomerSearchPage{}, nil
}

// AdminCustomerDetail returns an empty detail.
func (store *recordingAdminStore) AdminCustomerDetail(
	context.Context,
	core.ProjectID,
	core.CustomerID,
) (persistence.AdminCustomerDetail, error) {
	store.calls++
	return persistence.AdminCustomerDetail{}, nil
}

// AdminSubscriptionMetrics returns empty metrics.
func (store *recordingAdminStore) AdminSubscriptionMetrics(
	context.Context,
	core.ProjectID,
	persistence.SubscriptionMetricsQuery,
) (persistence.SubscriptionMetrics, error) {
	store.calls++
	return persistence.SubscriptionMetrics{}, nil
}

// AdminProjectSetup returns the fixed project setup.
func (store *recordingSetupStore) AdminProjectSetup(
	context.Context,
	core.ProjectID,
) (persistence.AdminProjectSetup, error) {
	store.calls++
	return store.setup, nil
}

// List counts one report read.
func (lister *recordingReportLister) List(context.Context, core.ProjectID) ([]persistence.FinancialReport, error) {
	lister.calls++
	return nil, nil
}
