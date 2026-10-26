package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/verification"
)

// TestCustomerResolutionFollowsOneHop verifies alias, merged, chain, and cross-project lookup.
func TestCustomerResolutionFollowsOneHop(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	projectID := core.ProjectID(fixture.projectID)
	mustExec(t, database, `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ('customer-source', $1, 'source-external'),
			('customer-chain', $1, 'chain-external')
	`, fixture.projectID)
	mustExec(t, database, `INSERT INTO projects (id) VALUES ('project-2')`)
	mustExec(t, database, `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ('customer-other-project', 'project-2', 'only-other-project')
	`)
	mustExec(t, database, `
		INSERT INTO customer_aliases (
			project_id, external_id, canonical_customer_id, actor_role, actor_key_id
		) VALUES ($1, 'alias-external', $2, 'admin', 'admin-key')
	`, fixture.projectID, fixture.customerID)
	mustExec(t, database, `
		UPDATE customers SET merged_into_customer_id = $2
		WHERE project_id = $1 AND id = 'customer-source'
	`, fixture.projectID, fixture.customerID)
	mustExec(t, database, `
		UPDATE customers SET merged_into_customer_id = 'customer-source'
		WHERE project_id = $1 AND id = 'customer-chain'
	`, fixture.projectID)

	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		primary, err := repository.ResolveCustomer(database.ctx, projectID, "customer-external")
		if err != nil {
			return err
		}
		if primary.Match != persistence.CustomerMatchPrimary || primary.CanonicalCustomerID != core.CustomerID(fixture.customerID) {
			t.Fatalf("primary resolution = %#v", primary)
		}
		alias, err := repository.ResolveCustomer(database.ctx, projectID, "alias-external")
		if err != nil {
			return err
		}
		if alias.Match != persistence.CustomerMatchAlias || alias.CanonicalCustomerID != core.CustomerID(fixture.customerID) {
			t.Fatalf("alias resolution = %#v", alias)
		}
		resolvedAlias, err := repository.CustomerByExternalID(database.ctx, projectID, "alias-external")
		if err != nil {
			return err
		}
		if resolvedAlias.ID != core.CustomerID(fixture.customerID) || resolvedAlias.ExternalID != "customer-external" {
			t.Fatalf("alias customer = %#v, want canonical external id", resolvedAlias)
		}
		merged, err := repository.ResolveCustomer(database.ctx, projectID, "source-external")
		if err != nil {
			return err
		}
		if merged.Match != persistence.CustomerMatchMerged || merged.CanonicalCustomerID != core.CustomerID(fixture.customerID) {
			t.Fatalf("merged resolution = %#v", merged)
		}
		if _, err := repository.ResolveCustomer(database.ctx, projectID, "chain-external"); !errors.Is(err, persistence.ErrConflict) {
			t.Fatalf("chain resolution error = %v, want ErrConflict", err)
		}
		if _, err := repository.CustomerByExternalID(database.ctx, projectID, "only-other-project"); !errors.Is(err, persistence.ErrNotFound) {
			t.Fatalf("cross-project lookup error = %v, want ErrNotFound", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Transact() error = %v", err)
	}

	var replayed core.Customer
	err = store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		var putErr error
		replayed, putErr = repository.PutCustomer(database.ctx, core.Customer{
			ID: "customer-replay", ProjectID: projectID, ExternalID: "source-external",
		})
		return putErr
	})
	if err != nil {
		t.Fatalf("PutCustomer() replay error = %v", err)
	}
	if replayed.ID != "customer-source" || replayed.ExternalID != "source-external" {
		t.Fatalf("replayed customer = %#v, want the merged source row", replayed)
	}
	var mergedInto *string
	if err := database.conn.QueryRow(database.ctx, `
		SELECT merged_into_customer_id FROM customers WHERE id = 'customer-source'
	`).Scan(&mergedInto); err != nil {
		t.Fatalf("read merged source: %v", err)
	}
	if mergedInto == nil || *mergedInto != fixture.customerID {
		t.Fatalf("merged_into_customer_id = %v, want %q", mergedInto, fixture.customerID)
	}
}

