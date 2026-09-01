package persistence

import (
	"context"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// AdminRevenueSandboxOnly means every configured application is non-production.
	AdminRevenueSandboxOnly AdminRevenueStatus = "sandbox_only"
	// AdminRevenueStoreReportsRequired means production applications need official financial reports.
	AdminRevenueStoreReportsRequired AdminRevenueStatus = "store_reports_required"
	// AdminRevenueNoApplications means the project has no application scope yet.
	AdminRevenueNoApplications AdminRevenueStatus = "no_applications"
)

// AdminQueryStore exposes read-only, secret-free operational projections for administrators.
type AdminQueryStore interface {
	// AdminProjects returns bounded project summaries ordered by stable identity.
	AdminProjects(context.Context) ([]AdminProject, error)
	// AdminProjectOverview returns one bounded operational snapshot in project scope.
	AdminProjectOverview(context.Context, core.ProjectID) (AdminProjectOverview, error)
}

// AdminProject summarizes one project without exposing protected configuration.
type AdminProject struct {
	ID               core.ProjectID
	ApplicationCount int64
	CustomerCount    int64
	ProductCount     int64
	CreatedAt        time.Time
}

// AdminProjectOverview contains the bounded datasets rendered by the operations dashboard.
type AdminProjectOverview struct {
	Project             AdminProject
	Analytics           AdminAnalytics
	Applications        []AdminApplication
	Products            []AdminProduct
	Customers           []AdminCustomer
	RecentTransactions  []AdminTransaction
	Queues              []AdminQueue
	RecentWebhookEvents []AdminWebhookEvent
}

// AdminRevenueStatus explains whether authoritative real-revenue data is available.
type AdminRevenueStatus string

// AdminMoney reports one currency amount without applying an exchange-rate conversion.
type AdminMoney struct {
	Milliunits int64
	Currency   string
}

// AdminSandboxValue reports provider-signed test transaction value separately from accounting revenue.
type AdminSandboxValue struct {
	Amounts           []AdminMoney
	TransactionCount  int64
	MissingPriceCount int64
}

// AdminAnalytics contains bounded operational activity and an explicit revenue truth state.
type AdminAnalytics struct {
	WindowDays             int
	GeneratedAt            time.Time
	VerifiedCount          int64
	AllowedCount           int64
	DeniedCount            int64
	UnresolvedCount        int64
	ReversedCount          int64
	ActiveEntitlementCount int64
	Revenue                AdminRevenue
	SandboxValue           AdminSandboxValue
	DailyActivity          []AdminDailyActivity
}

// AdminRevenue reports only authoritative real revenue, never client-supplied price estimates.
type AdminRevenue struct {
	Status                     AdminRevenueStatus
	RecognizedMinorUnits       *int64
	Currency                   *string
	ProductionApplicationCount int64
	TestApplicationCount       int64
}

// AdminDailyActivity reports one UTC day of normalized verification outcomes.
type AdminDailyActivity struct {
	Date       string
	Verified   int64
	Allowed    int64
	Denied     int64
	Unresolved int64
	Reversed   int64
}

// AdminApplication reports provider scope and configuration coverage for one application.
type AdminApplication struct {
	ID                    core.ApplicationID
	Provider              core.Provider
	Environment           core.Environment
	ProviderApplicationID core.ProviderApplicationID
	CredentialConfigured  bool
	CredentialRevision    int64
	WebhookConfigured     bool
	WebhookRevision       int64
	CreatedAt             time.Time
}

// AdminProduct reports one internal product, grants, and provider mapping coverage.
type AdminProduct struct {
	ID                core.ProductID
	Kind              core.ProductKind
	EntitlementKeys   []string
	StoreMappingCount int64
	CreatedAt         time.Time
}

// AdminCustomer reports current access counts and the last authoritative observation time.
type AdminCustomer struct {
	ID               core.CustomerID
	ExternalID       string
	EntitlementCount int64
	AllowedCount     int64
	LastObservedAt   *time.Time
	CreatedAt        time.Time
}

// AdminTransaction reports one normalized provider observation without protected references.
type AdminTransaction struct {
	ID                core.ObservationID
	ApplicationID     core.ApplicationID
	CustomerID        core.CustomerID
	ProductID         core.ProductID
	ProviderProductID core.ProviderProductID
	LifecycleState    core.LifecycleState
	Access            core.AccessStatus
	AccessReason      core.AccessReason
	ObservedAt        time.Time
	EffectiveEndsAt   *time.Time
}

// AdminQueue reports bounded-cardinality durable queue outcomes.
type AdminQueue struct {
	Name      QueueName
	Pending   int64
	Completed int64
	Failed    int64
}

// AdminWebhookEvent reports one outbound delivery result without returning its payload.
type AdminWebhookEvent struct {
	ID            string
	ApplicationID core.ApplicationID
	EventType     string
	AggregateID   string
	Status        string
	ErrorCode     string
	OccurredAt    time.Time
	DeliveredAt   *time.Time
	FailedAt      *time.Time
}
