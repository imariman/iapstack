package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/validation"
	"github.com/imariman/iapstack/internal/verification"
)

// fixedReferenceFingerprinter returns one stored transaction fingerprint for every application it is asked about.
type fixedReferenceFingerprinter struct {
	fingerprint  [32]byte
	applications []core.ApplicationID
}

// ReferenceFingerprints records the application scopes and returns the fixed transaction fingerprint.
func (fingerprinter *fixedReferenceFingerprinter) ReferenceFingerprints(
	_ context.Context,
	_ core.ProjectID,
	applicationIDs []core.ApplicationID,
) ([]persistence.AdminReferenceFingerprint, error) {
	fingerprinter.applications = append([]core.ApplicationID(nil), applicationIDs...)
	fingerprints := make([]persistence.AdminReferenceFingerprint, 0, len(applicationIDs))
	for _, applicationID := range applicationIDs {
		fingerprints = append(fingerprints, persistence.AdminReferenceFingerprint{
			ApplicationID: applicationID, Role: core.ReferenceTransaction, Kind: "order_id",
			Fingerprint: fingerprinter.fingerprint,
		})
	}
	return fingerprints, nil
}

// TestManualGrantRepositoryFollowsClosureAndRevokes verifies canonical storage, replay, catalog checks, and revocation.
func TestManualGrantRepositoryFollowsClosureAndRevokes(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	fixture := seedCatalog(t, database)
	store := openRepositoryStore(t, database.ctx)
	projectID := core.ProjectID(fixture.projectID)
	canonicalID := core.CustomerID(fixture.customerID)
	mustExec(t, database, `INSERT INTO customers (id, project_id, external_id) VALUES ('customer-merged', $1, 'merged-external')`, fixture.projectID)
	mustExec(t, database, `INSERT INTO customers (id, project_id, external_id) VALUES ('customer-other', $1, 'other-external')`, fixture.projectID)
	mustExec(t, database, `INSERT INTO products (id, project_id, kind) VALUES ('product-consumable', $1, 'consumable')`, fixture.projectID)
	mustExec(t, database, `
		INSERT INTO product_entitlements (project_id, product_id, entitlement_id) VALUES ($1, 'product-consumable', $2)
	`, fixture.projectID, fixture.entitlementID)
	mustExec(t, database, `INSERT INTO products (id, project_id, kind) VALUES ('product-unmapped', $1, 'subscription')`, fixture.projectID)
	mustExec(t, database, `
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ('application-unmapped', $1, 'huawei_appgallery', 'sandbox', 'huawei-app-2')
	`, fixture.projectID)
	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		_, mergeErr := repository.MergeCustomers(database.ctx, persistence.CustomerMergeWrite{
			ProjectID: projectID, CanonicalCustomerID: canonicalID, SourceCustomerID: "customer-merged",
			IdempotencyKey: "merge-1", ActorKeyID: "admin-key",
		})
		return mergeErr
	})
	if err != nil {
		t.Fatalf("MergeCustomers() error = %v", err)
	}

	write := persistence.ManualGrantWrite{
		ProjectID: projectID, CustomerID: "customer-merged", ApplicationID: core.ApplicationID(fixture.applicationID),
		ProductID: core.ProductID(fixture.productID), EntitlementID: core.EntitlementID(fixture.entitlementID),
		Reason: "Support ticket 7", ActorKeyID: "admin-key", IdempotencyKey: "grant-1",
		StartsAt: now, EndsAt: now.AddDate(0, 0, 30),
	}
	create := func(write persistence.ManualGrantWrite) (persistence.ManualGrantResult, verification.ProjectionRefreshResult, error) {
		var result persistence.ManualGrantResult
		var refresh verification.ProjectionRefreshResult
		err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
			var grantErr error
			refresh = verification.ProjectionRefreshResult{}
			result, grantErr = repository.CreateManualGrant(database.ctx, write)
			if grantErr != nil || result.Replay {
				return grantErr
			}
			refresh, grantErr = verification.RefreshCustomerProjectionWithOptions(
				database.ctx, repository, verification.NewDefaultProjector(), projectID, result.CanonicalCustomerID, now, true,
			)
			return grantErr
		})
		return result, refresh, err
	}
	created, refresh, err := create(write)
	if err != nil {
		t.Fatalf("CreateManualGrant() error = %v", err)
	}
	if created.Replay || created.Grant.CustomerID != canonicalID || created.CanonicalCustomerID != canonicalID ||
		created.Grant.EntitlementKey != "pro" || refresh.ProjectionsChanged != 1 || refresh.WebhooksEnqueued != 1 {
		t.Fatalf("created grant = %#v, refresh %#v", created, refresh)
	}
	replayed, _, err := create(write)
	if err != nil || !replayed.Replay || replayed.Grant.ID != created.Grant.ID {
		t.Fatalf("replayed grant = %#v, error %v", replayed, err)
	}
	conflicting := write
	conflicting.Reason = "Another reason"
	if _, _, err := create(conflicting); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("conflicting replay error = %v, want ErrConflict", err)
	}
	for name, test := range map[string]struct {
		mutate func(*persistence.ManualGrantWrite)
		want   error
	}{
		"consumable":       {mutate: func(w *persistence.ManualGrantWrite) { w.ProductID = "product-consumable" }, want: validation.ErrInvalid},
		"not granting":     {mutate: func(w *persistence.ManualGrantWrite) { w.ProductID = "product-unmapped" }, want: validation.ErrInvalid},
		"unknown app":      {mutate: func(w *persistence.ManualGrantWrite) { w.ApplicationID = "application-missing" }, want: persistence.ErrNotFound},
		"unknown product":  {mutate: func(w *persistence.ManualGrantWrite) { w.ProductID = "product-missing" }, want: persistence.ErrNotFound},
		"unknown customer": {mutate: func(w *persistence.ManualGrantWrite) { w.CustomerID = "customer-missing" }, want: persistence.ErrNotFound},
		"app without mapping": {
			mutate: func(w *persistence.ManualGrantWrite) { w.ApplicationID = "application-unmapped" },
			want:   persistence.ErrManualGrantProductNotMapped,
		},
	} {
		invalid := write
		invalid.IdempotencyKey = "grant-" + name
		test.mutate(&invalid)
		if _, _, err := create(invalid); !errors.Is(err, test.want) {
			t.Fatalf("%s grant error = %v, want %v", name, err, test.want)
		}
	}

	var entitlements []persistence.CustomerEntitlement
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		var loadErr error
		entitlements, loadErr = repository.CustomerEntitlements(database.ctx, projectID, canonicalID)
		return loadErr
	})
	if err != nil {
		t.Fatalf("CustomerEntitlements() error = %v", err)
	}
	if len(entitlements) != 1 || entitlements[0].Projection.SourceKind != persistence.SourceKindManual ||
		entitlements[0].Projection.SourceManualGrantID != created.Grant.ID ||
		entitlements[0].SourceApplicationID != core.ApplicationID(fixture.applicationID) ||
		entitlements[0].SourceLineage != "manual:"+created.Grant.ID || entitlements[0].Projection.Access != core.AccessAllowed ||
		entitlements[0].Projection.AccessReason != core.AccessReasonManualGrant {
		t.Fatalf("manual projection = %#v", entitlements)
	}
	assertTableCount(t, database, "purchase_observations", 0)
	assertTableCount(t, database, "purchase_evidence", 0)
	assertTableCount(t, database, "provider_references", 0)
	assertTableCount(t, database, "outbox_events", 1)

	revoke := func(customerID core.CustomerID, reason string) (persistence.ManualGrantResult, error) {
		var result persistence.ManualGrantResult
		err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
			var revokeErr error
			result, revokeErr = repository.RevokeManualGrant(database.ctx, persistence.ManualGrantRevocation{
				ProjectID: projectID, CustomerID: customerID, GrantID: created.Grant.ID,
				Reason: reason, ActorKeyID: "admin-key-2", RevokedAt: now.Add(time.Hour),
			})
			if revokeErr != nil || result.Replay {
				return revokeErr
			}
			return verification.RefreshCustomerProjection(
				database.ctx, repository, verification.NewDefaultProjector(), projectID, result.CanonicalCustomerID, now.Add(time.Hour),
			)
		})
		return result, err
	}
	if _, err := revoke("customer-other", "Wrong closure"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("revoke from another customer error = %v, want ErrNotFound", err)
	}
	revoked, err := revoke("customer-merged", "Granted by mistake")
	if err != nil || revoked.Replay || revoked.Grant.RevokedAt == nil || !revoked.Grant.RevokedAt.Equal(now.Add(time.Hour)) ||
		revoked.Grant.RevokedByKeyID != "admin-key-2" || revoked.Grant.RevocationReason != "Granted by mistake" {
		t.Fatalf("revoked grant = %#v, error %v", revoked, err)
	}
	again, err := revoke(canonicalID, "Second click")
	if err != nil || !again.Replay || again.Grant.RevocationReason != "Granted by mistake" {
		t.Fatalf("repeated revocation = %#v, error %v", again, err)
	}
	err = store.Transact(database.ctx, func(repository persistence.Transaction) error {
		var loadErr error
		entitlements, loadErr = repository.CustomerEntitlements(database.ctx, projectID, canonicalID)
		return loadErr
	})
	if err != nil {
		t.Fatalf("CustomerEntitlements() after revocation error = %v", err)
	}
	if len(entitlements) != 1 || entitlements[0].Projection.Access != core.AccessDenied ||
		entitlements[0].Projection.AccessReason != core.AccessReasonRevoked || entitlements[0].Version != 2 {
		t.Fatalf("revoked projection = %#v", entitlements)
	}
	if entitlements[0].Projection.EffectivePeriod.EndsAt == nil || !entitlements[0].Projection.EffectivePeriod.EndsAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("revoked projection period = %#v, want it to end at the revocation", entitlements[0].Projection.EffectivePeriod)
	}
	assertTableCount(t, database, "outbox_events", 2)
}

