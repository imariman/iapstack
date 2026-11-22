package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/verification"
)

// mergeSide describes one allowed access source a customer holds before a merge.
type mergeSide struct {
	// imported stores an import grant; otherwise the fixture store observation is stored.
	imported bool
	// productID is the import's product. Empty means the fixture product, which store rows always use.
	productID string
	// lineage adds a purchase token lineage reference to a store observation.
	lineage string
	// lapsed ends the access a day before the merge instead of 13 days after it.
	lapsed bool
	// unprojected stores an import grant without refreshing the customer's projection, so the
	// stored row stays the one an earlier projector chose.
	unprojected bool
}

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
			project_id, external_id, canonical_customer_id, actor_role, actor_key_id, actor_channel
		) VALUES ($1, 'alias-external', $2, 'admin', 'admin-key', 'api')
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
			project_id, external_id, canonical_customer_id, actor_role, actor_key_id, actor_channel
		) VALUES ($1, 'alias-external', $2, 'application', 'application-key', 'api')
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

// TestMergeCustomersRefreshesImportBackedProjection verifies historical grant ownership after a merge.
func TestMergeCustomersRefreshesImportBackedProjection(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	projectID := core.ProjectID(fixture.projectID)
	canonicalID := core.CustomerID("customer-import-canonical")
	mustExec(t, database, `
		INSERT INTO customers (id, project_id, external_id)
		VALUES ($1, $2, 'import-canonical-external')
	`, canonicalID, fixture.projectID)
	observedAt := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	grant := persistence.ImportGrant{
		ID: "imp-11111111111111111111111111111111", ProjectID: projectID,
		CustomerID: core.CustomerID(fixture.customerID), ApplicationID: core.ApplicationID(fixture.applicationID),
		ProductID: core.ProductID(fixture.productID), EntitlementID: core.EntitlementID(fixture.entitlementID),
		Origin: persistence.ImportOriginRevenueCat, OriginStore: "app_store", Environment: core.EnvironmentSandbox,
		Access: core.AccessAllowed, AccessReason: core.AccessReasonPurchaseValid,
		EffectivePeriod: core.EffectivePeriod{StartsAt: observedAt.Add(-time.Hour)}, ObservedAt: observedAt,
		ExportSHA256: [32]byte{1}, LineageFingerprint: [32]byte{2},
	}
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		writer, ok := repository.(interface {
			UpsertImportGrant(context.Context, persistence.ImportGrant) (persistence.ImportGrantResult, error)
		})
		if !ok {
			return errors.New("import grant writer is unavailable")
		}
		_, err := writer.UpsertImportGrant(database.ctx, grant)
		return err
	})
	if err != nil {
		t.Fatalf("UpsertImportGrant() error = %v", err)
	}
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		if _, err := repository.MergeCustomers(database.ctx, persistence.CustomerMergeWrite{
			ProjectID: projectID, CanonicalCustomerID: canonicalID,
			SourceCustomerID: core.CustomerID(fixture.customerID), IdempotencyKey: "merge-import", ActorKeyID: "admin-key",
		}); err != nil {
			return err
		}
		return verification.RefreshCustomerProjection(
			database.ctx, repository, verification.NewDefaultProjector(), projectID, canonicalID, observedAt,
		)
	})
	if err != nil {
		t.Fatalf("import-backed merge error = %v", err)
	}
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		entitlements, err := repository.CustomerEntitlements(database.ctx, projectID, canonicalID)
		if err != nil {
			return err
		}
		if len(entitlements) != 1 || entitlements[0].Projection.SourceImportID != grant.ID ||
			entitlements[0].Projection.SourceCustomerID != core.CustomerID(fixture.customerID) {
			t.Fatalf("canonical import projection = %#v", entitlements)
		}
		sources, err := repository.CustomerEntitlementSources(database.ctx, projectID, core.CustomerID(fixture.customerID))
		if err != nil {
			return err
		}
		if len(sources) != 1 || sources[0].Projection.CustomerID != canonicalID {
			t.Fatalf("source-customer closure sources = %#v", sources)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read import-backed merge error = %v", err)
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

	persistAllowedCustomer(t, database, store, fixture, "customer-source")

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
		_, mergeErr := verification.MergeCustomers(
			database.ctx, repository, verification.NewDefaultProjector(), conflictWrite, time.Now().UTC(),
		)
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
		result, mergeErr := verification.MergeCustomers(
			database.ctx, repository, verification.NewDefaultProjector(), write, time.Now().UTC(),
		)
		if mergeErr != nil {
			return mergeErr
		}
		first = result.Merge
		if result.Replay {
			t.Fatal("first merge was reported as a replay")
		}
		return nil
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
	if canonicalObservation != "observation-customer-source" {
		t.Fatalf("canonical source observation = %q, want observation-customer-source", canonicalObservation)
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
			project_id, external_id, canonical_customer_id, actor_role, actor_key_id, actor_channel
		) VALUES ($1, 'owned-alias', 'customer-alias-owner', 'admin', 'admin-key', 'dashboard')
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

// TestMergeCustomersIgnoresExpiredAllowedRows verifies two lapsed stored-allowed
// rows do not block a merge while a real current overlap still conflicts.
func TestMergeCustomersIgnoresExpiredAllowedRows(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	persistAllowedCustomer(t, database, store, fixture, fixture.customerID)
	persistAllowedCustomer(t, database, store, fixture, "customer-source")

	setStoredProjectionPeriod(t, database, fixture.customerID, "-2 hours", "1 hour")
	setStoredProjectionPeriod(t, database, "customer-source", "-2 hours", "1 hour")
	if err := mergeIntoFixtureCustomer(t, database, store, fixture, "merge-current-overlap"); !errors.Is(err, persistence.ErrEntitlementConflict) {
		t.Fatalf("current overlap merge error = %v, want ErrEntitlementConflict", err)
	}
	assertTableCount(t, database, "customer_merges", 0)

	setStoredProjectionPeriod(t, database, fixture.customerID, "-2 hours", "-1 hour")
	setStoredProjectionPeriod(t, database, "customer-source", "-2 hours", "-1 hour")
	if err := mergeIntoFixtureCustomer(t, database, store, fixture, "merge-expired-overlap"); err != nil {
		t.Fatalf("expired overlap merge error = %v, want success", err)
	}
	assertTableCount(t, database, "customer_merges", 1)

	var entitlements []persistence.CustomerEntitlement
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		var readErr error
		entitlements, readErr = repository.CustomerEntitlements(
			database.ctx, core.ProjectID(fixture.projectID), core.CustomerID(fixture.customerID),
		)
		return readErr
	})
	if err != nil || len(entitlements) != 1 {
		t.Fatalf("canonical entitlements = %#v, %v; want one", entitlements, err)
	}
	projection := entitlements[0].Projection
	access, reason := core.AccessAt(projection.Access, projection.AccessReason, projection.EffectivePeriod.EndsAt, time.Now().UTC())
	if access != core.AccessDenied || reason != core.AccessReasonExpired {
		t.Fatalf("canonical access after merge = %s/%s, want denied/expired", access, reason)
	}
}

