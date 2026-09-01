package httpapi

import (
	"net/http"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// adminProjectsResponse is the bounded administrator project collection.
type adminProjectsResponse struct {
	Projects []adminProjectResponse `json:"projects"`
}

// adminProjectResponse summarizes one project for navigation and overview counts.
type adminProjectResponse struct {
	ID               core.ProjectID `json:"id"`
	ApplicationCount int64          `json:"application_count"`
	CustomerCount    int64          `json:"customer_count"`
	ProductCount     int64          `json:"product_count"`
	CreatedAt        time.Time      `json:"created_at"`
}

// adminOverviewResponse contains secret-free operational datasets for one project.
type adminOverviewResponse struct {
	Project             adminProjectResponse        `json:"project"`
	Analytics           adminAnalyticsResponse      `json:"analytics"`
	Applications        []adminApplicationResponse  `json:"applications"`
	Products            []adminProductResponse      `json:"products"`
	Customers           []adminCustomerResponse     `json:"customers"`
	RecentTransactions  []adminTransactionResponse  `json:"recent_transactions"`
	Queues              []adminQueueResponse        `json:"queues"`
	RecentWebhookEvents []adminWebhookEventResponse `json:"recent_webhook_events"`
}

// adminAnalyticsResponse contains bounded verification activity and revenue availability.
type adminAnalyticsResponse struct {
	WindowDays             int                          `json:"window_days"`
	GeneratedAt            time.Time                    `json:"generated_at"`
	VerifiedCount          int64                        `json:"verified_count"`
	AllowedCount           int64                        `json:"allowed_count"`
	DeniedCount            int64                        `json:"denied_count"`
	UnresolvedCount        int64                        `json:"unresolved_count"`
	ReversedCount          int64                        `json:"reversed_count"`
	ActiveEntitlementCount int64                        `json:"active_entitlement_count"`
	Revenue                adminRevenueResponse         `json:"revenue"`
	DailyActivity          []adminDailyActivityResponse `json:"daily_activity"`
}

// adminRevenueResponse reports authoritative recognized revenue or why it is unavailable.
type adminRevenueResponse struct {
	Status                     persistence.AdminRevenueStatus `json:"status"`
	RecognizedMinorUnits       *int64                         `json:"recognized_minor_units"`
	Currency                   *string                        `json:"currency"`
	ProductionApplicationCount int64                          `json:"production_application_count"`
	TestApplicationCount       int64                          `json:"test_application_count"`
}

// adminDailyActivityResponse contains one UTC day of normalized outcomes.
type adminDailyActivityResponse struct {
	Date       string `json:"date"`
	Verified   int64  `json:"verified"`
	Allowed    int64  `json:"allowed"`
	Denied     int64  `json:"denied"`
	Unresolved int64  `json:"unresolved"`
	Reversed   int64  `json:"reversed"`
}

// adminApplicationResponse reports provider scope and safe setup coverage.
type adminApplicationResponse struct {
	ID                    core.ApplicationID         `json:"id"`
	Provider              core.Provider              `json:"provider"`
	Environment           core.Environment           `json:"environment"`
	ProviderApplicationID core.ProviderApplicationID `json:"provider_application_id"`
	CredentialConfigured  bool                       `json:"credential_configured"`
	CredentialRevision    int64                      `json:"credential_revision"`
	WebhookConfigured     bool                       `json:"webhook_configured"`
	WebhookRevision       int64                      `json:"webhook_revision"`
	CreatedAt             time.Time                  `json:"created_at"`
}

// adminProductResponse reports grants and provider mapping coverage for one product.
type adminProductResponse struct {
	ID                core.ProductID   `json:"id"`
	Kind              core.ProductKind `json:"kind"`
	EntitlementKeys   []string         `json:"entitlement_keys"`
	StoreMappingCount int64            `json:"store_mapping_count"`
	CreatedAt         time.Time        `json:"created_at"`
}

// adminCustomerResponse reports current access counts without provider references.
type adminCustomerResponse struct {
	ID               core.CustomerID `json:"id"`
	ExternalID       string          `json:"external_id"`
	EntitlementCount int64           `json:"entitlement_count"`
	AllowedCount     int64           `json:"allowed_count"`
	LastObservedAt   *time.Time      `json:"last_observed_at,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

// adminTransactionResponse reports one normalized, secret-free purchase observation.
type adminTransactionResponse struct {
	ID                core.ObservationID     `json:"id"`
	ApplicationID     core.ApplicationID     `json:"application_id"`
	CustomerID        core.CustomerID        `json:"customer_id"`
	ProductID         core.ProductID         `json:"product_id"`
	ProviderProductID core.ProviderProductID `json:"provider_product_id"`
	LifecycleState    core.LifecycleState    `json:"lifecycle_state"`
	Access            core.AccessStatus      `json:"access"`
	AccessReason      core.AccessReason      `json:"access_reason"`
	ObservedAt        time.Time              `json:"observed_at"`
	EffectiveEndsAt   *time.Time             `json:"effective_ends_at,omitempty"`
}

// adminQueueResponse reports aggregate durable processing outcomes.
type adminQueueResponse struct {
	Name      persistence.QueueName `json:"name"`
	Pending   int64                 `json:"pending"`
	Completed int64                 `json:"completed"`
	Failed    int64                 `json:"failed"`
}

// adminWebhookEventResponse reports delivery metadata without event payloads.
type adminWebhookEventResponse struct {
	ID            string             `json:"id"`
	ApplicationID core.ApplicationID `json:"application_id"`
	EventType     string             `json:"event_type"`
	AggregateID   string             `json:"aggregate_id"`
	Status        string             `json:"status"`
	ErrorCode     string             `json:"error_code,omitempty"`
	OccurredAt    time.Time          `json:"occurred_at"`
	DeliveredAt   *time.Time         `json:"delivered_at,omitempty"`
	FailedAt      *time.Time         `json:"failed_at,omitempty"`
}

// adminProjects returns the bounded project navigation collection.
func (api *API) adminProjects(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projects, err := api.admin.AdminProjects(request.Context())
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	responses := make([]adminProjectResponse, 0, len(projects))
	for _, project := range projects {
		responses = append(responses, adminProjectFromRecord(project))
	}
	writeJSON(writer, http.StatusOK, adminProjectsResponse{Projects: responses})
}

// adminProjectOverview returns one project-scoped, secret-free operational snapshot.
func (api *API) adminProjectOverview(writer http.ResponseWriter, request *http.Request) {
	if _, ok := api.requireAdmin(writer, request); !ok {
		return
	}
	projectID := core.ProjectID(request.PathValue("project_id"))
	if !api.requireValidInput(writer, request, projectID.Validate()) {
		return
	}
	overview, err := api.admin.AdminProjectOverview(
		request.Context(),
		projectID,
	)
	if err != nil {
		api.writeError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, adminOverviewFromRecord(overview))
}

// adminProjectFromRecord maps the persistence projection onto the stable JSON contract.
func adminProjectFromRecord(project persistence.AdminProject) adminProjectResponse {
	return adminProjectResponse{
		ID: project.ID, ApplicationCount: project.ApplicationCount,
		CustomerCount: project.CustomerCount, ProductCount: project.ProductCount,
		CreatedAt: project.CreatedAt,
	}
}

// adminOverviewFromRecord removes persistence naming from the public dashboard contract.
func adminOverviewFromRecord(overview persistence.AdminProjectOverview) adminOverviewResponse {
	response := adminOverviewResponse{
		Project:             adminProjectFromRecord(overview.Project),
		Analytics:           adminAnalyticsFromRecord(overview.Analytics),
		Applications:        make([]adminApplicationResponse, 0, len(overview.Applications)),
		Products:            make([]adminProductResponse, 0, len(overview.Products)),
		Customers:           make([]adminCustomerResponse, 0, len(overview.Customers)),
		RecentTransactions:  make([]adminTransactionResponse, 0, len(overview.RecentTransactions)),
		Queues:              make([]adminQueueResponse, 0, len(overview.Queues)),
		RecentWebhookEvents: make([]adminWebhookEventResponse, 0, len(overview.RecentWebhookEvents)),
	}
	for _, application := range overview.Applications {
		response.Applications = append(response.Applications, adminApplicationResponse{
			ID: application.ID, Provider: application.Provider, Environment: application.Environment,
			ProviderApplicationID: application.ProviderApplicationID,
			CredentialConfigured:  application.CredentialConfigured,
			CredentialRevision:    application.CredentialRevision,
			WebhookConfigured:     application.WebhookConfigured,
			WebhookRevision:       application.WebhookRevision, CreatedAt: application.CreatedAt,
		})
	}
	for _, product := range overview.Products {
		response.Products = append(response.Products, adminProductResponse{
			ID: product.ID, Kind: product.Kind, EntitlementKeys: product.EntitlementKeys,
			StoreMappingCount: product.StoreMappingCount, CreatedAt: product.CreatedAt,
		})
	}
	for _, customer := range overview.Customers {
		response.Customers = append(response.Customers, adminCustomerResponse{
			ID: customer.ID, ExternalID: customer.ExternalID,
			EntitlementCount: customer.EntitlementCount, AllowedCount: customer.AllowedCount,
			LastObservedAt: customer.LastObservedAt, CreatedAt: customer.CreatedAt,
		})
	}
	for _, transaction := range overview.RecentTransactions {
		response.RecentTransactions = append(response.RecentTransactions, adminTransactionResponse{
			ID: transaction.ID, ApplicationID: transaction.ApplicationID,
			CustomerID: transaction.CustomerID, ProductID: transaction.ProductID,
			ProviderProductID: transaction.ProviderProductID,
			LifecycleState:    transaction.LifecycleState, Access: transaction.Access,
			AccessReason: transaction.AccessReason, ObservedAt: transaction.ObservedAt,
			EffectiveEndsAt: transaction.EffectiveEndsAt,
		})
	}
	for _, queue := range overview.Queues {
		response.Queues = append(response.Queues, adminQueueResponse{
			Name: queue.Name, Pending: queue.Pending,
			Completed: queue.Completed, Failed: queue.Failed,
		})
	}
	for _, event := range overview.RecentWebhookEvents {
		response.RecentWebhookEvents = append(response.RecentWebhookEvents, adminWebhookEventResponse{
			ID: event.ID, ApplicationID: event.ApplicationID, EventType: event.EventType,
			AggregateID: event.AggregateID, Status: event.Status, ErrorCode: event.ErrorCode,
			OccurredAt: event.OccurredAt, DeliveredAt: event.DeliveredAt, FailedAt: event.FailedAt,
		})
	}
	return response
}

// adminAnalyticsFromRecord maps the bounded analytics projection onto the public contract.
func adminAnalyticsFromRecord(analytics persistence.AdminAnalytics) adminAnalyticsResponse {
	response := adminAnalyticsResponse{
		WindowDays: analytics.WindowDays, GeneratedAt: analytics.GeneratedAt,
		VerifiedCount: analytics.VerifiedCount, AllowedCount: analytics.AllowedCount,
		DeniedCount: analytics.DeniedCount, UnresolvedCount: analytics.UnresolvedCount,
		ReversedCount:          analytics.ReversedCount,
		ActiveEntitlementCount: analytics.ActiveEntitlementCount,
		Revenue: adminRevenueResponse{
			Status:                     analytics.Revenue.Status,
			RecognizedMinorUnits:       analytics.Revenue.RecognizedMinorUnits,
			Currency:                   analytics.Revenue.Currency,
			ProductionApplicationCount: analytics.Revenue.ProductionApplicationCount,
			TestApplicationCount:       analytics.Revenue.TestApplicationCount,
		},
		DailyActivity: make([]adminDailyActivityResponse, 0, len(analytics.DailyActivity)),
	}
	for _, day := range analytics.DailyActivity {
		response.DailyActivity = append(response.DailyActivity, adminDailyActivityResponse{
			Date: day.Date, Verified: day.Verified, Allowed: day.Allowed,
			Denied: day.Denied, Unresolved: day.Unresolved, Reversed: day.Reversed,
		})
	}
	return response
}
