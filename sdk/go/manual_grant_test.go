package iapstack

import (
	"encoding/json"
	"testing"
	"time"
)

// TestEntitlementSnapshotAcceptsManualGrantReason verifies an administrator grant decodes and grants access.
func TestEntitlementSnapshotAcceptsManualGrantReason(t *testing.T) {
	t.Parallel()

	var snapshot EntitlementSnapshot
	body := `{"customer_id":"customer-internal","entitlements":[{"key":"premium","access":"allowed",` +
		`"reason":"manual_grant","effective_starts_at":"2026-11-18T10:00:00Z",` +
		`"effective_ends_at":"2026-12-18T10:00:00Z","version":2}]}`
	if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if err := snapshot.validate(); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
	entitlement := snapshot.Entitlements[0]
	if entitlement.Reason != "manual_grant" ||
		!entitlement.GrantsAccessAt(time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)) ||
		entitlement.GrantsAccessAt(time.Date(2026, time.December, 18, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("manual grant entitlement = %#v", entitlement)
	}
}
