package postgres_test

import (
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

// TestAdminCustomersAllowedCountUsesCurrentPeriod verifies the customer list
// counts only stored-allowed rows that have started and not ended, the same
// definition as the analytics active count. That is core.AccessAt plus a start
// gate: reads serve a not-yet-started allowed row, but the admin counts do not
// report it as active yet.
func TestAdminCustomersAllowedCountUsesCurrentPeriod(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)

	customers := []struct {
		id            string
		startInterval string
		endInterval   string
		wantAllowed   int64
	}{
		{id: fixture.customerID, startInterval: "-2 hours", endInterval: "-1 hour", wantAllowed: 0},
		{id: "customer-future", startInterval: "1 hour", endInterval: "2 hours", wantAllowed: 0},
		{id: "customer-current", startInterval: "-1 hour", endInterval: "1 hour", wantAllowed: 1},
		{id: "customer-durable", startInterval: "-1 hour", endInterval: "", wantAllowed: 1},
	}
	for _, customer := range customers {
		persistAllowedCustomer(t, database, store, fixture, customer.id)
		setStoredProjectionPeriod(t, database, customer.id, customer.startInterval, customer.endInterval)
	}

	overview, err := store.AdminProjectOverview(database.ctx, core.ProjectID(fixture.projectID), persistence.AdminRevenueQuery{})
	if err != nil {
		t.Fatalf("AdminProjectOverview() error = %v", err)
	}
	byID := make(map[core.CustomerID]persistence.AdminCustomer, len(overview.Customers))
	var allowedSum int64
	for _, customer := range overview.Customers {
		byID[customer.ID] = customer
		allowedSum += customer.AllowedCount
	}
	for _, want := range customers {
		customer, found := byID[core.CustomerID(want.id)]
		if !found {
			t.Fatalf("admin customers = %#v, missing %q", overview.Customers, want.id)
		}
		if customer.EntitlementCount != 1 || customer.AllowedCount != want.wantAllowed {
			t.Fatalf("customer %q counts = (%d, %d), want (1, %d)", want.id, customer.EntitlementCount, customer.AllowedCount, want.wantAllowed)
		}
	}
	if allowedSum != 2 || overview.Analytics.ActiveEntitlementCount != allowedSum {
		t.Fatalf("allowed sum = %d, analytics active = %d, want both 2", allowedSum, overview.Analytics.ActiveEntitlementCount)
	}
}
