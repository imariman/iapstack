package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// TestImportedEntitlementResponseDeniesAtEndWithoutSource verifies the public body stays the v1 object.
func TestImportedEntitlementResponseDeniesAtEndWithoutSource(t *testing.T) {
	t.Parallel()

	endsAt := time.Date(2026, time.October, 15, 0, 0, 0, 0, time.UTC)
	value := persistence.CustomerEntitlement{
		Projection: persistence.EntitlementProjection{
			Access:       core.AccessAllowed,
			AccessReason: core.AccessReasonPurchaseValid,
			EffectivePeriod: core.EffectivePeriod{
				StartsAt: endsAt.Add(-30 * 24 * time.Hour),
				EndsAt:   &endsAt,
			},
			SourceKind:     persistence.SourceKindImport,
			SourceImportID: "imp-55555555555555555555555555555555",
		},
		Key:     "premium",
		Version: 4,
	}
	responses := entitlementResponses([]persistence.CustomerEntitlement{value}, endsAt)
	if len(responses) != 1 || responses[0].Access != core.AccessDenied || responses[0].Reason != core.AccessReasonExpired || responses[0].Version != 4 {
		t.Fatalf("response = %#v, want denied expired version 4", responses)
	}
	payload, err := json.Marshal(responses[0])
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	encoded := string(payload)
	for _, forbidden := range []string{"source", "verified_at", "imp-"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("public entitlement JSON contained %q: %s", forbidden, encoded)
		}
	}
}