// TestMergeCustomersChecksTheSurvivingRowOfASharedSource verifies a merge of one
// current and one lapsed row that cite one source. The fixture observations share the
// application product, carry no lineage reference, and were observed at the same
// instant, so the merged closure keeps only the source customer's observation. The
// merge is refused when that observation is the lapsed one, because current access
// would end, and proceeds when it is the current one. A not-yet-started allowed row is
// current here because reads ignore its start.
func TestMergeCustomersChecksTheSurvivingRowOfASharedSource(t *testing.T) {
	tests := map[string]struct {
		canonicalStart string
		canonicalEnd   string
		sourceStart    string
		sourceEnd      string
		wantConflict   bool
	}{
		"source lapsed":         {canonicalStart: "-2 hours", canonicalEnd: "1 hour", sourceStart: "-2 hours", sourceEnd: "-1 hour", wantConflict: true},
		"canonical not started": {canonicalStart: "1 hour", canonicalEnd: "2 hours", sourceStart: "-2 hours", sourceEnd: "-1 hour", wantConflict: true},
		"canonical durable":     {canonicalStart: "-2 hours", canonicalEnd: "", sourceStart: "-2 hours", sourceEnd: "-1 hour", wantConflict: true},
		"canonical lapsed":      {canonicalStart: "-2 hours", canonicalEnd: "-1 hour", sourceStart: "-2 hours", sourceEnd: "1 hour"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			database := openTestDatabase(t, postgres.LatestVersion)
			fixture := seedCatalog(t, database)
			store := openRepositoryStore(t, database.ctx)
			persistAllowedCustomer(t, database, store, fixture, fixture.customerID)
			persistAllowedCustomer(t, database, store, fixture, "customer-source")
			setStoredProjectionPeriod(t, database, fixture.customerID, test.canonicalStart, test.canonicalEnd)
			setStoredProjectionPeriod(t, database, "customer-source", test.sourceStart, test.sourceEnd)

			err := mergeIntoFixtureCustomer(t, database, store, fixture, "merge-shared-source")
			if test.wantConflict {
				if !errors.Is(err, persistence.ErrEntitlementConflict) {
					t.Fatalf("merge error = %v, want ErrEntitlementConflict", err)
				}
				assertTableCount(t, database, "customer_merges", 0)
				return
			}
			if err != nil {
				t.Fatalf("merge error = %v, want success", err)
			}
			entitlement, access := customerAccessNow(t, database, store, fixture, fixture.customerID)
			if access != core.AccessAllowed || entitlement.Projection.SourceObservationID != "observation-customer-source" {
				t.Fatalf("canonical access after merge = %s from %#v, want the source's current row",
					access, entitlement.Projection)
			}
		})
	}
}

