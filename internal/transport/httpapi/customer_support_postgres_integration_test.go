//go:build integration

package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
)

const (
	// supportBonusEntitlementID identifies an entitlement with no store purchase in the fixture.
	supportBonusEntitlementID = "http-entitlement-bonus"
	// supportBonusProductID identifies the product that can back a bonus manual grant.
	supportBonusProductID = "http-product-bonus"
	// supportUnmappedApplicationID identifies an application that maps no store product.
	supportUnmappedApplicationID = "http-application-unmapped"
)

// TestCustomerSupportSearchDetailAndManualGrantHTTP verifies search, the timeline, and the manual grant lifecycle.
func TestCustomerSupportSearchDetailAndManualGrantHTTP(t *testing.T) {
	fixture := newAPIIntegrationFixture(t)
	bootstrap := testBootstrapAdminKey
	project := "/v1/admin/projects/" + apiIntegrationProjectID
	seedCustomerSupportCatalog(t, fixture, bootstrap)

	var adminKey map[string]string
	fixture.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", bootstrap, keyRequest{Role: persistence.APIKeyRoleAdmin}, http.StatusCreated, &adminKey)
	admin := adminKey["key"]
	fixture.requestJSON(t, http.MethodPut, project+"/customers/"+apiIntegrationCustomerID+"/aliases/legacy-support-user",
		admin, map[string]string{}, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut, project+"/customers/http-customer-2",
		admin, customerRequest{ExternalID: "http-external-customer-2"}, http.StatusOK, nil)

	search := func(query string, extra string) adminCustomerSearchResponse {
		t.Helper()
		var page adminCustomerSearchResponse
		body := fixture.requestJSON(t, http.MethodGet,
			project+"/customers?query="+url.QueryEscape(query)+extra, admin, nil, http.StatusOK, &page)
		for _, secret := range []string{apiIntegrationPurchaseToken, "ciphertext", "fingerprint"} {
			if bytes.Contains(body, []byte(secret)) {
				t.Fatalf("search response exposed %q: %s", secret, body)
			}
		}
		return page
	}
	for _, test := range []struct {
		query string
		match persistence.AdminCustomerMatch
	}{
		{query: apiIntegrationExternalCustomerID, match: persistence.AdminCustomerMatchExternalID},
		{query: "  " + apiIntegrationCustomerID + " ", match: persistence.AdminCustomerMatchCustomerID},
		{query: "legacy-support-user", match: persistence.AdminCustomerMatchAlias},
		{query: apiIntegrationTransactionID, match: persistence.AdminCustomerMatchTransactionID},
	} {
		page := search(test.query, "")
		if len(page.Customers) != 1 || page.Customers[0].ID != apiIntegrationCustomerID ||
			page.Customers[0].Match != test.match || page.NextPageToken != "" {
			t.Fatalf("search %q = %#v, want %s match", test.query, page, test.match)
		}
	}
	if page := search(apiIntegrationPurchaseToken, ""); len(page.Customers) != 0 {
		t.Fatalf("purchase token search = %#v, want no match", page)
	}
	if page := search("no-such-customer%_", ""); len(page.Customers) != 0 {
		t.Fatalf("wildcard search = %#v, want no match", page)
	}
	first := search("http-external-customer", "&limit=1")
	if len(first.Customers) != 1 || first.Customers[0].Match != persistence.AdminCustomerMatchExternalIDPrefix ||
		first.NextPageToken == "" {
		t.Fatalf("first prefix page = %#v", first)
	}
	second := search("http-external-customer", "&limit=1&page_token="+url.QueryEscape(first.NextPageToken))
	if len(second.Customers) != 1 || second.Customers[0].ID == first.Customers[0].ID || second.NextPageToken != "" {
		t.Fatalf("second prefix page = %#v, first %#v", second, first)
	}
	fixture.requestJSON(t, http.MethodGet, project+"/customers?query=other&page_token="+url.QueryEscape(first.NextPageToken),
		admin, nil, http.StatusBadRequest, nil)
	fixture.requestJSON(t, http.MethodGet, project+"/customers?query="+url.QueryEscape("bad\x01text"),
		admin, nil, http.StatusBadRequest, nil)
	fixture.requestJSON(t, http.MethodGet, "/v1/admin/projects/project-other/customers?query=x",
		admin, nil, http.StatusNotFound, nil)
	var listing adminCustomerSearchResponse
	fixture.requestJSON(t, http.MethodGet, project+"/customers", admin, nil, http.StatusOK, &listing)
	if len(listing.Customers) != 2 || listing.Customers[0].Match != "" {
		t.Fatalf("listing = %#v", listing)
	}

	grantPath := project + "/customers/" + apiIntegrationCustomerID + "/manual-grants"
	duration := 30
	bonusGrant := manualGrantRequest{
		IdempotencyKey: "grant-bonus-1", EntitlementID: supportBonusEntitlementID, ProductID: supportBonusProductID,
		ApplicationID: apiIntegrationApplicationID, Reason: "Support ticket 1042: store outage", DurationDays: &duration,
	}
	fixture.requestJSON(t, http.MethodPost, grantPath, bootstrap, bonusGrant, http.StatusForbidden, nil)
	unmapped := bonusGrant
	unmapped.IdempotencyKey, unmapped.ApplicationID = "grant-unmapped", supportUnmappedApplicationID
	unmappedBody := fixture.requestJSON(t, http.MethodPost, grantPath, admin, unmapped, http.StatusUnprocessableEntity, nil)
	if !bytes.Contains(unmappedBody, []byte(`"code":"product_not_mapped"`)) {
		t.Fatalf("unmapped application grant = %s", unmappedBody)
	}
	var granted manualGrantResultResponse
	fixture.requestJSON(t, http.MethodPost, grantPath, admin, bonusGrant, http.StatusOK, &granted)
	if granted.Replay || granted.ProjectionsChanged != 1 || granted.WebhooksEnqueued != 1 ||
		granted.Grant.Status != manualGrantStatusActive || granted.Grant.Reason != bonusGrant.Reason ||
		granted.Grant.ActorKeyID == "" || !granted.Grant.EffectiveEndsAt.Equal(fixture.now.AddDate(0, 0, 30)) {
		t.Fatalf("manual grant = %#v", granted)
	}
	var replayed manualGrantResultResponse
	fixture.requestJSON(t, http.MethodPost, grantPath, admin, bonusGrant, http.StatusOK, &replayed)
	if !replayed.Replay || replayed.Grant.ID != granted.Grant.ID || replayed.ProjectionsChanged != 0 {
		t.Fatalf("manual grant replay = %#v", replayed)
	}
	conflicting := bonusGrant
	conflicting.Reason = "A different reason"
	fixture.requestJSON(t, http.MethodPost, grantPath, admin, conflicting, http.StatusConflict, nil)

	premiumGrant := manualGrantRequest{
		IdempotencyKey: "grant-premium-1", EntitlementID: apiIntegrationEntitlementID, ProductID: apiIntegrationProductID,
		ApplicationID: apiIntegrationApplicationID, Reason: "Goodwill extension", DurationDays: &duration,
	}
	var shadowed manualGrantResultResponse
	fixture.requestJSON(t, http.MethodPost, grantPath, admin, premiumGrant, http.StatusOK, &shadowed)
	if shadowed.ProjectionsChanged != 0 || shadowed.WebhooksEnqueued != 0 {
		t.Fatalf("grant behind lifetime store access = %#v, want no projection change", shadowed)
	}

	var session customerSessionResponse
	var applicationKey map[string]string
	fixture.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", bootstrap, keyRequest{
		Role: persistence.APIKeyRoleApplication, ProjectID: apiIntegrationProjectID, ApplicationID: apiIntegrationApplicationID,
	}, http.StatusCreated, &applicationKey)
	fixture.requestJSON(t, http.MethodPost, "/v1/applications/"+apiIntegrationApplicationID+"/customer-sessions",
		applicationKey["key"], customerSessionRequest{ExternalCustomerID: apiIntegrationExternalCustomerID}, http.StatusCreated, &session)
	publicEntitlements := func() map[string]entitlementResponse {
		t.Helper()
		var snapshot struct {
			Entitlements []entitlementResponse `json:"entitlements"`
		}
		fixture.requestJSON(t, http.MethodGet,
			"/v1/applications/"+apiIntegrationApplicationID+"/customers/"+apiIntegrationExternalCustomerID+"/entitlements",
			session.Token, nil, http.StatusOK, &snapshot)
		byKey := make(map[string]entitlementResponse, len(snapshot.Entitlements))
		for _, entitlement := range snapshot.Entitlements {
			byKey[entitlement.Key] = entitlement
		}
		return byKey
	}
	if bonus := publicEntitlements()["bonus"]; bonus.Access != core.AccessAllowed || bonus.EffectiveEndsAt == nil ||
		bonus.Reason != core.AccessReasonManualGrant {
		t.Fatalf("public bonus entitlement = %#v", bonus)
	}

	var payload map[string]any
	var rawPayload []byte
	if err := fixture.database.conn.QueryRow(fixture.database.ctx, `
		SELECT payload FROM outbox_events WHERE payload->>'source' = 'manual' ORDER BY occurred_at, id LIMIT 1
	`).Scan(&rawPayload); err != nil {
		t.Fatalf("load manual outbox event: %v", err)
	}
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		t.Fatalf("decode manual outbox event: %v", err)
	}
	if payload["schema_version"] != float64(3) || payload["source_manual_grant_id"] != granted.Grant.ID ||
		payload["source_observation_id"] != nil || payload["source_import_id"] != nil ||
		payload["entitlement_key"] != "bonus" || payload["access"] != "allowed" ||
		payload["access_reason"] != "manual_grant" {
		t.Fatalf("manual outbox payload = %s", rawPayload)
	}
	var storeVersions []int
	rows, err := fixture.database.conn.Query(fixture.database.ctx, `
		SELECT DISTINCT (payload->>'schema_version')::int FROM outbox_events
		WHERE COALESCE(payload->>'source', '') <> 'manual'
	`)
	if err != nil {
		t.Fatalf("load store outbox versions: %v", err)
	}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan store outbox version: %v", err)
		}
		storeVersions = append(storeVersions, version)
	}
	rows.Close()
	if len(storeVersions) != 1 || storeVersions[0] != 1 {
		t.Fatalf("store outbox schema versions = %v, want only 1", storeVersions)
	}
	if bytes.Contains(rawPayload, []byte(bonusGrant.Reason)) {
		t.Fatalf("manual outbox payload exposed the audit reason: %s", rawPayload)
	}

	revokePath := grantPath + "/" + granted.Grant.ID + ":revoke"
	fixture.requestJSON(t, http.MethodPost, grantPath+"/"+granted.Grant.ID, admin,
		manualGrantRevocationRequest{Reason: "x"}, http.StatusNotFound, nil)
	fixture.requestJSON(t, http.MethodPost, revokePath, admin, manualGrantRevocationRequest{}, http.StatusBadRequest, nil)
	var revoked manualGrantResultResponse
	fixture.requestJSON(t, http.MethodPost, revokePath, admin,
		manualGrantRevocationRequest{Reason: "Granted to the wrong account"}, http.StatusOK, &revoked)
	if revoked.Replay || revoked.Grant.Status != manualGrantStatusRevoked || revoked.Grant.RevokedAt == nil ||
		revoked.ProjectionsChanged != 1 || revoked.WebhooksEnqueued != 1 {
		t.Fatalf("manual grant revocation = %#v", revoked)
	}
	var revokedAgain manualGrantResultResponse
	fixture.requestJSON(t, http.MethodPost, revokePath, admin,
		manualGrantRevocationRequest{Reason: "Repeated click"}, http.StatusOK, &revokedAgain)
	if !revokedAgain.Replay || revokedAgain.Grant.RevocationReason != "Granted to the wrong account" {
		t.Fatalf("repeated revocation = %#v", revokedAgain)
	}
	if bonus := publicEntitlements()["bonus"]; bonus.Access != core.AccessDenied || bonus.Reason != core.AccessReasonRevoked {
		t.Fatalf("public bonus entitlement after revocation = %#v", bonus)
	}

	var detail adminCustomerDetailResponse
	detailBody := fixture.requestJSON(t, http.MethodGet, project+"/customers/"+apiIntegrationCustomerID,
		admin, nil, http.StatusOK, &detail)
	for _, secret := range []string{apiIntegrationPurchaseToken, apiIntegrationTransactionID, "ciphertext", "fingerprint"} {
		if bytes.Contains(detailBody, []byte(secret)) {
			t.Fatalf("customer detail exposed %q: %s", secret, detailBody)
		}
	}
	if detail.CanonicalCustomerID != apiIntegrationCustomerID || len(detail.Aliases) != 1 ||
		len(detail.ManualGrants) != 2 || len(detail.GrantOptions) != 2 || detail.TimelineTruncated {
		t.Fatalf("customer detail = %#v", detail)
	}
	sources := make(map[string]adminCustomerEntitlementResponse, len(detail.Entitlements))
	for _, entitlement := range detail.Entitlements {
		sources[entitlement.Key] = entitlement
	}
	if sources["premium"].Source != persistence.SourceKindStoreObservation || sources["premium"].SourceID != apiIntegrationObservationID ||
		sources["bonus"].Source != persistence.SourceKindManual || sources["bonus"].SourceID != granted.Grant.ID ||
		sources["bonus"].Access != core.AccessDenied {
		t.Fatalf("customer detail entitlements = %#v", detail.Entitlements)
	}
	kinds := make(map[persistence.AdminTimelineKind]int)
	for index, event := range detail.Timeline {
		kinds[event.Kind]++
		if index > 0 && event.OccurredAt.After(detail.Timeline[index-1].OccurredAt) {
			t.Fatalf("timeline is not newest first at %d: %#v", index, detail.Timeline)
		}
		if event.Kind == persistence.AdminTimelinePurchaseObserved && event.Source != persistence.SourceKindStoreObservation {
			t.Fatalf("purchase observation event = %#v", event)
		}
	}
	for kind, minimum := range map[persistence.AdminTimelineKind]int{
		persistence.AdminTimelineCustomerCreated:    1,
		persistence.AdminTimelineAliasAdded:         1,
		persistence.AdminTimelinePurchaseObserved:   1,
		persistence.AdminTimelineManualGrantCreated: 2,
		persistence.AdminTimelineManualGrantRevoked: 1,
		persistence.AdminTimelineEntitlementChanged: 3,
	} {
		if kinds[kind] < minimum {
			t.Fatalf("timeline %s count = %d, want at least %d: %#v", kind, kinds[kind], minimum, detail.Timeline)
		}
	}
	fixture.requestJSON(t, http.MethodGet, project+"/customers/missing-customer", admin, nil, http.StatusNotFound, nil)

	var overview adminOverviewResponse
	fixture.requestJSON(t, http.MethodGet, project+"/overview", admin, nil, http.StatusOK, &overview)
	if len(overview.RecentTransactions) != 1 || overview.Analytics.VerifiedCount != 1 {
		t.Fatalf("manual grants leaked into verified activity: %#v", overview.RecentTransactions)
	}
	assertAPIIntegrationTableCount(t, fixture.database, "purchase_observations", 1)
	assertAPIIntegrationTableCount(t, fixture.database, "purchase_evidence", 1)
}

