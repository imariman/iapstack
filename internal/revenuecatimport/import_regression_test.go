package revenuecatimport

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

// TestPlanJSONLNonSubscriptionUsesPurchaseInstant verifies mixed timestamp shapes
// select the later purchase, and an unparseable date fails that line.
func TestPlanJSONLNonSubscriptionUsesPurchaseInstant(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		purchases string
		wantID    string
		wantStart time.Time
	}{
		{
			name: "space format later than rfc3339",
			// 'T' sorts after a space, so a string compare keeps the 11:00 row.
			purchases: `{"id":"older-txn","is_sandbox":false,"purchase_date":"2024-06-01T11:00:00Z","store":"app_store"},` +
				`{"id":"newer-txn","is_sandbox":false,"purchase_date":"2024-06-01 12:00:00","store":"app_store"}`,
			wantID:    "newer-txn",
			wantStart: time.Date(2024, time.June, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "fractional second later than whole second",
			// 'Z' sorts after '.', so a string compare keeps the earlier whole second.
			purchases: `{"id":"fractional-txn","is_sandbox":false,"purchase_date":"2024-06-15T10:00:00.123456Z","store":"app_store"},` +
				`{"id":"whole-second-txn","is_sandbox":false,"purchase_date":"2024-06-15T10:00:00Z","store":"app_store"}`,
			wantID:    "fractional-txn",
			wantStart: time.Date(2024, time.June, 15, 10, 0, 0, 123456000, time.UTC),
		},
		{
			name: "unpadded hour is earlier than ten",
			// '9' sorts after '1', so a string compare keeps 9:30 over 10:00.
			purchases: `{"id":"ten-txn","is_sandbox":false,"purchase_date":"2024-01-01 10:00:00","store":"app_store"},` +
				`{"id":"nine-txn","is_sandbox":false,"purchase_date":"2024-01-01 9:30:00","store":"app_store"}`,
			wantID:    "ten-txn",
			wantStart: time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
		},
		{
			name: "equal instants keep the earlier purchase",
			// The second string sorts after the first and names the same instant.
			purchases: `{"id":"space-txn","is_sandbox":false,"purchase_date":"2024-06-01 12:00:00","store":"app_store"},` +
				`{"id":"rfc-txn","is_sandbox":false,"purchase_date":"2024-06-01T12:00:00Z","store":"app_store"}`,
			wantID:    "space-txn",
			wantStart: time.Date(2024, time.June, 1, 12, 0, 0, 0, time.UTC),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := jsonlInput(t, now, lifetimeJSONL(test.purchases))
			result, err := Plan(input)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			subscriber := result.Subscribers[0]
			if subscriber.Fatal || len(subscriber.Grants) != 1 {
				t.Fatalf("subscriber = %#v, want one grant", subscriber)
			}
			grant := subscriber.Grants[0]
			if grant.LineageFingerprint != sha256.Sum256([]byte(test.wantID)) || !grant.StartsAt.Equal(test.wantStart) {
				t.Fatalf("grant start = %s lineage = %x, want %s and %q", grant.StartsAt, grant.LineageFingerprint, test.wantStart, test.wantID)
			}
			if grant.Access != core.AccessAllowed || grant.Reason != core.AccessReasonPurchaseValid || grant.EndsAt != nil {
				t.Fatalf("grant = %#v, want a durable allowed purchase", grant)
			}
		})
	}
}

// TestPlanJSONLNonSubscriptionRejectsUnparsedPurchaseDate verifies a date that
// loses a string compare still fails the line when it does not parse.
func TestPlanJSONLNonSubscriptionRejectsUnparsedPurchaseDate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	purchases := `{"id":"good-txn","is_sandbox":false,"purchase_date":"2024-06-01T10:00:00Z","store":"app_store"},` +
		`{"id":"bad-txn","is_sandbox":false,"purchase_date":"2023-99-01 00:00:00","store":"app_store"}`
	input := jsonlInput(t, now, lifetimeJSONL(purchases))
	result, err := Plan(input)
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("Plan() error = %v, want usage", err)
	}
	if len(result.Subscribers) != 0 {
		t.Fatalf("subscribers = %#v, want none", result.Subscribers)
	}
}