// TestMergeCustomersKeepsCurrentAccessWhenOneAllowedRowLapsed verifies a merge of one
// current and one lapsed stored-allowed row proceeds when the merged projection keeps
// the current access, whichever customer and source kind holds it. The projector
// compares rows of different sources by end, and a current store observation also
// supersedes a lapsed import of its own product.
func TestMergeCustomersKeepsCurrentAccessWhenOneAllowedRowLapsed(t *testing.T) {
	tests := map[string]struct {
		canonical mergeSide
		source    mergeSide
		wantKind  persistence.SourceKind
	}{
		"current import and lapsed store of another product": {
			canonical: mergeSide{imported: true, productID: "product-other"},
			source:    mergeSide{lapsed: true},
			wantKind:  persistence.SourceKindImport,
		},
		"lapsed store and current import of another product": {
			canonical: mergeSide{lapsed: true},
			source:    mergeSide{imported: true, productID: "product-other"},
			wantKind:  persistence.SourceKindImport,
		},
		"current store and lapsed import of another product": {
			canonical: mergeSide{},
			source:    mergeSide{imported: true, productID: "product-other", lapsed: true},
			wantKind:  persistence.SourceKindStoreObservation,
		},
		"current store and lapsed import of the same product": {
			canonical: mergeSide{},
			source:    mergeSide{imported: true, lapsed: true},
			wantKind:  persistence.SourceKindStoreObservation,
		},
		"lapsed import and current store of the same product": {
			canonical: mergeSide{imported: true, lapsed: true},
			source:    mergeSide{},
			wantKind:  persistence.SourceKindStoreObservation,
		},
		"current and lapsed store rows of different lineages": {
			canonical: mergeSide{lineage: "purchase-token-a"},
			source:    mergeSide{lineage: "purchase-token-b", lapsed: true},
			wantKind:  persistence.SourceKindStoreObservation,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			database := openTestDatabase(t, postgres.LatestVersion)
			fixture := seedCatalog(t, database)
			store := openRepositoryStore(t, database.ctx)
			persistMergeSide(t, database, store, fixture, fixture.customerID, test.canonical)
			persistMergeSide(t, database, store, fixture, "customer-source", test.source)

			if err := mergeIntoFixtureCustomer(t, database, store, fixture, "merge-one-lapsed"); err != nil {
				t.Fatalf("merge error = %v, want success", err)
			}
			assertTableCount(t, database, "customer_merges", 1)
			entitlement, access := customerAccessNow(t, database, store, fixture, fixture.customerID)
			if access != core.AccessAllowed || entitlement.Projection.Normalized().SourceKind != test.wantKind {
				t.Fatalf("canonical access after merge = %s from %#v, want allowed from %s",
					access, entitlement.Projection, test.wantKind)
			}
		})
	}
}

