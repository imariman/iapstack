package controlplane

import (
	"context"
	"errors"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
)

// ProjectList is the bounded project collection an actor may see.
type ProjectList struct {
	Projects []Project `json:"projects"`
}

// Project summarizes one project for navigation and overview counts.
type Project struct {
	ID               core.ProjectID `json:"id"`
	ApplicationCount int64          `json:"application_count"`
	CustomerCount    int64          `json:"customer_count"`
	ProductCount     int64          `json:"product_count"`
	CreatedAt        time.Time      `json:"created_at"`
}

// ProjectOverviewInput selects one project and an optional financial period, as YYYY-MM-DD dates.
type ProjectOverviewInput struct {
	ProjectID            core.ProjectID
	FinancialPeriodStart string
	FinancialPeriodEnd   string
}

// ProjectOverview contains secret-free operational datasets for one project.
type ProjectOverview struct {
	Project             Project        `json:"project"`
	Analytics           Analytics      `json:"analytics"`
	Applications        []Application  `json:"applications"`
	Products            []Product      `json:"products"`
	Customers           []Customer     `json:"customers"`
	RecentTransactions  []Transaction  `json:"recent_transactions"`
	Queues              []Queue        `json:"queues"`
	RecentWebhookEvents []WebhookEvent `json:"recent_webhook_events"`
}

// Analytics contains bounded verification activity and revenue availability.
type Analytics struct {
	WindowDays             int             `json:"window_days"`
	GeneratedAt            time.Time       `json:"generated_at"`
	VerifiedCount          int64           `json:"verified_count"`
	AllowedCount           int64           `json:"allowed_count"`
	DeniedCount            int64           `json:"denied_count"`
	UnresolvedCount        int64           `json:"unresolved_count"`
	ReversedCount          int64           `json:"reversed_count"`
	ActiveEntitlementCount int64           `json:"active_entitlement_count"`
	Revenue                Revenue         `json:"revenue"`
	SandboxValue           SandboxValue    `json:"sandbox_value"`
	DailyActivity          []DailyActivity `json:"daily_activity"`
}

// Revenue reports authoritative recognized revenue or why it is unavailable.
type Revenue struct {
	Status                     persistence.AdminRevenueStatus `json:"status"`
	RecognizedMinorUnits       *int64                         `json:"recognized_minor_units"`
	Currency                   *string                        `json:"currency"`
	Amounts                    []RecognizedAmount             `json:"amounts"`
	UncoveredProviders         []core.Provider                `json:"uncovered_providers"`
	ZeroFilledApplicationIDs   []core.ApplicationID           `json:"zero_filled_application_ids"`
	ProductionApplicationCount int64                          `json:"production_application_count"`
	TestApplicationCount       int64                          `json:"test_application_count"`
}

// RecognizedAmount is one store-reported provider, period, and currency total.
type RecognizedAmount struct {
	Provider        core.Provider `json:"provider"`
	ReportKind      string        `json:"report_kind"`
	PeriodStart     string        `json:"period_start"`
	PeriodEnd       string        `json:"period_end"`
	Currency        string        `json:"currency"`
	AmountDecimal   string        `json:"amount_decimal"`
	AmountScale     int32         `json:"amount_scale"`
	MinorUnits      *int64        `json:"minor_units"`
	CommissionState string        `json:"commission_state"`
	TaxState        string        `json:"tax_state"`
}

// Money reports one non-converted provider-signed amount.
type Money struct {
	Milliunits int64  `json:"milliunits"`
	Currency   string `json:"currency"`
}

// SandboxValue reports non-accounting test transaction value coverage.
type SandboxValue struct {
	Amounts           []Money `json:"amounts"`
	TransactionCount  int64   `json:"transaction_count"`
	MissingPriceCount int64   `json:"missing_price_count"`
}

// DailyActivity contains one UTC day of normalized outcomes.
type DailyActivity struct {
	Date       string `json:"date"`
	Verified   int64  `json:"verified"`
	Allowed    int64  `json:"allowed"`
	Denied     int64  `json:"denied"`
	Unresolved int64  `json:"unresolved"`
	Reversed   int64  `json:"reversed"`
}

