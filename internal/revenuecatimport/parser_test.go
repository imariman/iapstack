package revenuecatimport

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// TestPlanCanonicalColumnDoesNotEmitAliasAsCustomer verifies the other id stays an alias.
func TestPlanCanonicalColumnDoesNotEmitAliasAsCustomer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	input := testInput(t, now, false, testCatalog(t))
	input.Mapping.CanonicalIDColumn = columnLastSeenAlias
	input.Export = []byte(transactionsCSV(
		"rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,start_time,end_time,effective_end_time,store,is_auto_renewable,is_sandbox,updated_at,store_transaction_id",
		"original-user,seen-user,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00,txn-1",
	))
	result, err := Plan(input)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(result.Subscribers) != 1 || result.Subscribers[0].ExternalID != "seen-user" {
		t.Fatalf("subscribers = %#v, want seen-user only", result.Subscribers)
	}
	if len(result.Subscribers[0].Aliases) != 1 || result.Subscribers[0].Aliases[0] != "original-user" {
		t.Fatalf("aliases = %#v, want original-user", result.Subscribers[0].Aliases)
	}
	if result.Subscribers[0].Fatal || len(result.Subscribers[0].Grants) != 1 {
		t.Fatalf("subscriber = %#v, want one canonical grant", result.Subscribers[0])
	}
}

// TestPlanMissingAliasPortRefusesAliasAndKeepsGrant verifies a missing alias port is not a subscriber failure.
func TestPlanMissingAliasPortRefusesAliasAndKeepsGrant(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	input := testInput(t, now, false, testCatalog(t))
	input.Export = []byte(transactionsCSV(
		"rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,start_time,effective_end_time,store,is_auto_renewable,is_sandbox,updated_at",
		"user-1,alias-1,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00",
	))
	result, err := Plan(input)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Subscribers[0].Fatal || len(result.Subscribers[0].Grants) != 1 {
		t.Fatalf("subscriber = %#v, want the canonical grant", result.Subscribers[0])
	}
	if result.Report.Counts.AliasRefused != 1 || !hasFailure(result, CodeAliasRefused) {
		t.Fatalf("report = %#v, want alias_refused", result.Report)
	}
}

// TestPlanAliasCollisionWithCanonicalIDRefusesSubscriber verifies one alias cannot belong to two customers.
func TestPlanAliasCollisionWithCanonicalIDRefusesSubscriber(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	input := testInput(t, now, false, testCatalog(t))
	input.Export = []byte(transactionsCSV(
		"rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,start_time,effective_end_time,store,is_auto_renewable,is_sandbox,updated_at",
		"user-a,user-b,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00",
		"user-b,user-b,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00",
	))
	result, err := Plan(input)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	refused := subscriberByID(t, result, "user-a")
	if !refused.Fatal || len(refused.Grants) != 0 || !hasCode(refused.Failures, CodeConflictingSubscriber) {
		t.Fatalf("user-a = %#v, want conflicting_subscriber", refused)
	}
	kept := subscriberByID(t, result, "user-b")
	if kept.Fatal || len(kept.Grants) != 1 {
		t.Fatalf("user-b = %#v, want the canonical grant", kept)
	}
}

// TestPlanRefusesUnknownCatalogAndKindMismatches verifies mapping and catalog failures write nothing for that subscriber.
func TestPlanRefusesUnknownCatalogAndKindMismatches(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	catalog := testCatalog(t)
	cases := []struct {
		name string
		row  string
		want string
	}{
		{name: "unknown entitlement", row: "user-1,user-1,com.example.annual,\"[\"\"missing\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00", want: CodeUnknownEntitlement},
		{name: "unknown product", row: "user-1,user-1,com.example.missing,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00", want: CodeUnknownProduct},
		{name: "consumable", row: "user-1,user-1,com.example.coins,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,false,false,2026-10-02 00:00:00", want: CodeUnsupportedProductKind},
		{name: "sandbox production application", row: "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,true,2026-10-02 00:00:00", want: CodeEnvironmentMismatch},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := testInput(t, now, false, catalog)
			input.Export = []byte(transactionsCSV(
				"rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,start_time,effective_end_time,store,is_auto_renewable,is_sandbox,updated_at",
				test.row,
			))
			result, err := Plan(input)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			subscriber := result.Subscribers[0]
			if !subscriber.Fatal || len(subscriber.Grants) != 0 || !hasCode(subscriber.Failures, test.want) {
				t.Fatalf("subscriber = %#v, want %s", subscriber, test.want)
			}
		})
	}
}

