package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/verification"
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

// TestClockSkewWindowAgreesAcrossAPIWebhookAndClient verifies the allowance the Apple
// adapter publishes is still allowed on GET, in the webhook body, and to grantsAccess.
// A stored allowed row whose end is already past stays expired on both surfaces.
func TestClockSkewWindowAgreesAcrossAPIWebhookAndClient(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 25, 11, 0, 0, 0, time.UTC)
	signedExpiry := now.Add(-time.Minute)
	// The Apple adapter publishes the signed expiry plus the five-minute allowance.
	publishedEnd := signedExpiry.Add(5 * time.Minute)
	withinWindow := skewEntitlement(publishedEnd)
	rawPastEnd := skewEntitlement(signedExpiry)

	assertEntitlementAgreement(t, withinWindow, now, core.AccessAllowed, core.AccessReasonPurchaseValid, true)
	assertEntitlementAgreement(t, withinWindow, publishedEnd, core.AccessDenied, core.AccessReasonExpired, false)
	assertEntitlementAgreement(t, rawPastEnd, now, core.AccessDenied, core.AccessReasonExpired, false)
}

// skewEntitlement is an allowed projection whose effective end is the test instant.
func skewEntitlement(endsAt time.Time) persistence.CustomerEntitlement {
	end := endsAt
	return persistence.CustomerEntitlement{
		Projection: persistence.EntitlementProjection{
			ProjectID:           "project-1",
			CustomerID:          "customer-1",
			EntitlementID:       "entitlement-1",
			SourceObservationID: "observation-1",
			SourceProductID:     "product-1",
			Access:              core.AccessAllowed,
			AccessReason:        core.AccessReasonPurchaseValid,
			EffectivePeriod: core.EffectivePeriod{
				StartsAt: endsAt.Add(-30 * 24 * time.Hour),
				EndsAt:   &end,
			},
		},
		SourceApplicationID: "application-1",
		Key:                 "premium",
		Version:             4,
	}
}

// assertEntitlementAgreement checks GET, the webhook payload, and the SDK end check together.
func assertEntitlementAgreement(
	t *testing.T,
	entitlement persistence.CustomerEntitlement,
	now time.Time,
	wantAccess core.AccessStatus,
	wantReason core.AccessReason,
	wantGrant bool,
) {
	t.Helper()

	responses := entitlementResponses([]persistence.CustomerEntitlement{entitlement}, now)
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if responses[0].Access != wantAccess || responses[0].Reason != wantReason {
		t.Fatalf("GET access = (%q, %q), want (%q, %q)", responses[0].Access, responses[0].Reason, wantAccess, wantReason)
	}
	event, err := verification.EntitlementChangedEvent(entitlement.SourceApplicationID, entitlement, now)
	if err != nil {
		t.Fatalf("EntitlementChangedEvent() error = %v", err)
	}
	var payload struct {
		Access          core.AccessStatus `json:"access"`
		AccessReason    core.AccessReason `json:"access_reason"`
		EffectiveEndsAt time.Time         `json:"effective_ends_at"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("unmarshal webhook: %v", err)
	}
	if payload.Access != wantAccess || payload.AccessReason != wantReason {
		t.Fatalf("webhook access = (%q, %q), want (%q, %q)", payload.Access, payload.AccessReason, wantAccess, wantReason)
	}
	if entitlement.Projection.EffectivePeriod.EndsAt == nil || !payload.EffectiveEndsAt.Equal(*entitlement.Projection.EffectivePeriod.EndsAt) {
		t.Fatalf("webhook end = %v, want stored effective end", payload.EffectiveEndsAt)
	}
	// grantsAccessAt allows only an allowed projection whose end is strictly after now.
	endsAt := responses[0].EffectiveEndsAt
	grants := responses[0].Access == core.AccessAllowed && (endsAt == nil || now.Before(*endsAt))
	if grants != wantGrant || payload.Access != responses[0].Access {
		t.Fatalf("grantsAccessAt = %v, want %v; webhook access = %q, GET access = %q",
			grants, wantGrant, payload.Access, responses[0].Access)
	}
}