// TestPutCustomerRejectsAliasExternalID verifies an alias cannot become a second customer.
func TestPutCustomerRejectsAliasExternalID(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	mustExec(t, database, `
		INSERT INTO customer_aliases (
			project_id, external_id, canonical_customer_id, actor_role, actor_key_id
		) VALUES ($1, 'alias-external', $2, 'application', 'application-key')
	`, fixture.projectID, fixture.customerID)

	err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, putErr := repository.PutCustomer(database.ctx, core.Customer{
			ID: "customer-new", ProjectID: core.ProjectID(fixture.projectID), ExternalID: "alias-external",
		})
		return putErr
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("PutCustomer() error = %v, want ErrConflict", err)
	}
	var customers int
	if err := database.conn.QueryRow(database.ctx, `SELECT count(*) FROM customers`).Scan(&customers); err != nil {
		t.Fatalf("count customers: %v", err)
	}
	if customers != 1 {
		t.Fatalf("customers = %d, want 1", customers)
	}
}

// TestPutEntitlementStoresEqualSourceCustomer verifies new projections cite their owner.
func TestPutEntitlementStoresEqualSourceCustomer(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	persistPurchaseWrites(t, database.ctx, store, newPurchaseWrites(t, fixture))

	var customerID, sourceCustomerID string
	if err := database.conn.QueryRow(database.ctx, `
		SELECT customer_id, source_customer_id FROM customer_entitlements
	`).Scan(&customerID, &sourceCustomerID); err != nil {
		t.Fatalf("read projection owner: %v", err)
	}
	if customerID != fixture.customerID || sourceCustomerID != customerID {
		t.Fatalf("projection customers = (%q, %q), want equal %q", customerID, sourceCustomerID, fixture.customerID)
	}
}

// TestCustomerClosurePreservesHistoricalOwners verifies evidence idempotency after a merge pointer exists.
func TestCustomerClosurePreservesHistoricalOwners(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	projectID := core.ProjectID(fixture.projectID)
	original := newPurchaseWrites(t, fixture)
	persistPurchaseWrites(t, database.ctx, store, original)
	mustExec(t, database, `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ('customer-canonical', $1, 'canonical-external'),
			('customer-outside', $1, 'outside-external')
	`, fixture.projectID)
	mustExec(t, database, `
		UPDATE customers SET merged_into_customer_id = 'customer-canonical'
		WHERE id = $1
	`, fixture.customerID)

	replay := original
	replay.evidence.CustomerID = "customer-canonical"
	replay.observation.CustomerID = "customer-canonical"
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		evidenceID, saveErr := repository.SaveEvidence(database.ctx, replay.evidence)
		if saveErr != nil {
			return saveErr
		}
		replay.observation.EvidenceID = evidenceID
		return repository.SaveObservation(database.ctx, replay.observation)
	})
	if err != nil {
		t.Fatalf("replay canonical snapshot error = %v", err)
	}
	var evidenceCustomer, observationCustomer string
	if err := database.conn.QueryRow(database.ctx, `
		SELECT customer_id FROM purchase_evidence WHERE payload_fingerprint = $1
	`, original.evidence.Payload.Fingerprint[:]).Scan(&evidenceCustomer); err != nil {
		t.Fatalf("read evidence owner: %v", err)
	}
	if err := database.conn.QueryRow(database.ctx, `
		SELECT customer_id FROM purchase_observations WHERE id = $1
	`, original.observation.Observation.ID).Scan(&observationCustomer); err != nil {
		t.Fatalf("read observation owner: %v", err)
	}
	if evidenceCustomer != fixture.customerID || observationCustomer != fixture.customerID {
		t.Fatalf("historical owners = (%q, %q), want %q", evidenceCustomer, observationCustomer, fixture.customerID)
	}

	fresh := original
	fresh.evidence.CustomerID = "customer-canonical"
	fresh.evidence.Payload = repositoryProtectedValue("canonical-new-evidence")
	fresh.observation.CustomerID = "customer-canonical"
	fresh.observation.Observation.ID = "observation-canonical-new"
	fresh.observation.Observation.ObservedAt = original.observation.Observation.ObservedAt.Add(-time.Hour)
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		evidenceID, saveErr := repository.SaveEvidence(database.ctx, fresh.evidence)
		if saveErr != nil {
			return saveErr
		}
		fresh.observation.EvidenceID = evidenceID
		return repository.SaveObservation(database.ctx, fresh.observation)
	})
	if err != nil {
		t.Fatalf("save canonical evidence error = %v", err)
	}
	if err := database.conn.QueryRow(database.ctx, `
		SELECT customer_id FROM purchase_observations WHERE id = 'observation-canonical-new'
	`).Scan(&observationCustomer); err != nil {
		t.Fatalf("read new observation owner: %v", err)
	}
	if observationCustomer != "customer-canonical" {
		t.Fatalf("new observation customer = %q, want customer-canonical", observationCustomer)
	}

	outside := original.evidence
	outside.CustomerID = "customer-outside"
	outside.Payload = repositoryProtectedValue("outside-evidence")
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, saveErr := repository.SaveEvidence(database.ctx, outside)
		return saveErr
	})
	if err != nil {
		t.Fatalf("save outside evidence error = %v", err)
	}
	outside.CustomerID = "customer-canonical"
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, saveErr := repository.SaveEvidence(database.ctx, outside)
		return saveErr
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("outside evidence error = %v, want ErrConflict", err)
	}

	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		return verification.RefreshCustomerProjection(
			database.ctx,
			repository,
			verification.NewDefaultProjector(),
			projectID,
			"customer-canonical",
			time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC),
		)
	})
	if err != nil {
		t.Fatalf("RefreshCustomerProjection() error = %v", err)
	}
	var canonicalObservation, sourceObservation string
	var canonicalAccess, sourceAccess string
	if err := database.conn.QueryRow(database.ctx, `
		SELECT source_observation_id, access_status
		FROM customer_entitlements WHERE customer_id = 'customer-canonical'
	`).Scan(&canonicalObservation, &canonicalAccess); err != nil {
		t.Fatalf("read canonical projection: %v", err)
	}
	if err := database.conn.QueryRow(database.ctx, `
		SELECT source_observation_id, access_status
		FROM customer_entitlements WHERE customer_id = $1
	`, fixture.customerID).Scan(&sourceObservation, &sourceAccess); err != nil {
		t.Fatalf("read source projection: %v", err)
	}
	if canonicalAccess != "allowed" || canonicalObservation != string(original.observation.Observation.ID) {
		t.Fatalf("canonical projection = (%q, %q), want source observation allowed", canonicalObservation, canonicalAccess)
	}
	if sourceObservation != string(original.observation.Observation.ID) || sourceAccess != "allowed" {
		t.Fatalf("source projection = (%q, %q), want unchanged allowed source", sourceObservation, sourceAccess)
	}
	var resolved core.Customer
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		var loadErr error
		resolved, loadErr = repository.CustomerByExternalID(database.ctx, projectID, "customer-external")
		if loadErr != nil {
			return loadErr
		}
		entitlements, loadErr := repository.CustomerEntitlements(database.ctx, projectID, resolved.ID)
		if loadErr != nil {
			return loadErr
		}
		if len(entitlements) != 1 || entitlements[0].Projection.Access != core.AccessAllowed {
			t.Fatalf("canonical entitlements = %#v", entitlements)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("entitlement read error = %v", err)
	}
	if resolved.ID != "customer-canonical" {
		t.Fatalf("resolved customer = %q, want customer-canonical", resolved.ID)
	}
}