// TestAdminSearchCustomersMatchesEscapesAndPaginates verifies match reasons, literal prefixes, and keyset pages.
func TestAdminSearchCustomersMatchesEscapesAndPaginates(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	purchase := seedPurchase(t, database)
	fixture := purchase.catalog
	store := openRepositoryStore(t, database.ctx)
	projectID := core.ProjectID(fixture.projectID)
	for index, externalID := range []string{"user_100", "userA100", "user%200", "user-300"} {
		mustExec(t, database, `
			INSERT INTO customers (id, project_id, external_id, created_at)
			VALUES ($1, $2, $3, now() - make_interval(mins => $4))
		`, "search-customer-"+string(rune('a'+index)), fixture.projectID, externalID, index+1)
	}
	mustExec(t, database, `
		INSERT INTO customer_aliases (project_id, external_id, canonical_customer_id, actor_role, actor_key_id)
		VALUES ($1, 'legacy-alias', 'search-customer-d', 'admin', 'admin-key')
	`, fixture.projectID)
	mustExec(t, database, `INSERT INTO projects (id) VALUES ('project-empty')`)

	search := func(query string, limit int, after *persistence.AdminCustomerSearchCursor, references persistence.AdminReferenceFingerprinter) persistence.AdminCustomerSearchPage {
		t.Helper()
		page, err := store.AdminSearchCustomers(database.ctx, persistence.AdminCustomerSearch{
			ProjectID: projectID, Query: query, Limit: limit, After: after, References: references,
		})
		if err != nil {
			t.Fatalf("AdminSearchCustomers(%q) error = %v", query, err)
		}
		return page
	}
	ids := func(page persistence.AdminCustomerSearchPage) []string {
		values := make([]string, 0, len(page.Results))
		for _, result := range page.Results {
			values = append(values, string(result.Customer.ID)+"/"+string(result.Match))
		}
		return values
	}
	for query, want := range map[string]string{
		"user_100":          "search-customer-a/external_id",
		"search-customer-b": "search-customer-b/customer_id",
		"legacy-alias":      "search-customer-d/alias",
		"user%":             "search-customer-c/external_id_prefix",
		"user_":             "search-customer-a/external_id_prefix",
	} {
		page := search(query, 0, nil, nil)
		if got := ids(page); len(got) != 1 || got[0] != want || page.Next != nil {
			t.Fatalf("search %q = %v, want only %s", query, got, want)
		}
	}
	if page := search("%", 0, nil, nil); len(page.Results) != 0 {
		t.Fatalf("wildcard search = %v, want no literal match", ids(page))
	}

	fingerprinter := &fixedReferenceFingerprinter{}
	copy(fingerprinter.fingerprint[:], bytes.Repeat([]byte{3}, 32))
	if page := search("GPA.0000", 0, nil, fingerprinter); len(page.Results) != 1 ||
		page.Results[0].Customer.ID != core.CustomerID(fixture.customerID) ||
		page.Results[0].Match != persistence.AdminCustomerMatchTransactionID ||
		page.Results[0].Customer.EntitlementCount != 0 || page.Results[0].Customer.LastObservedAt == nil {
		t.Fatalf("reference search = %#v", page.Results)
	}
	if len(fingerprinter.applications) != 1 || fingerprinter.applications[0] != core.ApplicationID(fixture.applicationID) {
		t.Fatalf("fingerprinted applications = %v", fingerprinter.applications)
	}
	copy(fingerprinter.fingerprint[:], bytes.Repeat([]byte{4}, 32))
	if page := search("GPA.0000", 0, nil, fingerprinter); len(page.Results) != 0 {
		t.Fatalf("unknown reference search = %v", ids(page))
	}

	seen := map[core.CustomerID]bool{}
	var after *persistence.AdminCustomerSearchCursor
	for pages := 0; ; pages++ {
		page := search("", 2, after, nil)
		for _, result := range page.Results {
			if seen[result.Customer.ID] || result.Match != "" {
				t.Fatalf("listing page %d repeated or labeled %#v", pages, result)
			}
			seen[result.Customer.ID] = true
		}
		if page.Next == nil {
			break
		}
		if pages > 5 {
			t.Fatal("listing pagination did not terminate")
		}
		after = page.Next
	}
	if len(seen) != 5 {
		t.Fatalf("listing saw %d customers, want 5", len(seen))
	}
	prefixed := search("user", 2, nil, nil)
	rest := search("user", 2, prefixed.Next, nil)
	if len(prefixed.Results) != 2 || prefixed.Next == nil || len(rest.Results) != 2 || rest.Next != nil ||
		prefixed.Results[0].Customer.ID != "search-customer-a" || rest.Results[1].Customer.ID != "search-customer-d" {
		t.Fatalf("prefix pages = %v then %v, want newest first across two pages", ids(prefixed), ids(rest))
	}

	if page, err := store.AdminSearchCustomers(database.ctx, persistence.AdminCustomerSearch{ProjectID: "project-empty"}); err != nil || len(page.Results) != 0 {
		t.Fatalf("empty project search = %#v, %v", page, err)
	}
	if _, err := store.AdminSearchCustomers(database.ctx, persistence.AdminCustomerSearch{ProjectID: "project-missing", Query: "x"}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("missing project search error = %v, want ErrNotFound", err)
	}
	if _, err := store.AdminSearchCustomers(database.ctx, persistence.AdminCustomerSearch{ProjectID: projectID, Query: "bad\x00"}); !errors.Is(err, validation.ErrInvalid) {
		t.Fatalf("control character search error = %v, want invalid", err)
	}
}

