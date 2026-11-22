package controlplane

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/stores/apple"
	"github.com/imariman/iapstack/internal/stores/googleplay"
	"github.com/imariman/iapstack/internal/stores/huawei"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// SetupIssueCredentialMissing means the provider credential the adapter needs is not stored.
	SetupIssueCredentialMissing = "credential_missing"
	// SetupIssueWebhookNotConfigured means entitlement changes for the application are not delivered.
	SetupIssueWebhookNotConfigured = "webhook_not_configured"
	// SetupIssuePublicURLNotConfigured means only notification paths can be reported, not full URLs.
	SetupIssuePublicURLNotConfigured = "public_url_not_configured"
	// SetupIssueQueueFailures means a durable queue has failed messages.
	SetupIssueQueueFailures = "queue_failures"
	// SetupIssueWebhookDeliveryFailed means at least one recent webhook delivery failed.
	SetupIssueWebhookDeliveryFailed = "webhook_delivery_failed"
	// webhookStatusFailed is the admin delivery status of a terminal webhook failure.
	webhookStatusFailed = "failed"
)

// SetupStatus reports what an operator still has to configure for one project and what recently failed.
// It never contains credential payloads, ciphertext, fingerprints, or webhook secrets.
type SetupStatus struct {
	ProjectID   core.ProjectID `json:"project_id"`
	GeneratedAt time.Time      `json:"generated_at"`
	// PublicURLConfigured is true when the server knows its external URL and can print full notification URLs.
	PublicURLConfigured   bool               `json:"public_url_configured"`
	Applications          []ApplicationSetup `json:"applications"`
	Queues                []Queue            `json:"queues"`
	RecentWebhookFailures []WebhookFailure   `json:"recent_webhook_failures"`
	Issues                []SetupIssue       `json:"issues"`
}

// ApplicationSetup is the configuration state of one application.
type ApplicationSetup struct {
	ID                    core.ApplicationID         `json:"id"`
	Provider              core.Provider              `json:"provider"`
	Environment           core.Environment           `json:"environment"`
	ProviderApplicationID core.ProviderApplicationID `json:"provider_application_id"`
	Credentials           CredentialSetup            `json:"credentials"`
	Webhook               WebhookSetup               `json:"webhook"`
	StoreNotifications    NotificationSetup          `json:"store_notifications"`
}

// CredentialSetup reports which provider credentials are stored, by kind and revision only.
type CredentialSetup struct {
	// RequiredKind is the credential kind this server's adapter for the provider reads.
	RequiredKind string `json:"required_kind"`
	// Configured is true when a credential of RequiredKind is stored.
	Configured bool               `json:"configured"`
	Stored     []StoredCredential `json:"stored"`
}