// Application reports provider scope and safe setup coverage.
type Application struct {
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

// Product reports grants and provider mapping coverage for one product.
type Product struct {
	ID                core.ProductID   `json:"id"`
	Kind              core.ProductKind `json:"kind"`
	EntitlementKeys   []string         `json:"entitlement_keys"`
	StoreMappingCount int64            `json:"store_mapping_count"`
	CreatedAt         time.Time        `json:"created_at"`
}

// Customer reports current access counts without provider references.
type Customer struct {
	ID                   core.CustomerID `json:"id"`
	ExternalID           string          `json:"external_id"`
	MergedIntoCustomerID *string         `json:"merged_into_customer_id,omitempty"`
	AliasCount           int64           `json:"alias_count"`
	EntitlementCount     int64           `json:"entitlement_count"`
	AllowedCount         int64           `json:"allowed_count"`
	LastObservedAt       *time.Time      `json:"last_observed_at,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
}

// Transaction reports one normalized, secret-free purchase observation.
type Transaction struct {
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

// Queue reports aggregate durable processing outcomes.
type Queue struct {
	Name      persistence.QueueName `json:"name"`
	Pending   int64                 `json:"pending"`
	Completed int64                 `json:"completed"`
	Failed    int64                 `json:"failed"`
	Skipped   int64                 `json:"skipped"`
}

// WebhookEvent reports delivery metadata without event payloads.
type WebhookEvent struct {
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

var (
	// errMissingDependency reports an operation whose persistence port was not configured.
	errMissingDependency = errors.New("control-plane dependency is not configured")
)

// Projects returns the projects the actor may see. An agent sees only its own project.
func (service *Service) Projects(ctx context.Context, actor Actor) (ProjectList, error) {
	if actor.Role != persistence.APIKeyRoleAdmin && actor.Role != persistence.APIKeyRoleAgent {
		return ProjectList{}, ErrForbidden
	}
	if service.dependencies.Admin == nil {
		return ProjectList{}, errMissingDependency
	}
	projects, err := service.dependencies.Admin.AdminProjects(ctx)
	if err != nil {
		return ProjectList{}, err
	}
	list := ProjectList{Projects: make([]Project, 0, len(projects))}
	for _, project := range projects {
		if actor.AuthorizeProject(project.ID) != nil {
			continue
		}
		list.Projects = append(list.Projects, ProjectView(project))
	}
	if actor.Role == persistence.APIKeyRoleAgent && len(list.Projects) == 0 {
		// The project collection is bounded, so an agent's project can fall outside it. Read the
		// project directly instead of reporting that the agent has none.
		overview, err := service.dependencies.Admin.AdminProjectOverview(ctx, actor.ProjectID, persistence.AdminRevenueQuery{})
		if errors.Is(err, persistence.ErrNotFound) {
			return list, nil
		}
		if err != nil {
			return ProjectList{}, err
		}
		list.Projects = append(list.Projects, ProjectView(overview.Project))
	}
	return list, nil
}

// ProjectOverview returns one project-scoped, secret-free operational snapshot.
func (service *Service) ProjectOverview(
	ctx context.Context,
	actor Actor,
	input ProjectOverviewInput,
) (ProjectOverview, error) {
	if err := actor.AuthorizeProject(input.ProjectID); err != nil {
		return ProjectOverview{}, err
	}
	if err := input.ProjectID.Validate(); err != nil {
		return ProjectOverview{}, validation.Wrap(err)
	}
	query, err := RevenueQuery(input.FinancialPeriodStart, input.FinancialPeriodEnd)
	if err != nil {
		return ProjectOverview{}, validation.Wrap(err)
	}
	if service.dependencies.Admin == nil {
		return ProjectOverview{}, errMissingDependency
	}
	overview, err := service.dependencies.Admin.AdminProjectOverview(ctx, input.ProjectID, query)
	if err != nil {
		return ProjectOverview{}, err
	}
	return ProjectOverviewView(overview), nil
}

// RevenueQuery parses the optional financial period filter. Both dates or neither must be set.
func RevenueQuery(startRaw, endRaw string) (persistence.AdminRevenueQuery, error) {
	if startRaw == "" && endRaw == "" {
		return persistence.AdminRevenueQuery{}, nil
	}
	if startRaw == "" || endRaw == "" {
		return persistence.AdminRevenueQuery{}, errors.New("financial period requires both start and end")
	}
	start, err := time.Parse(time.DateOnly, startRaw)
	if err != nil {
		return persistence.AdminRevenueQuery{}, errors.New("financial period start is invalid")
	}
	end, err := time.Parse(time.DateOnly, endRaw)
	if err != nil {
		return persistence.AdminRevenueQuery{}, errors.New("financial period end is invalid")
	}
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	if end.Before(start) {
		return persistence.AdminRevenueQuery{}, errors.New("financial period end is before the start")
	}
	return persistence.AdminRevenueQuery{FinancialPeriodStart: &start, FinancialPeriodEnd: &end}, nil
}

// ProjectView maps the persistence projection onto the stable JSON view.
func ProjectView(project persistence.AdminProject) Project {
	return Project{
		ID: project.ID, ApplicationCount: project.ApplicationCount,
		CustomerCount: project.CustomerCount, ProductCount: project.ProductCount,
		CreatedAt: project.CreatedAt,
	}
}

// ProjectOverviewView removes persistence naming from the public overview view.
func ProjectOverviewView(overview persistence.AdminProjectOverview) ProjectOverview {
	response := ProjectOverview{
		Project:             ProjectView(overview.Project),
		Analytics:           analyticsView(overview.Analytics),
		Applications:        make([]Application, 0, len(overview.Applications)),
		Products:            make([]Product, 0, len(overview.Products)),
		Customers:           make([]Customer, 0, len(overview.Customers)),
		RecentTransactions:  make([]Transaction, 0, len(overview.RecentTransactions)),
		Queues:              make([]Queue, 0, len(overview.Queues)),
		RecentWebhookEvents: make([]WebhookEvent, 0, len(overview.RecentWebhookEvents)),
	}
	for _, application := range overview.Applications {
		response.Applications = append(response.Applications, Application{
			ID: application.ID, Provider: application.Provider, Environment: application.Environment,
			ProviderApplicationID: application.ProviderApplicationID,
			CredentialConfigured:  application.CredentialConfigured,
			CredentialRevision:    application.CredentialRevision,
			WebhookConfigured:     application.WebhookConfigured,
			WebhookRevision:       application.WebhookRevision, CreatedAt: application.CreatedAt,
		})
	}
	for _, product := range overview.Products {
		response.Products = append(response.Products, Product{
			ID: product.ID, Kind: product.Kind, EntitlementKeys: product.EntitlementKeys,
			StoreMappingCount: product.StoreMappingCount, CreatedAt: product.CreatedAt,
		})
	}
	for _, customer := range overview.Customers {
		response.Customers = append(response.Customers, CustomerView(customer))
	}
	for _, transaction := range overview.RecentTransactions {
		response.RecentTransactions = append(response.RecentTransactions, Transaction{
			ID: transaction.ID, ApplicationID: transaction.ApplicationID,
			CustomerID: transaction.CustomerID, ProductID: transaction.ProductID,
			ProviderProductID: transaction.ProviderProductID,
			LifecycleState:    transaction.LifecycleState, Access: transaction.Access,
			AccessReason: transaction.AccessReason, ObservedAt: transaction.ObservedAt,
			EffectiveEndsAt: transaction.EffectiveEndsAt,
		})
	}
	for _, queue := range overview.Queues {
		response.Queues = append(response.Queues, queueView(queue))
	}
	for _, event := range overview.RecentWebhookEvents {
		response.RecentWebhookEvents = append(response.RecentWebhookEvents, webhookEventView(event))
	}
	return response
}

// CustomerView maps one customer summary onto the overview and search view.
func CustomerView(customer persistence.AdminCustomer) Customer {
	return Customer{
		ID: customer.ID, ExternalID: customer.ExternalID,
		MergedIntoCustomerID: customer.MergedIntoCustomerID, AliasCount: customer.AliasCount,
		EntitlementCount: customer.EntitlementCount, AllowedCount: customer.AllowedCount,
		LastObservedAt: customer.LastObservedAt, CreatedAt: customer.CreatedAt,
	}
}

// queueView maps one aggregate queue outcome.
func queueView(queue persistence.AdminQueue) Queue {
	return Queue{
		Name: queue.Name, Pending: queue.Pending,
		Completed: queue.Completed, Failed: queue.Failed, Skipped: queue.Skipped,
	}
}

// webhookEventView maps one delivery result without its payload.
func webhookEventView(event persistence.AdminWebhookEvent) WebhookEvent {
	return WebhookEvent{
		ID: event.ID, ApplicationID: event.ApplicationID, EventType: event.EventType,
		AggregateID: event.AggregateID, Status: event.Status, ErrorCode: event.ErrorCode,
		OccurredAt: event.OccurredAt, DeliveredAt: event.DeliveredAt, FailedAt: event.FailedAt,
	}
}

// analyticsView maps the bounded analytics projection onto the public view.
func analyticsView(analytics persistence.AdminAnalytics) Analytics {
	response := Analytics{
		WindowDays: analytics.WindowDays, GeneratedAt: analytics.GeneratedAt,
		VerifiedCount: analytics.VerifiedCount, AllowedCount: analytics.AllowedCount,
		DeniedCount: analytics.DeniedCount, UnresolvedCount: analytics.UnresolvedCount,
		ReversedCount:          analytics.ReversedCount,
		ActiveEntitlementCount: analytics.ActiveEntitlementCount,
		Revenue:                RevenueView(analytics.Revenue),
		SandboxValue: SandboxValue{
			Amounts:           make([]Money, 0, len(analytics.SandboxValue.Amounts)),
			TransactionCount:  analytics.SandboxValue.TransactionCount,
			MissingPriceCount: analytics.SandboxValue.MissingPriceCount,
		},
		DailyActivity: make([]DailyActivity, 0, len(analytics.DailyActivity)),
	}
	for _, amount := range analytics.SandboxValue.Amounts {
		response.SandboxValue.Amounts = append(response.SandboxValue.Amounts, Money{
			Milliunits: amount.Milliunits,
			Currency:   amount.Currency,
		})
	}
	for _, day := range analytics.DailyActivity {
		response.DailyActivity = append(response.DailyActivity, DailyActivity{
			Date: day.Date, Verified: day.Verified, Allowed: day.Allowed,
			Denied: day.Denied, Unresolved: day.Unresolved, Reversed: day.Reversed,
		})
	}
	return response
}

// RevenueView maps store-reported proceeds without collapsing currencies.
func RevenueView(revenue persistence.AdminRevenue) Revenue {
	response := Revenue{
		Status:                     revenue.Status,
		RecognizedMinorUnits:       revenue.RecognizedMinorUnits,
		Currency:                   revenue.Currency,
		Amounts:                    make([]RecognizedAmount, 0, len(revenue.Amounts)),
		UncoveredProviders:         append([]core.Provider{}, revenue.UncoveredProviders...),
		ZeroFilledApplicationIDs:   append([]core.ApplicationID{}, revenue.ZeroFilledApplicationIDs...),
		ProductionApplicationCount: revenue.ProductionApplicationCount,
		TestApplicationCount:       revenue.TestApplicationCount,
	}
	if response.UncoveredProviders == nil {
		response.UncoveredProviders = []core.Provider{}
	}
	if response.ZeroFilledApplicationIDs == nil {
		response.ZeroFilledApplicationIDs = []core.ApplicationID{}
	}
	for _, amount := range revenue.Amounts {
		response.Amounts = append(response.Amounts, RecognizedAmount{
			Provider: amount.Provider, ReportKind: amount.ReportKind,
			PeriodStart: amount.PeriodStart.UTC().Format(time.DateOnly),
			PeriodEnd:   amount.PeriodEnd.UTC().Format(time.DateOnly),
			Currency:    amount.Currency, AmountDecimal: amount.AmountDecimal,
			AmountScale: amount.AmountScale, MinorUnits: amount.MinorUnits,
			CommissionState: amount.CommissionState, TaxState: amount.TaxState,
		})
	}
	return response
}