// TestPlanAccessDecisions verifies expiry, durable grants, open subscriptions, and refunds.
func TestPlanAccessDecisions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	catalog := testCatalog(t)
	header := "rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,start_time,effective_end_time,store,is_auto_renewable,is_sandbox,updated_at,refunded_at"
	cases := []struct {
		name       string
		row        string
		wantAccess core.AccessStatus
		wantReason core.AccessReason
		wantEnd    bool
		wantCode   string
	}{
		{name: "expired", row: "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-09-01 00:00:00,2026-10-01 00:00:00,app_store,true,false,2026-10-02 00:00:00,", wantAccess: core.AccessDenied, wantReason: core.AccessReasonExpired, wantEnd: true},
		{name: "future", row: "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00,", wantAccess: core.AccessAllowed, wantReason: core.AccessReasonPurchaseValid, wantEnd: true},
		{name: "durable", row: "user-1,user-1,com.example.lifetime,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,,app_store,false,false,2026-10-02 00:00:00,", wantAccess: core.AccessAllowed, wantReason: core.AccessReasonPurchaseValid, wantEnd: false},
		{name: "open subscription", row: "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,,app_store,true,false,2026-10-02 00:00:00,", wantCode: CodeMissingEffectiveEnd},
		{name: "refunded", row: "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00,2026-10-20 00:00:00", wantAccess: core.AccessDenied, wantReason: core.AccessReasonRefunded, wantEnd: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := testInput(t, now, false, catalog)
			input.Export = []byte(transactionsCSV(header, test.row))
			result, err := Plan(input)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			subscriber := result.Subscribers[0]
			if test.wantCode != "" {
				if !subscriber.Fatal || !hasCode(subscriber.Failures, test.wantCode) {
					t.Fatalf("subscriber = %#v, want %s", subscriber, test.wantCode)
				}
				return
			}
			if subscriber.Fatal || len(subscriber.Grants) != 1 {
				t.Fatalf("subscriber = %#v, want one grant", subscriber)
			}
			grant := subscriber.Grants[0]
			if grant.Access != test.wantAccess || grant.Reason != test.wantReason || (grant.EndsAt != nil) != test.wantEnd {
				t.Fatalf("grant = %#v", grant)
			}
			if test.name == "refunded" && result.Report.Counts.RefundEndDisagreement != 1 {
				t.Fatalf("refund disagreement = %d", result.Report.Counts.RefundEndDisagreement)
			}
		})
	}
}

// TestPlanTruncatedExportWritesNothing verifies gzip and mid-quote CSV failures.
func TestPlanTruncatedExportWritesNothing(t *testing.T) {
	t.Parallel()

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte("rc_original_app_user_id\n"))
	_ = writer.Flush()
	raw := compressed.Bytes()
	if len(raw) > 4 {
		raw = raw[:len(raw)-4]
	}
	if _, _, err := ReadExport(bytes.NewReader(raw)); !errors.Is(err, ErrTruncated) {
		t.Fatalf("ReadExport() error = %v, want truncated", err)
	}
	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	input := testInput(t, now, false, nil)
	input.Export = []byte("rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,store,is_auto_renewable,is_sandbox\n\"user-1\",\"alias")
	result, err := Plan(input)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("Plan() error = %v, want truncated", err)
	}
	if len(result.Subscribers) != 0 {
		t.Fatalf("subscribers = %#v, want none", result.Subscribers)
	}
}

// TestPlanIgnoresReceiptAndAttributeColumns verifies unknown cells never enter the decoded grant.
func TestPlanIgnoresReceiptAndAttributeColumns(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	input := testInput(t, now, false, testCatalog(t))
	input.Export = []byte(transactionsCSV(
		"rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,start_time,effective_end_time,store,is_auto_renewable,is_sandbox,updated_at,receipt,custom_subscriber_attributes",
		"user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00,RECEIPT-SECRET-VALUE,ATTRIBUTE-SECRET-VALUE",
	))
	result, err := Plan(input)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.Subscribers[0].Fatal || len(result.Subscribers[0].Grants) != 1 {
		t.Fatalf("subscriber = %#v", result.Subscribers[0])
	}
	assertNoSecret(t, result, "RECEIPT-SECRET-VALUE")
	assertNoSecret(t, result, "ATTRIBUTE-SECRET-VALUE")
	if result.Subscribers[0].Grants[0].LineageFingerprint == ([32]byte{}) {
		t.Fatal("lineage fingerprint was empty")
	}
}

