package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

// TestManualGrantReplayPeriodMergeAndEndedRevocation verifies replay identity, merged replays, ended grants, and expiry.
func TestManualGrantReplayPeriodMergeAndEndedRevocation(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	projectID := core.ProjectID(fixture.projectID)
	mustExec(t, database, `INSERT INTO customers (id, project_id, external_id) VALUES ('customer-target', $1, 'target-external')`, fixture.projectID)
	now := time.Now().UTC().Truncate(time.Second)
	write := persistence.ManualGrantWrite{
		ProjectID: projectID, CustomerID: core.CustomerID(fixture.customerID),
		ApplicationID: core.ApplicationID(fixture.applicationID), ProductID: core.ProductID(fixture.productID),
		EntitlementID: core.EntitlementID(fixture.entitlementID), Reason: "Goodwill", ActorKeyID: "admin-key",
		IdempotencyKey: "grant-period", StartsAt: now, EndsAt: now.AddDate(0, 0, 30),
	}
	create := func(write persistence.ManualGrantWrite) (persistence.ManualGrantResult, error) {
		var result persistence.ManualGrantResult
		err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
			var grantErr error
			result, grantErr = repository.CreateManualGrant(database.ctx, write)
			return grantErr
		})
		return result, err
	}
	created, err := create(write)
	if err != nil {
		t.Fatalf("CreateManualGrant() error = %v", err)
	}

	sameDurationLater := write
	sameDurationLater.StartsAt, sameDurationLater.EndsAt = now.Add(time.Minute), now.Add(time.Minute).AddDate(0, 0, 30)
	if replay, err := create(sameDurationLater); err != nil || !replay.Replay || replay.Grant.ID != created.Grant.ID {
		t.Fatalf("same-duration retry = %#v, %v, want replay", replay, err)
	}
	longer := sameDurationLater
	longer.EndsAt = longer.StartsAt.AddDate(0, 0, 365)
	if _, err := create(longer); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("different-period retry error = %v, want ErrConflict", err)
	}

	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, mergeErr := repository.MergeCustomers(database.ctx, persistence.CustomerMergeWrite{
			ProjectID: projectID, CanonicalCustomerID: "customer-target",
			SourceCustomerID: core.CustomerID(fixture.customerID), IdempotencyKey: "merge-after-grant", ActorKeyID: "admin-key",
		})
		return mergeErr
	})
	if err != nil {
		t.Fatalf("MergeCustomers() error = %v", err)
	}
	if replay, err := create(write); err != nil || !replay.Replay || replay.CanonicalCustomerID != "customer-target" {
		t.Fatalf("retry after merge = %#v, %v, want replay for the new canonical customer", replay, err)
	}

	ended := write
	ended.IdempotencyKey, ended.StartsAt, ended.EndsAt = "grant-ended", now.AddDate(0, 0, -2), now.AddDate(0, 0, -1)
	endedGrant, err := create(ended)
	if err != nil {
		t.Fatalf("create ended grant error = %v", err)
	}
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, revokeErr := repository.RevokeManualGrant(database.ctx, persistence.ManualGrantRevocation{
			ProjectID: projectID, CustomerID: "customer-target", GrantID: endedGrant.Grant.ID,
			Reason: "Too late", ActorKeyID: "admin-key", RevokedAt: now,
		})
		return revokeErr
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("revoke ended grant error = %v, want ErrConflict", err)
	}

	var sources []persistence.EntitlementSource
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		var loadErr error
		sources, loadErr = repository.CustomerEntitlementSources(context.Background(), projectID, "customer-target")
		return loadErr
	})
	if err != nil {
		t.Fatalf("CustomerEntitlementSources() error = %v", err)
	}
	found := map[string]persistence.EntitlementProjection{}
	for _, source := range sources {
		found[source.Projection.SourceManualGrantID] = source.Projection
	}
	if active := found[created.Grant.ID]; active.Access != core.AccessAllowed || active.AccessReason != core.AccessReasonManualGrant {
		t.Fatalf("active grant source = %#v", active)
	}
	if lapsed := found[endedGrant.Grant.ID]; lapsed.Access != core.AccessDenied || lapsed.AccessReason != core.AccessReasonExpired {
		t.Fatalf("ended grant source = %#v, want denied and expired", lapsed)
	}
}
