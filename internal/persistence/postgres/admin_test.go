package postgres_test

import (
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

// TestAdminCustomersAllowedCountUsesCurrentPeriod verifies the customer list
// counts only stored-allowed rows inside their effective period, matching the
// analytics active count and the AccessAt decisions for the same rows.
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
		if current := storedRowCurrentlyAllowed(t, database, want.id); current != (want.wantAllowed == 1) {
			t.Fatalf("customer %q AccessAt current = %v, want %v", want.id, current, want.wantAllowed == 1)
		}
	}
	if allowedSum != 2 || overview.Analytics.ActiveEntitlementCount != allowedSum {
		t.Fatalf("allowed sum = %d, analytics active = %d, want both 2", allowedSum, overview.Analytics.ActiveEntitlementCount)
	}
}

// storedRowCurrentlyAllowed applies the shared read decision to one stored projection.
func storedRowCurrentlyAllowed(t *testing.T, database *testDatabase, customerID string) bool {
	t.Helper()
	var access string
	var startsAt *time.Time
	var endsAt *time.Time
	if err := database.conn.QueryRow(database.ctx, `
		SELECT access_status, effective_starts_at, effective_ends_at
		FROM customer_entitlements
		WHERE customer_id = $1
	`, customerID).Scan(&access, &startsAt, &endsAt); err != nil {
		t.Fatalf("read stored projection for %q: %v", customerID, err)
	}
	now := time.Now().UTC()
	status, _ := core.AccessAt(core.AccessStatus(access), core.AccessReasonPurchaseValid, endsAt, now)
	return status == core.AccessAllowed && startsAt != nil && !startsAt.After(now)
}