// TestMergeCustomersRefusesLapsedStoreRowOfTheImportProduct verifies a current import
// and a lapsed store observation of the same application product conflict. The
// observation supersedes every import of its product whatever their ends, so the merged
// projection would be the lapsed row and current access would end. The import customer
// keeps access.
func TestMergeCustomersRefusesLapsedStoreRowOfTheImportProduct(t *testing.T) {
	tests := map[string]struct {
		canonical mergeSide
		source    mergeSide
	}{
		"current import and lapsed store": {canonical: mergeSide{imported: true}, source: mergeSide{lapsed: true}},
		"lapsed store and current import": {canonical: mergeSide{lapsed: true}, source: mergeSide{imported: true}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			database := openTestDatabase(t, postgres.LatestVersion)
			fixture := seedCatalog(t, database)
			store := openRepositoryStore(t, database.ctx)
			persistMergeSide(t, database, store, fixture, fixture.customerID, test.canonical)
			persistMergeSide(t, database, store, fixture, "customer-source", test.source)

			err := mergeIntoFixtureCustomer(t, database, store, fixture, "merge-lapsed-store")
			if !errors.Is(err, persistence.ErrEntitlementConflict) {
				t.Fatalf("merge error = %v, want ErrEntitlementConflict", err)
			}
			assertTableCount(t, database, "customer_merges", 0)
			importCustomer := fixture.customerID
			if test.source.imported {
				importCustomer = "customer-source"
			}
			entitlement, access := customerAccessNow(t, database, store, fixture, importCustomer)
			if access != core.AccessAllowed || entitlement.Projection.Normalized().SourceKind != persistence.SourceKindImport {
				t.Fatalf("import customer access = %s from %#v, want the allowed import", access, entitlement.Projection)
			}
		})
	}
}

// TestMergeCustomersRefusesUnprojectedStoreObservationOfTheImportProduct verifies a merge
// is refused when a store observation the other customer does not project would supersede
// current imported access. The source customer projects a lapsed import of another product;
// only its sources show the denied store observation of the canonical import's product.
func TestMergeCustomersRefusesUnprojectedStoreObservationOfTheImportProduct(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	persistMergeSide(t, database, store, fixture, fixture.customerID, mergeSide{imported: true})
	persistAllowedCustomerWrites(t, database, store, fixture, "customer-source", func(writes *purchaseWrites) {
		writes.observation.Observation.State = core.LifecycleExpired
		writes.observation.Observation.Access = core.AccessDenied
		writes.observation.Observation.AccessReason = core.AccessReasonExpired
		writes.projection.Access = core.AccessDenied
		writes.projection.AccessReason = core.AccessReasonExpired
	})
	persistMergeSide(t, database, store, fixture, "customer-source",
		mergeSide{imported: true, productID: "product-other", lapsed: true})
	sourceEntitlement, sourceAccess := customerAccessNow(t, database, store, fixture, "customer-source")
	if sourceAccess == core.AccessAllowed || sourceEntitlement.Projection.SourceProductID != "product-other" {
		t.Fatalf("source projection = %s from %#v, want the lapsed import of product-other",
			sourceAccess, sourceEntitlement.Projection)
	}

	err := mergeIntoFixtureCustomer(t, database, store, fixture, "merge-unprojected-store")
	if !errors.Is(err, persistence.ErrEntitlementConflict) {
		t.Fatalf("merge error = %v, want ErrEntitlementConflict", err)
	}
	assertTableCount(t, database, "customer_merges", 0)
	entitlement, access := customerAccessNow(t, database, store, fixture, fixture.customerID)
	if access != core.AccessAllowed || entitlement.Projection.Normalized().SourceKind != persistence.SourceKindImport {
		t.Fatalf("canonical access = %s from %#v, want the allowed import", access, entitlement.Projection)
	}
}

// TestMergeCustomersRefusesCurrentAccessHiddenByAStaleProjection verifies a merge reads
// each customer's sources and not only its stored projection. The source customer's row is
// a lapsed store observation that the earlier store-first order chose over its current
// import of another product, so both customers can use the entitlement now.
func TestMergeCustomersRefusesCurrentAccessHiddenByAStaleProjection(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	persistMergeSide(t, database, store, fixture, fixture.customerID,
		mergeSide{imported: true, productID: "product-third"})
	persistMergeSide(t, database, store, fixture, "customer-source", mergeSide{lapsed: true})
	persistMergeSide(t, database, store, fixture, "customer-source",
		mergeSide{imported: true, productID: "product-other", unprojected: true})
	sourceEntitlement, sourceAccess := customerAccessNow(t, database, store, fixture, "customer-source")
	if sourceAccess == core.AccessAllowed ||
		sourceEntitlement.Projection.Normalized().SourceKind != persistence.SourceKindStoreObservation {
		t.Fatalf("source projection = %s from %#v, want the stale lapsed store row",
			sourceAccess, sourceEntitlement.Projection)
	}

	err := mergeIntoFixtureCustomer(t, database, store, fixture, "merge-stale-projection")
	if !errors.Is(err, persistence.ErrEntitlementConflict) {
		t.Fatalf("merge error = %v, want ErrEntitlementConflict", err)
	}
	assertTableCount(t, database, "customer_merges", 0)
}