// TestPlanDeniesGrantBeforeEffectiveStart verifies a future start is stored denied.
func TestPlanDeniesGrantBeforeEffectiveStart(t *testing.T) {
	t.Parallel()

	catalog := testCatalog(t)
	header := "rc_original_app_user_id,rc_last_seen_app_user_id_alias,product_identifier,entitlement_identifiers,start_time,effective_end_time,store,is_auto_renewable,is_sandbox,updated_at,refunded_at,unsubscribe_detected_at"
	december := time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)
	november := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name         string
		now          time.Time
		row          string
		wantAccess   core.AccessStatus
		wantReason   core.AccessReason
		wantStart    time.Time
		wantEnd      *time.Time
		wantCode     string
		wantDisagree bool
	}{
		{
			name:       "subscription before start",
			now:        time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC),
			row:        "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-11-01 00:00:00,2026-12-01 00:00:00,app_store,true,false,2026-10-02 00:00:00,,",
			wantAccess: core.AccessDenied,
			wantReason: core.AccessReasonProviderDecision,
			wantStart:  november,
			wantEnd:    &december,
		},
		{
			name:       "subscription at start",
			now:        november,
			row:        "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-11-01 00:00:00,2026-12-01 00:00:00,app_store,true,false,2026-10-02 00:00:00,,",
			wantAccess: core.AccessAllowed,
			wantReason: core.AccessReasonPurchaseValid,
			wantStart:  november,
			wantEnd:    &december,
		},
		{
			name:       "canceled subscription before start",
			now:        time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC),
			row:        "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-11-01 00:00:00,2026-12-01 00:00:00,app_store,true,false,2026-10-02 00:00:00,,2026-10-20 00:00:00",
			wantAccess: core.AccessDenied,
			wantReason: core.AccessReasonProviderDecision,
			wantStart:  november,
			wantEnd:    &december,
		},
		{
			name:       "durable purchase before start",
			now:        time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC),
			row:        "user-1,user-1,com.example.lifetime,\"[\"\"premium\"\"]\",2026-11-01 00:00:00,,app_store,false,false,2026-10-02 00:00:00,,",
			wantAccess: core.AccessDenied,
			wantReason: core.AccessReasonProviderDecision,
			wantStart:  november,
		},
		{
			name:         "refunded before start stays refunded",
			now:          time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC),
			row:          "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-11-01 00:00:00,2026-12-01 00:00:00,app_store,true,false,2026-10-02 00:00:00,2026-10-20 00:00:00,",
			wantAccess:   core.AccessDenied,
			wantReason:   core.AccessReasonRefunded,
			wantStart:    november,
			wantEnd:      &december,
			wantDisagree: true,
		},
		{
			name:     "end before start is still refused",
			now:      time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC),
			row:      "user-1,user-1,com.example.annual,\"[\"\"premium\"\"]\",2026-11-01 00:00:00,2026-10-20 00:00:00,app_store,true,false,2026-10-02 00:00:00,,",
			wantCode: CodeMissingEffectiveEnd,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := testInput(t, test.now, false, catalog)
			input.Export = []byte(transactionsCSV(header, test.row))
			result, err := Plan(input)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			subscriber := result.Subscribers[0]
			if test.wantCode != "" {
				if !subscriber.Fatal || len(subscriber.Grants) != 0 || !hasCode(subscriber.Failures, test.wantCode) {
					t.Fatalf("subscriber = %#v, want %s", subscriber, test.wantCode)
				}
				return
			}
			if subscriber.Fatal || len(subscriber.Grants) != 1 {
				t.Fatalf("subscriber = %#v, want one grant", subscriber)
			}
			grant := subscriber.Grants[0]
			if grant.Access != test.wantAccess || grant.Reason != test.wantReason || !grant.StartsAt.Equal(test.wantStart) {
				t.Fatalf("grant = %#v", grant)
			}
			if (grant.EndsAt == nil) != (test.wantEnd == nil) || (grant.EndsAt != nil && !grant.EndsAt.Equal(*test.wantEnd)) {
				t.Fatalf("grant end = %v, want %v", grant.EndsAt, test.wantEnd)
			}
			disagreed := result.Report.Counts.RefundEndDisagreement == 1
			if disagreed != test.wantDisagree {
				t.Fatalf("refund disagreement = %d", result.Report.Counts.RefundEndDisagreement)
			}
		})
	}
}

// jsonlInput builds one subscriber JSONL dry-run around the shared catalog fixture.
func jsonlInput(t *testing.T, now time.Time, export string) Input {
	t.Helper()
	input := testInput(t, now, false, testCatalog(t))
	input.Format = FormatSubscriberJSONL
	input.Mapping.CanonicalIDColumn = ""
	input.Export = []byte(export)
	return input
}

// lifetimeJSONL is one Customer Info line whose non-subscription purchases share a product.
func lifetimeJSONL(purchases string) string {
	return `{"app_user_id":"user-1","request_date":"2026-10-26T00:00:00Z","subscriber":{"entitlements":{"premium":{"product_identifier":"com.example.lifetime","purchase_date":"2020-01-01T00:00:00Z"}},"non_subscriptions":{"com.example.lifetime":[` + purchases + `]}}}` + "\n"
}
