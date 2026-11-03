package processing

import (
	"testing"

	"github.com/imariman/iapstack/internal/core"
)

// TestLineageEndedRequiresEveryLineItemExpired verifies only a fully expired lineage stops its daily refresh.
func TestLineageEndedRequiresEveryLineItemExpired(t *testing.T) {
	t.Parallel()

	expired := core.PurchaseObservation{State: core.LifecycleExpired, Access: core.AccessDenied}
	active := core.PurchaseObservation{State: core.LifecycleActive, Access: core.AccessAllowed}
	cases := []struct {
		name         string
		observations []core.PurchaseObservation
		want         bool
	}{
		{name: "no observations", want: false},
		{name: "expired", observations: []core.PurchaseObservation{expired}, want: true},
		{name: "every line item expired", observations: []core.PurchaseObservation{expired, expired}, want: true},
		{name: "one line item active", observations: []core.PurchaseObservation{expired, active}, want: false},
		{name: "active", observations: []core.PurchaseObservation{active}, want: false},
		{
			name:         "grace period",
			observations: []core.PurchaseObservation{{State: core.LifecycleGracePeriod, Access: core.AccessAllowed}},
			want:         false,
		},
		{
			name:         "on hold",
			observations: []core.PurchaseObservation{{State: core.LifecycleOnHold, Access: core.AccessDenied}},
			want:         false,
		},
		{
			name:         "paused",
			observations: []core.PurchaseObservation{{State: core.LifecyclePaused, Access: core.AccessDenied}},
			want:         false,
		},
		{
			name:         "refunded",
			observations: []core.PurchaseObservation{{State: core.LifecycleRefunded, Access: core.AccessDenied}},
			want:         false,
		},
		{
			name:         "revoked",
			observations: []core.PurchaseObservation{{State: core.LifecycleRevoked, Access: core.AccessDenied}},
			want:         false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := lineageEnded(testCase.observations); got != testCase.want {
				t.Fatalf("lineageEnded() = %t, want %t", got, testCase.want)
			}
		})
	}
}