// persistMergeSide stores side for customerID, creating that customer when it is not
// the fixture customer, and projects it unless side is unprojected.
func persistMergeSide(
	t *testing.T,
	database *testDatabase,
	store *postgres.Store,
	fixture catalogFixture,
	customerID string,
	side mergeSide,
) {
	t.Helper()
	if !side.imported {
		persistAllowedCustomerWrites(t, database, store, fixture, customerID, func(writes *purchaseWrites) {
			if side.lineage == "" {
				return
			}
			reference, err := core.NewStoreReference(core.ReferenceQuery, "purchase_token", side.lineage)
			if err != nil {
				t.Fatalf("NewStoreReference() error = %v", err)
			}
			writes.observation.Observation.References = append(writes.observation.Observation.References, reference)
			writes.observation.References = append(writes.observation.References, persistence.ProtectedReference{
				Role: reference.Role, Kind: reference.Kind, Value: repositoryProtectedValue(side.lineage),
			})
		})
		endInterval := "13 days"
		if side.lapsed {
			endInterval = "-1 day"
		}
		setStoredProjectionPeriod(t, database, customerID, "-17 days", endInterval)
		return
	}
	if customerID != fixture.customerID {
		mustExec(t, database, `
			INSERT INTO customers (id, project_id, external_id)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING
		`, customerID, fixture.projectID, customerID+"-external")
	}
	productID := side.productID
	if productID == "" {
		productID = fixture.productID
	} else {
		mustExec(t, database, `
			INSERT INTO products (id, project_id, kind) VALUES ($1, $2, 'subscription')
			ON CONFLICT DO NOTHING
		`, productID, fixture.projectID)
		mustExec(t, database, `
			INSERT INTO product_entitlements (project_id, product_id, entitlement_id) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING
		`, fixture.projectID, productID, fixture.entitlementID)
	}
	grantID, lineage := "imp-11111111111111111111111111111111", [32]byte{1}
	if customerID != fixture.customerID {
		grantID, lineage = "imp-22222222222222222222222222222222", [32]byte{2}
	}
	now := time.Now().UTC()
	endsAt := now.AddDate(0, 0, 13)
	if side.lapsed {
		endsAt = now.AddDate(0, 0, -1)
	}
	grant := persistence.ImportGrant{
		ID: grantID, ProjectID: core.ProjectID(fixture.projectID), CustomerID: core.CustomerID(customerID),
		ApplicationID: core.ApplicationID(fixture.applicationID), ProductID: core.ProductID(productID),
		EntitlementID: core.EntitlementID(fixture.entitlementID),
		Origin:        persistence.ImportOriginRevenueCat, OriginStore: "app_store", Environment: core.EnvironmentSandbox,
		Access: core.AccessAllowed, AccessReason: core.AccessReasonPurchaseValid,
		EffectivePeriod: core.EffectivePeriod{StartsAt: now.AddDate(0, 0, -17), EndsAt: &endsAt},
		ObservedAt:      now.AddDate(0, 0, -2), ExportSHA256: [32]byte{3}, LineageFingerprint: lineage,
	}
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		writer, ok := repository.(interface {
			UpsertImportGrant(context.Context, persistence.ImportGrant) (persistence.ImportGrantResult, error)
		})
		if !ok {
			return errors.New("import grant writer is unavailable")
		}
		if _, err := writer.UpsertImportGrant(database.ctx, grant); err != nil || side.unprojected {
			return err
		}
		return verification.RefreshCustomerProjection(
			database.ctx, repository, verification.NewDefaultProjector(),
			core.ProjectID(fixture.projectID), core.CustomerID(customerID), now,
		)
	})
	if err != nil {
		t.Fatalf("project %s import error = %v", customerID, err)
	}
}