// StoredCredential is one stored credential kind and revision, never its payload.
type StoredCredential struct {
	Kind      string    `json:"kind"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WebhookSetup reports the webhook endpoint without its signing secret. The URL never includes a
// query string, fragment, or user information.
type WebhookSetup struct {
	Configured bool       `json:"configured"`
	URL        string     `json:"url,omitempty"`
	Revision   int64      `json:"revision,omitempty"`
	UpdatedAt  *time.Time `json:"updated_at,omitempty"`
}

// NotificationSetup tells the operator where store server notifications must point.
type NotificationSetup struct {
	// Destination names the store console setting that takes the URL.
	Destination string `json:"destination"`
	Path        string `json:"path"`
	// URL is the full notification URL when the server's public URL is configured.
	URL string `json:"url,omitempty"`
}

// WebhookFailure is one recent terminal webhook delivery failure without its payload.
type WebhookFailure struct {
	EventID       string             `json:"event_id"`
	ApplicationID core.ApplicationID `json:"application_id"`
	EventType     string             `json:"event_type"`
	ErrorCode     string             `json:"error_code,omitempty"`
	OccurredAt    time.Time          `json:"occurred_at"`
	FailedAt      *time.Time         `json:"failed_at,omitempty"`
}

// SetupIssue is one stable, machine-readable setup gap or failure signal.
type SetupIssue struct {
	Code          string                `json:"code"`
	ApplicationID core.ApplicationID    `json:"application_id,omitempty"`
	Queue         persistence.QueueName `json:"queue,omitempty"`
}

// SetupStatus composes application, credential, webhook, and failure reads into one setup report.
func (service *Service) SetupStatus(
	ctx context.Context,
	actor Actor,
	projectID core.ProjectID,
) (SetupStatus, error) {
	if err := actor.AuthorizeProject(projectID); err != nil {
		return SetupStatus{}, err
	}
	if err := projectID.Validate(); err != nil {
		return SetupStatus{}, validation.Wrap(err)
	}
	if service.dependencies.Admin == nil || service.dependencies.Setup == nil {
		return SetupStatus{}, errMissingDependency
	}
	overview, err := service.dependencies.Admin.AdminProjectOverview(ctx, projectID, persistence.AdminRevenueQuery{})
	if err != nil {
		return SetupStatus{}, err
	}
	setups, err := service.dependencies.Setup.AdminApplicationSetup(ctx, projectID)
	if err != nil {
		return SetupStatus{}, err
	}
	stored := make(map[core.ApplicationID]persistence.AdminApplicationSetup, len(setups))
	for _, setup := range setups {
		stored[setup.ApplicationID] = setup
	}
	publicURL := strings.TrimRight(service.dependencies.PublicURL, "/")
	status := SetupStatus{
		ProjectID: projectID, GeneratedAt: service.now(), PublicURLConfigured: publicURL != "",
		Applications:          make([]ApplicationSetup, 0, len(overview.Applications)),
		Queues:                make([]Queue, 0, len(overview.Queues)),
		RecentWebhookFailures: make([]WebhookFailure, 0),
		Issues:                make([]SetupIssue, 0),
	}
	if publicURL == "" && len(overview.Applications) > 0 {
		status.Issues = append(status.Issues, SetupIssue{Code: SetupIssuePublicURLNotConfigured})
	}
	for _, application := range overview.Applications {
		setup := applicationSetupView(projectID, application, stored[application.ID], publicURL)
		if !setup.Credentials.Configured {
			status.Issues = append(status.Issues, SetupIssue{Code: SetupIssueCredentialMissing, ApplicationID: application.ID})
		}
		if !setup.Webhook.Configured {
			status.Issues = append(status.Issues, SetupIssue{Code: SetupIssueWebhookNotConfigured, ApplicationID: application.ID})
		}
		status.Applications = append(status.Applications, setup)
	}
	for _, queue := range overview.Queues {
		status.Queues = append(status.Queues, queueView(queue))
		if queue.Failed > 0 {
			status.Issues = append(status.Issues, SetupIssue{Code: SetupIssueQueueFailures, Queue: queue.Name})
		}
	}
	failedApplications := make(map[core.ApplicationID]bool)
	for _, event := range overview.RecentWebhookEvents {
		if event.Status != webhookStatusFailed {
			continue
		}
		status.RecentWebhookFailures = append(status.RecentWebhookFailures, WebhookFailure{
			EventID: event.ID, ApplicationID: event.ApplicationID, EventType: event.EventType,
			ErrorCode: event.ErrorCode, OccurredAt: event.OccurredAt, FailedAt: event.FailedAt,
		})
		if !failedApplications[event.ApplicationID] {
			failedApplications[event.ApplicationID] = true
			status.Issues = append(status.Issues, SetupIssue{
				Code: SetupIssueWebhookDeliveryFailed, ApplicationID: event.ApplicationID,
			})
		}
	}
	return status, nil
}

// applicationSetupView combines one application's provider scope with its stored setup rows.
func applicationSetupView(
	projectID core.ProjectID,
	application persistence.AdminApplication,
	stored persistence.AdminApplicationSetup,
	publicURL string,
) ApplicationSetup {
	requiredKind := requiredCredentialKind(application.Provider)
	setup := ApplicationSetup{
		ID: application.ID, Provider: application.Provider, Environment: application.Environment,
		ProviderApplicationID: application.ProviderApplicationID,
		Credentials: CredentialSetup{
			RequiredKind: requiredKind, Stored: make([]StoredCredential, 0, len(stored.Credentials)),
		},
		StoreNotifications: notificationSetup(projectID, application, publicURL),
	}
	for _, credential := range stored.Credentials {
		setup.Credentials.Stored = append(setup.Credentials.Stored, StoredCredential{
			Kind: credential.Kind, Revision: credential.Revision, UpdatedAt: credential.UpdatedAt.UTC(),
		})
		if requiredKind != "" && credential.Kind == requiredKind {
			setup.Credentials.Configured = true
		}
	}
	if stored.Webhook != nil {
		updatedAt := stored.Webhook.UpdatedAt.UTC()
		setup.Webhook = WebhookSetup{
			Configured: true, URL: redactedURL(stored.Webhook.URL),
			Revision: stored.Webhook.Revision, UpdatedAt: &updatedAt,
		}
	}
	return setup
}

// notificationSetup returns where a store must send server notifications for one application.
func notificationSetup(
	projectID core.ProjectID,
	application persistence.AdminApplication,
	publicURL string,
) NotificationSetup {
	var segment, destination string
	switch application.Provider {
	case core.ProviderAppleAppStore:
		segment = "apple"
		destination = "App Store Connect: App Store Server Notifications URL"
	case core.ProviderGooglePlay:
		segment = "google-play"
		destination = "Google Cloud Pub/Sub push subscription endpoint for Play real-time developer notifications"
	case core.ProviderHuaweiAppGallery:
		segment = "huawei"
		destination = "AppGallery Connect: server notification URL"
	default:
		return NotificationSetup{}
	}
	path := "/v1/providers/" + segment + "/projects/" + url.PathEscape(string(projectID)) +
		"/applications/" + url.PathEscape(string(application.ID)) + "/notifications"
	setup := NotificationSetup{Destination: destination, Path: path}
	if publicURL != "" {
		setup.URL = publicURL + path
	}
	return setup
}

// requiredCredentialKind returns the credential kind this server's adapter reads for a provider.
func requiredCredentialKind(provider core.Provider) string {
	switch provider {
	case core.ProviderAppleAppStore:
		return string(apple.CredentialKind)
	case core.ProviderGooglePlay:
		return string(googleplay.CredentialKind)
	case core.ProviderHuaweiAppGallery:
		return string(huawei.CredentialKind)
	default:
		return ""
	}
}

// redactedURL drops user information, the query string, and the fragment, which may carry tokens.
func redactedURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}