// TestMergeCustomersEnforcesConflictIdempotencyAndAliasOwnership verifies merge audit rules.
func TestMergeCustomersEnforcesConflictIdempotencyAndAliasOwnership(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	projectID := core.ProjectID(fixture.projectID)
	canonicalWrites := newPurchaseWrites(t, fixture)
	canonicalWrites.projection.Access = core.AccessDenied
	canonicalWrites.projection.AccessReason = core.AccessReasonExpired
	canonicalWrites.observation.Observation.Access = core.AccessDenied
	canonicalWrites.observation.Observation.AccessReason = core.AccessReasonExpired
	canonicalWrites.observation.Observation.State = core.LifecycleExpired
	persistPurchaseWrites(t, database.ctx, store, canonicalWrites)

	mustExec(t, database, `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ('customer-source', $1, 'source-external')
	`, fixture.projectID)
	sourceWrites := newPurchaseWrites(t, fixture)
	sourceWrites.evidence.CustomerID = "customer-source"
	sourceWrites.evidence.Payload = repositoryProtectedValue("source-evidence")
	sourceWrites.observation.CustomerID = "customer-source"
	sourceWrites.observation.Observation.ID = "observation-source"
	sourceWrites.projection.CustomerID = "customer-source"
	sourceWrites.projection.SourceObservationID = "observation-source"
	sourceWrites.outbox.ID = "outbox-source"
	sourceWrites.outbox.AggregateID = "customer-source"
	persistPurchaseWrites(t, database.ctx, store, sourceWrites)

	conflictWrite := persistence.CustomerMergeWrite{
		ProjectID: projectID, CanonicalCustomerID: core.CustomerID(fixture.customerID),
		SourceCustomerID: "customer-source", IdempotencyKey: "merge-conflict", ActorKeyID: "admin-key",
	}
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		canonicalWrites.projection.Access = core.AccessAllowed
		canonicalWrites.projection.AccessReason = core.AccessReasonPurchaseValid
		if _, putErr := repository.PutEntitlement(database.ctx, canonicalWrites.projection); putErr != nil {
			return putErr
		}
		_, mergeErr := repository.MergeCustomers(database.ctx, conflictWrite)
		return mergeErr
	})
	if !errors.Is(err, persistence.ErrEntitlementConflict) {
		t.Fatalf("dual allowed merge error = %v, want ErrEntitlementConflict", err)
	}
	assertTableCount(t, database, "customer_merges", 0)

	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		denied := canonicalWrites.projection
		denied.Access = core.AccessDenied
		denied.AccessReason = core.AccessReasonExpired
		if _, putErr := repository.PutEntitlement(database.ctx, denied); putErr != nil {
			return putErr
		}
		return nil
	})
	if err != nil {
		t.Fatalf("restore denied projection error = %v", err)
	}

	write := conflictWrite
	write.IdempotencyKey = "merge-auth-user-42"
	var first persistence.CustomerMerge
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		result, mergeErr := repository.MergeCustomers(database.ctx, write)
		if mergeErr != nil {
			return mergeErr
		}
		first = result.Merge
		if result.Replay {
			t.Fatal("first merge was reported as a replay")
		}
		return verification.RefreshCustomerProjection(
			database.ctx, repository, verification.NewDefaultProjector(),
			projectID, write.CanonicalCustomerID, time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC),
		)
	})
	if err != nil {
		t.Fatalf("merge error = %v", err)
	}
	assertTableCount(t, database, "customer_merges", 1)
	var canonicalObservation, sourceAccess string
	if err := database.conn.QueryRow(database.ctx, `
		SELECT source_observation_id FROM customer_entitlements WHERE customer_id = $1
	`, fixture.customerID).Scan(&canonicalObservation); err != nil {
		t.Fatalf("read canonical projection: %v", err)
	}
	if canonicalObservation != "observation-source" {
		t.Fatalf("canonical source observation = %q, want observation-source", canonicalObservation)
	}
	if err := database.conn.QueryRow(database.ctx, `
		SELECT access_status FROM customer_entitlements WHERE customer_id = 'customer-source'
	`).Scan(&sourceAccess); err != nil {
		t.Fatalf("read source projection: %v", err)
	}
	if sourceAccess != "allowed" {
		t.Fatalf("source access = %q, want allowed", sourceAccess)
	}
	var evidenceCustomer string
	if err := database.conn.QueryRow(database.ctx, `
		SELECT customer_id FROM purchase_evidence WHERE customer_id = 'customer-source' LIMIT 1
	`).Scan(&evidenceCustomer); err != nil {
		t.Fatalf("source evidence missing: %v", err)
	}

	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		result, mergeErr := repository.MergeCustomers(database.ctx, write)
		if mergeErr != nil {
			return mergeErr
		}
		if !result.Replay || result.Merge.ID != first.ID {
			t.Fatalf("replay = %#v, want id %q", result, first.ID)
		}
		different := write
		different.IdempotencyKey = "merge-other-key"
		result, mergeErr = repository.MergeCustomers(database.ctx, different)
		if mergeErr != nil {
			return mergeErr
		}
		if !result.Replay || result.Merge.ID != first.ID {
			t.Fatalf("pair replay = %#v, want original id %q", result, first.ID)
		}
		conflict := write
		conflict.CanonicalCustomerID = "customer-source"
		conflict.SourceCustomerID = core.CustomerID(fixture.customerID)
		_, mergeErr = repository.MergeCustomers(database.ctx, conflict)
		return mergeErr
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("reused key error = %v, want ErrConflict", err)
	}
	assertTableCount(t, database, "customer_merges", 1)

	mustExec(t, database, `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ('customer-alias-owner', $1, 'alias-owner-external')
	`, fixture.projectID)
	mustExec(t, database, `
		INSERT INTO customer_aliases (
			project_id, external_id, canonical_customer_id, actor_role, actor_key_id
		) VALUES ($1, 'owned-alias', 'customer-alias-owner', 'admin', 'admin-key')
	`, fixture.projectID)
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, mergeErr := repository.MergeCustomers(database.ctx, persistence.CustomerMergeWrite{
			ProjectID: projectID, CanonicalCustomerID: core.CustomerID(fixture.customerID),
			SourceCustomerID: "customer-alias-owner", IdempotencyKey: "merge-alias-owner", ActorKeyID: "admin-key",
		})
		return mergeErr
	})
	if !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("alias owner merge error = %v, want ErrConflict", err)
	}
	assertTableCount(t, database, "customer_merges", 1)
}