// customerAccessNow reads the only projection of customerID and its access at the
// current time, as the public read path applies the effective end.
func customerAccessNow(
	t *testing.T,
	database *testDatabase,
	store *postgres.Store,
	fixture catalogFixture,
	customerID string,
) (persistence.CustomerEntitlement, core.AccessStatus) {
	t.Helper()
	var entitlements []persistence.CustomerEntitlement
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		var readErr error
		entitlements, readErr = repository.CustomerEntitlements(
			database.ctx, core.ProjectID(fixture.projectID), core.CustomerID(customerID),
		)
		return readErr
	})
	if err != nil || len(entitlements) != 1 {
		t.Fatalf("%s entitlements = %#v, %v; want one", customerID, entitlements, err)
	}
	projection := entitlements[0].Projection
	access, _ := core.AccessAt(projection.Access, projection.AccessReason, projection.EffectivePeriod.EndsAt, time.Now().UTC())
	return entitlements[0], access
}

// mergeIntoFixtureCustomer merges customer-source into the fixture customer through the
// merge use case, which compares access and refreshes the canonical projection, as the
// merge route does.
func mergeIntoFixtureCustomer(
	t *testing.T,
	database *testDatabase,
	store *postgres.Store,
	fixture catalogFixture,
	idempotencyKey string,
) error {
	t.Helper()
	return store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, err := verification.MergeCustomers(database.ctx, repository, verification.NewDefaultProjector(),
			persistence.CustomerMergeWrite{
				ProjectID: core.ProjectID(fixture.projectID), CanonicalCustomerID: core.CustomerID(fixture.customerID),
				SourceCustomerID: "customer-source", IdempotencyKey: idempotencyKey, ActorKeyID: "admin-key",
			}, time.Now().UTC())
		return err
	})
}

// persistAllowedCustomer stores one allowed projection for a catalog customer.
func persistAllowedCustomer(
	t *testing.T,
	database *testDatabase,
	store *postgres.Store,
	fixture catalogFixture,
	customerID string,
) {
	t.Helper()
	persistAllowedCustomerWrites(t, database, store, fixture, customerID, nil)
}

// persistAllowedCustomerWrites stores one allowed projection for a catalog customer
// after edit, when set, adjusts the purchase writes.
func persistAllowedCustomerWrites(
	t *testing.T,
	database *testDatabase,
	store *postgres.Store,
	fixture catalogFixture,
	customerID string,
	edit func(*purchaseWrites),
) {
	t.Helper()
	writes := newPurchaseWrites(t, fixture)
	if customerID != fixture.customerID {
		mustExec(t, database, `
			INSERT INTO customers (id, project_id, external_id)
			VALUES ($1, $2, $3)
		`, customerID, fixture.projectID, customerID+"-external")
		writes.evidence.CustomerID = core.CustomerID(customerID)
		writes.evidence.Payload = repositoryProtectedValue(customerID + "-evidence")
		writes.observation.CustomerID = core.CustomerID(customerID)
		writes.observation.Observation.ID = core.ObservationID("observation-" + customerID)
		writes.projection.CustomerID = core.CustomerID(customerID)
		writes.projection.SourceObservationID = writes.observation.Observation.ID
		writes.outbox.ID = "outbox-" + customerID
		writes.outbox.AggregateID = customerID
	}
	if edit != nil {
		edit(&writes)
	}
	persistPurchaseWrites(t, database.ctx, store, writes)
}

// setStoredProjectionPeriod rewrites one customer's stored projection period, and
// the period of the observation it cites, relative to the database clock. An
// empty end interval stores a durable open end.
func setStoredProjectionPeriod(
	t *testing.T,
	database *testDatabase,
	customerID string,
	startInterval string,
	endInterval string,
) {
	t.Helper()
	mustExec(t, database, `
		WITH projection AS (
			UPDATE customer_entitlements
			SET effective_starts_at = now() + $2::interval,
				effective_ends_at = CASE WHEN $3 = '' THEN NULL ELSE now() + $3::interval END
			WHERE customer_id = $1
			RETURNING source_observation_id
		)
		UPDATE purchase_observations
		SET effective_starts_at = now() + $2::interval,
			effective_ends_at = CASE WHEN $3 = '' THEN NULL ELSE now() + $3::interval END
		WHERE id IN (SELECT source_observation_id FROM projection)
	`, customerID, startInterval, endInterval)
}
