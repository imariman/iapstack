package httpapi

import (
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// TestEntitlementResponsesFailClosedAtEffectiveEnd verifies response-time access cannot outlive its period.
func TestEntitlementResponsesFailClosedAtEffectiveEnd(t *testing.T) {
	t.Parallel()

	endsAt := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	value := persistence.CustomerEntitlement{
		Projection: persistence.EntitlementProjection{
			Access:          core.AccessAllowed,
			AccessReason:    core.AccessReasonCanceledAtPeriodEnd,
			EffectivePeriod: core.EffectivePeriod{StartsAt: endsAt.Add(-30 * 24 * time.Hour), EndsAt: &endsAt},
		},
		Key:     "premium",
		Version: 7,
	}

	tests := []struct {
		name       string
		now        time.Time
		wantAccess core.AccessStatus
		wantReason core.AccessReason
	}{
		{
			name:       "before expiry",
			now:        endsAt.Add(-time.Nanosecond),
			wantAccess: core.AccessAllowed,
			wantReason: core.AccessReasonCanceledAtPeriodEnd,
		},
		{
			name:       "at expiry",
			now:        endsAt,
			wantAccess: core.AccessDenied,
			wantReason: core.AccessReasonExpired,
		},
		{
			name:       "after expiry",
			now:        endsAt.Add(time.Hour),
			wantAccess: core.AccessDenied,
			wantReason: core.AccessReasonExpired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			responses := entitlementResponses([]persistence.CustomerEntitlement{value}, test.now)
			if len(responses) != 1 {
				t.Fatalf("responses = %d, want 1", len(responses))
			}
			if responses[0].Access != test.wantAccess || responses[0].Reason != test.wantReason {
				t.Fatalf("access decision = (%q, %q), want (%q, %q)",
					responses[0].Access, responses[0].Reason, test.wantAccess, test.wantReason)
			}
			if responses[0].Version != value.Version || responses[0].EffectiveEndsAt != &endsAt {
				t.Fatalf("response metadata changed: %#v", responses[0])
			}
		})
	}
}

// TestEntitlementResponsesPreserveAlreadyDeniedAccess verifies time evaluation does not rewrite provider denials.
func TestEntitlementResponsesPreserveAlreadyDeniedAccess(t *testing.T) {
	t.Parallel()

	endsAt := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	value := persistence.CustomerEntitlement{
		Projection: persistence.EntitlementProjection{
			Access:          core.AccessDenied,
			AccessReason:    core.AccessReasonRevoked,
			EffectivePeriod: core.EffectivePeriod{StartsAt: endsAt.Add(-time.Hour), EndsAt: &endsAt},
		},
		Key:     "premium",
		Version: 8,
	}

	response := entitlementResponses([]persistence.CustomerEntitlement{value}, endsAt.Add(time.Hour))[0]
	if response.Access != core.AccessDenied || response.Reason != core.AccessReasonRevoked {
		t.Fatalf("access decision = (%q, %q), want revoked denial", response.Access, response.Reason)
	}
}