// seedCustomerSupportCatalog creates a purchased premium entitlement and an unpurchased bonus entitlement.
func seedCustomerSupportCatalog(t *testing.T, fixture *apiIntegrationFixture, bootstrap string) {
	t.Helper()
	project := "/v1/admin/projects/" + apiIntegrationProjectID
	fixture.requestJSON(t, http.MethodPut, project, bootstrap, nil, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut, project+"/applications/"+apiIntegrationApplicationID, bootstrap,
		applicationRequest{
			Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest,
			ProviderApplicationID: apiIntegrationProviderApplicationID,
		}, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut, project+"/customers/"+apiIntegrationCustomerID, bootstrap,
		customerRequest{ExternalID: apiIntegrationExternalCustomerID}, http.StatusOK, nil)
	for entitlementID, key := range map[string]string{apiIntegrationEntitlementID: "premium", supportBonusEntitlementID: "bonus"} {
		fixture.requestJSON(t, http.MethodPut, project+"/entitlements/"+entitlementID, bootstrap,
			entitlementRequest{Key: key}, http.StatusOK, nil)
	}
	fixture.requestJSON(t, http.MethodPut, project+"/products/"+apiIntegrationProductID, bootstrap,
		productRequest{Kind: core.ProductKindNonConsumable, EntitlementIDs: []core.EntitlementID{apiIntegrationEntitlementID}},
		http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut, project+"/products/"+supportBonusProductID, bootstrap,
		productRequest{Kind: core.ProductKindSubscription, EntitlementIDs: []core.EntitlementID{supportBonusEntitlementID}},
		http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut,
		project+"/applications/"+apiIntegrationApplicationID+"/store-products/"+apiIntegrationProviderProductID,
		bootstrap, storeProductRequest{ProductID: apiIntegrationProductID}, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut,
		project+"/applications/"+apiIntegrationApplicationID+"/store-products/bonus_monthly",
		bootstrap, storeProductRequest{ProductID: supportBonusProductID}, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut, project+"/applications/"+supportUnmappedApplicationID, bootstrap,
		applicationRequest{
			Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest,
			ProviderApplicationID: "com.example.iapstack.unmapped",
		}, http.StatusOK, nil)

	var applicationKey map[string]string
	fixture.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", bootstrap, keyRequest{
		Role: persistence.APIKeyRoleApplication, ProjectID: apiIntegrationProjectID, ApplicationID: apiIntegrationApplicationID,
	}, http.StatusCreated, &applicationKey)
	var session customerSessionResponse
	fixture.requestJSON(t, http.MethodPost, "/v1/applications/"+apiIntegrationApplicationID+"/customer-sessions",
		applicationKey["key"], customerSessionRequest{ExternalCustomerID: apiIntegrationExternalCustomerID}, http.StatusCreated, &session)
	fixture.requestJSON(t, http.MethodPost, "/v1/applications/"+apiIntegrationApplicationID+"/purchases:verify",
		session.Token, jobs.VerificationPayload{
			ExternalCustomerID: apiIntegrationExternalCustomerID,
			ClaimedProducts:    []core.ProviderProductID{apiIntegrationProviderProductID},
			Evidence:           json.RawMessage(`{"purchase_token":"` + apiIntegrationPurchaseToken + `","product_kind":"non_consumable"}`),
		}, http.StatusOK, nil)
}
