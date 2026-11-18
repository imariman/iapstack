package postgres

import (
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// TestAdminTimelineDeliveryKeyIncludesCustomer verifies another closure member's outbox row cannot hide a projection.
func TestAdminTimelineDeliveryKeyIncludesCustomer(t *testing.T) {
	t.Parallel()

	timeline := newAdminTimeline()
	timeline.noteDelivery("customer-merged", "entitlement-1", 3)
	timeline.addUndeliveredProjections("customer-canonical", []persistence.AdminCustomerEntitlement{{
		EntitlementID: "entitlement-1", Key: "premium", SourceKind: persistence.SourceKindImport,
		SourceID: "imp-0123456789abcdef0123456789abcdef", Access: core.AccessAllowed,
		AccessReason: core.AccessReasonPurchaseValid, Version: 3, UpdatedAt: time.Now().UTC(),
	}})
	events, _ := timeline.sorted()
	if len(events) != 1 || events[0].Kind != persistence.AdminTimelineEntitlementChanged ||
		events[0].CustomerID != "customer-canonical" {
		t.Fatalf("timeline = %#v, want the canonical projection change", events)
	}

	timeline = newAdminTimeline()
	timeline.noteDelivery("customer-canonical", "entitlement-1", 3)
	timeline.addUndeliveredProjections("customer-canonical", []persistence.AdminCustomerEntitlement{{
		EntitlementID: "entitlement-1", Version: 3,
	}})
	if events, _ := timeline.sorted(); len(events) != 0 {
		t.Fatalf("timeline = %#v, want the delivered version left to its outbox row", events)
	}
}