// TestAdminCustomerDetailDescribesMergedClosure verifies identity, sources, grants, and the timeline of one closure.
func TestAdminCustomerDetailDescribesMergedClosure(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	purchase := seedPurchase(t, database)
	fixture := purchase.catalog
	store := openRepositoryStore(t, database.ctx)
	projectID := core.ProjectID(fixture.projectID)
	canonicalID := core.CustomerID("customer-canonical")
	mustExec(t, database, `INSERT INTO customers (id, project_id, external_id) VALUES ($1, $2, 'canonical-external')`, canonicalID, fixture.projectID)
	mustExec(t, database, `
		INSERT INTO reconciliation_jobs (
			id, project_id, application_id, customer_id, payload_ciphertext, payload_fingerprint,
			encryption_key_id, available_at, processed_at
		) VALUES ('job-1', $1, $2, $3, 'ciphertext', $4, 'test-key', now(), now())
	`, fixture.projectID, fixture.applicationID, fixture.customerID, bytes.Repeat([]byte{5}, 32))
	now := time.Now().UTC()
	err := store.Transact(database.ctx, func(repository persistence.Transaction) error {
		if _, err := repository.MergeCustomers(database.ctx, persistence.CustomerMergeWrite{
			ProjectID: projectID, CanonicalCustomerID: canonicalID, SourceCustomerID: core.CustomerID(fixture.customerID),
			IdempotencyKey: "merge-detail", ActorKeyID: "admin-key",
		}); err != nil {
			return err
		}
		if _, err := repository.PutCustomerAlias(database.ctx, persistence.CustomerAliasWrite{
			ProjectID: projectID, CanonicalCustomerID: canonicalID, ExternalID: "alias-external",
			ActorRole: persistence.APIKeyRoleAdmin, ActorKeyID: "admin-key",
		}); err != nil {
			return err
		}
		return verification.RefreshCustomerProjection(
			database.ctx, repository, verification.NewDefaultProjector(), projectID, canonicalID, now,
		)
	})
	if err != nil {
		t.Fatalf("merge and refresh error = %v", err)
	}

	detail, err := store.AdminCustomerDetail(database.ctx, projectID, core.CustomerID(fixture.customerID))
	if err != nil {
		t.Fatalf("AdminCustomerDetail() error = %v", err)
	}
	if detail.Customer.ID != core.CustomerID(fixture.customerID) || detail.Customer.MergedIntoCustomerID == nil ||
		detail.CanonicalCustomerID != canonicalID || len(detail.Members) != 2 || detail.Members[0].ID != canonicalID ||
		len(detail.Aliases) != 1 || len(detail.Merges) != 1 || detail.Merges[0].SourceExternalID != "customer-external" {
		t.Fatalf("merged detail identity = %#v", detail)
	}
	if len(detail.Entitlements) != 1 || detail.Entitlements[0].SourceKind != persistence.SourceKindStoreObservation ||
		detail.Entitlements[0].SourceID != purchase.observationID ||
		detail.Entitlements[0].SourceApplicationID != core.ApplicationID(fixture.applicationID) ||
		len(detail.GrantOptions) != 1 || len(detail.GrantOptions[0].ApplicationIDs) != 1 {
		t.Fatalf("merged detail access = %#v options %#v", detail.Entitlements, detail.GrantOptions)
	}
	kinds := map[persistence.AdminTimelineKind]int{}
	for _, event := range detail.Timeline {
		kinds[event.Kind]++
	}
	for kind, want := range map[persistence.AdminTimelineKind]int{
		persistence.AdminTimelineCustomerCreated:    2,
		persistence.AdminTimelineAliasAdded:         1,
		persistence.AdminTimelineCustomerMerged:     1,
		persistence.AdminTimelinePurchaseObserved:   1,
		persistence.AdminTimelineEntitlementChanged: 1,
		persistence.AdminTimelineReconciliation:     1,
	} {
		if kinds[kind] != want {
			t.Fatalf("timeline %s = %d, want %d: %#v", kind, kinds[kind], want, detail.Timeline)
		}
	}
	if detail.TimelineTruncated {
		t.Fatal("small timeline reported truncation")
	}
	if _, err := store.AdminCustomerDetail(database.ctx, projectID, "customer-missing"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("missing customer detail error = %v, want ErrNotFound", err)
	}
	if _, err := store.AdminCustomerDetail(database.ctx, "project-other", canonicalID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("cross-project detail error = %v, want ErrNotFound", err)
	}
}