// TestPlanEntitlementListAcceptsOnlyJSONArrays verifies the feed's string-array encoding.
func TestPlanEntitlementListAcceptsOnlyJSONArrays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	catalog := testCatalog(t)
	header := "rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,start_time,effective_end_time,store,is_auto_renewable,is_sandbox,updated_at"
	invalid := testInput(t, now, false, catalog)
	invalid.Export = []byte(transactionsCSV(header, "user-1,user-1,com.example.annual,['premium'],2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00"))
	result, err := Plan(invalid)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !result.Subscribers[0].Fatal || !hasCode(result.Subscribers[0].Failures, CodeInvalidEntitlementList) {
		t.Fatalf("subscriber = %#v, want invalid_entitlement_list", result.Subscribers[0])
	}
	valid := testInput(t, now, false, catalog)
	valid.Export = []byte(transactionsCSV(header, "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-10-01 00:00:00,2026-11-01 00:00:00,app_store,true,false,2026-10-02 00:00:00"))
	result, err = Plan(valid)
	if err != nil {
		t.Fatalf("Plan() valid error = %v", err)
	}
	if result.Subscribers[0].Fatal || result.Subscribers[0].Grants[0].EntitlementKey != "premium" {
		t.Fatalf("subscriber = %#v, want mapped premium", result.Subscribers[0])
	}
}

// testInput builds one repeatable dry-run input.
func testInput(t *testing.T, now time.Time, aliases bool, catalog *Catalog) Input {
	t.Helper()
	return Input{
		ProjectID:    "project-1",
		Mapping:      testMapping(),
		Format:       FormatTransactionsCSV,
		Now:          now,
		StoreAliases: aliases,
		Catalog:      catalog,
	}
}

// testMapping returns the fixture mapping used by parser tests.
func testMapping() Mapping {
	return Mapping{
		ProjectID:         "project-1",
		CanonicalIDColumn: columnOriginalAppUserID,
		Entitlements:      map[string]string{"premium": "premium"},
		Products: []ProductMapping{
			{Store: "app_store", Environment: "production", ProviderProductID: "com.example.annual", ApplicationID: "application-apple", ProductID: "product-annual"},
			{Store: "app_store", Environment: "sandbox", ProviderProductID: "com.example.annual", ApplicationID: "application-apple", ProductID: "product-annual"},
			{Store: "app_store", Environment: "production", ProviderProductID: "com.example.lifetime", ApplicationID: "application-apple", ProductID: "product-lifetime"},
			{Store: "app_store", Environment: "production", ProviderProductID: "com.example.coins", ApplicationID: "application-apple", ProductID: "product-coins"},
		},
	}
}

// testCatalog returns applications and products the fixture mapping names.
func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	catalog := NewCatalog(persistence.ImportCatalog{
		Applications: []core.Application{{
			ID: "application-apple", ProjectID: "project-1",
			Store: core.StoreApplication{Provider: core.ProviderAppleAppStore, Environment: core.EnvironmentProduction, ID: "apple-app"},
		}},
		Products: []core.Product{
			{ID: "product-annual", ProjectID: "project-1", Kind: core.ProductKindSubscription, EntitlementIDs: []core.EntitlementID{"entitlement-premium"}},
			{ID: "product-lifetime", ProjectID: "project-1", Kind: core.ProductKindNonConsumable, EntitlementIDs: []core.EntitlementID{"entitlement-premium"}},
			{ID: "product-coins", ProjectID: "project-1", Kind: core.ProductKindConsumable, EntitlementIDs: []core.EntitlementID{"entitlement-premium"}},
		},
		Entitlements: []core.Entitlement{{ID: "entitlement-premium", ProjectID: "project-1", Key: "premium"}},
		StoreProducts: []core.StoreProduct{
			{ApplicationID: "application-apple", ProductID: "product-annual", ProviderID: "com.example.annual"},
			{ApplicationID: "application-apple", ProductID: "product-lifetime", ProviderID: "com.example.lifetime"},
			{ApplicationID: "application-apple", ProductID: "product-coins", ProviderID: "com.example.coins"},
		},
	})
	return &catalog
}

// transactionsCSV joins a header and data rows.
func transactionsCSV(header string, rows ...string) string {
	return header + "\n" + strings.Join(rows, "\n") + "\n"
}

// subscriberByID finds one planned subscriber.
func subscriberByID(t *testing.T, result Result, externalID string) Subscriber {
	t.Helper()
	for _, subscriber := range result.Subscribers {
		if subscriber.ExternalID == externalID {
			return subscriber
		}
	}
	t.Fatalf("subscriber %q not found in %#v", externalID, result.Subscribers)
	return Subscriber{}
}

// hasFailure reports whether the report contains a code.
func hasFailure(result Result, code string) bool {
	return hasCode(result.Report.Failures, code)
}

// hasCode reports whether failures contain a code.
func hasCode(failures []Failure, code string) bool {
	for _, failure := range failures {
		if failure.Code == code {
			return true
		}
	}
	return false
}

// assertNoSecret marshals the plan and rejects a forbidden export cell.
func assertNoSecret(t *testing.T, result Result, secret string) {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	if bytes.Contains(payload, []byte(secret)) {
		t.Fatalf("plan contained %q: %s", secret, payload)
	}
}
