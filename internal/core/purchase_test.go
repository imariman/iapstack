package core_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
)

func TestRepresentativeProviderObservations(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(31 * 24 * time.Hour)

	tests := []struct {
		name        string
		observation core.PurchaseObservation
	}{
		{
			name: "Apple renewal transaction and original lineage",
			observation: subscriptionObservation(
				t,
				core.ProviderAppleAppStore,
				core.LifecycleActive,
				core.AccessAllowed,
				core.AccessReasonPurchaseValid,
				storeReference(t, core.ReferenceTransaction, "transaction_id", "2000000000002"),
				storeReference(t, core.ReferenceLineage, "original_transaction_id", "1000000000001"),
				[]core.StoreReference{storeReference(t, core.ReferenceQuery, "transaction_id", "2000000000002")},
				start,
				end,
			),
		},
		{
			name: "Google grace period with linked purchase token",
			observation: func() core.PurchaseObservation {
				observation := subscriptionObservation(
					t,
					core.ProviderGooglePlay,
					core.LifecycleGracePeriod,
					core.AccessAllowed,
					core.AccessReasonGracePeriod,
					storeReference(t, core.ReferenceTransaction, "order_id", "GPA.1234-5678-9012-34567..0"),
					storeReference(t, core.ReferenceLineage, "purchase_token", "current-sensitive-token"),
					[]core.StoreReference{storeReference(t, core.ReferenceQuery, "purchase_token", "current-sensitive-token")},
					start,
					end,
				)
				observation.References = append(observation.References,
					storeReference(t, core.ReferenceLinkedLineage, "purchase_token", "previous-sensitive-token"),
				)
				return observation
			}(),
		},
		{
			name: "Huawei canceled but effective until period end",
			observation: func() core.PurchaseObservation {
				observation := subscriptionObservation(
					t,
					core.ProviderHuaweiAppGallery,
					core.LifecycleCanceled,
					core.AccessAllowed,
					core.AccessReasonCanceledAtPeriodEnd,
					storeReference(t, core.ReferenceTransaction, "order_id", "202608230001"),
					storeReference(t, core.ReferenceLineage, "purchase_token", "stable-renewal-token"),
					[]core.StoreReference{storeReference(t, core.ReferenceQuery, "purchase_token", "stable-renewal-token")},
					start,
					end,
				)
				observation.Renewal.Status = core.RenewalDisabled
				observation.Renewal.NextRenewalAt = nil
				return observation
			}(),
		},
		{
			name: "Amazon receipt",
			observation: subscriptionObservation(
				t,
				core.ProviderAmazonAppstore,
				core.LifecycleActive,
				core.AccessAllowed,
				core.AccessReasonPurchaseValid,
				storeReference(t, core.ReferenceTransaction, "receipt_id", "amazon-receipt"),
				storeReference(t, core.ReferenceLineage, "receipt_id", "amazon-receipt"),
				[]core.StoreReference{storeReference(t, core.ReferenceQuery, "receipt_id", "amazon-receipt")},
				start,
				end,
			),
		},
		{
			name: "Samsung refund that does not revoke current access",
			observation: subscriptionObservation(
				t,
				core.ProviderSamsungGalaxyStore,
				core.LifecycleRefunded,
				core.AccessAllowed,
				core.AccessReasonRefunded,
				storeReference(t, core.ReferenceTransaction, "payment_transaction_id", "samsung-payment"),
				storeReference(t, core.ReferenceLineage, "purchase_id", "samsung-purchase"),
				[]core.StoreReference{storeReference(t, core.ReferenceQuery, "purchase_id", "samsung-purchase")},
				start,
				end,
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.observation.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestPendingPurchaseDoesNotRequireTransaction(t *testing.T) {
	t.Parallel()

	observation := core.PurchaseObservation{
		ID:            "google-pending-digest",
		ApplicationID: "app_1",
		Store: core.StoreApplication{
			Provider:    core.ProviderGooglePlay,
			Environment: core.EnvironmentProduction,
			ID:          "com.example.app",
		},
		ProductID:     "coins_100",
		ProductKind:   core.ProductKindConsumable,
		State:         core.LifecyclePending,
		ProviderState: "PURCHASE_STATE_PENDING",
		Access:        core.AccessUnresolved,
		AccessReason:  core.AccessReasonPendingPayment,
		Ownership:     core.OwnershipPurchased,
		Quantity:      1,
		ObservedAt:    time.Now().UTC(),
		Renewal:       core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable},
		References: []core.StoreReference{
			storeReference(t, core.ReferenceQuery, "purchase_token", "pending-token"),
		},
	}

	if err := observation.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAllowedAccessRequiresTransactionAndPeriod(t *testing.T) {
	t.Parallel()

	start := time.Now().UTC()
	end := start.Add(time.Hour)
	observation := subscriptionObservation(
		t,
		core.ProviderAppleAppStore,
		core.LifecycleActive,
		core.AccessAllowed,
		core.AccessReasonPurchaseValid,
		storeReference(t, core.ReferenceTransaction, "transaction_id", "transaction"),
		storeReference(t, core.ReferenceLineage, "original_transaction_id", "lineage"),
		nil,
		start,
		end,
	)

	observation.References = observation.ReferencesFor(core.ReferenceLineage)
	if err := observation.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want missing transaction error")
	}

	observation.References = append(observation.References,
		storeReference(t, core.ReferenceTransaction, "transaction_id", "transaction"),
	)
	observation.EffectivePeriod.EndsAt = nil
	if err := observation.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want missing subscription end error")
	}
}

func TestTerminalStateAccessRules(t *testing.T) {
	t.Parallel()

	for _, state := range []core.LifecycleState{core.LifecycleExpired, core.LifecycleRevoked} {
		t.Run(string(state), func(t *testing.T) {
			observation := deniedObservation(t, state)
			if err := observation.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}

			observation.Access = core.AccessAllowed
			if err := observation.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want terminal access error")
			}
		})
	}
}

func TestStoreReferenceIsRedacted(t *testing.T) {
	t.Parallel()

	reference := storeReference(t, core.ReferenceQuery, "purchase_token", "do-not-log-me")
	if got := fmt.Sprint(reference); strings.Contains(got, reference.Value()) {
		t.Fatalf("String() = %q, leaked reference value", got)
	}
	if reference.Value() != "do-not-log-me" {
		t.Fatalf("Value() = %q, want original value", reference.Value())
	}
}

func subscriptionObservation(
	t *testing.T,
	provider core.Provider,
	state core.LifecycleState,
	access core.AccessStatus,
	reason core.AccessReason,
	transaction core.StoreReference,
	lineage core.StoreReference,
	query []core.StoreReference,
	start time.Time,
	end time.Time,
) core.PurchaseObservation {
	t.Helper()
	return core.PurchaseObservation{
		ID:            core.ObservationID(provider + "-observation"),
		ApplicationID: "app_1",
		Store: core.StoreApplication{
			Provider:    provider,
			Environment: core.EnvironmentProduction,
			ID:          "provider-app",
		},
		ProductID:       "pro_monthly",
		ProductKind:     core.ProductKindSubscription,
		State:           state,
		ProviderState:   string(state),
		Access:          access,
		AccessReason:    reason,
		Ownership:       core.OwnershipPurchased,
		Quantity:        1,
		OccurredAt:      start,
		ObservedAt:      start.Add(time.Minute),
		EffectivePeriod: core.EffectivePeriod{StartsAt: start, EndsAt: &end},
		Renewal:         core.Renewal{Mode: core.RenewalAuto, Status: core.RenewalEnabled, NextRenewalAt: &end},
		References:      append([]core.StoreReference{transaction, lineage}, query...),
	}
}

func deniedObservation(t *testing.T, state core.LifecycleState) core.PurchaseObservation {
	t.Helper()
	start := time.Now().UTC().Add(-2 * time.Hour)
	end := start.Add(time.Hour)
	reason := core.AccessReasonExpired
	if state == core.LifecycleRevoked {
		reason = core.AccessReasonRevoked
	}
	observation := subscriptionObservation(
		t,
		core.ProviderAppleAppStore,
		state,
		core.AccessDenied,
		reason,
		storeReference(t, core.ReferenceTransaction, "transaction_id", "transaction"),
		storeReference(t, core.ReferenceLineage, "original_transaction_id", "lineage"),
		nil,
		start,
		end,
	)
	observation.Renewal.Status = core.RenewalDisabled
	observation.Renewal.NextRenewalAt = nil
	return observation
}

func storeReference(t *testing.T, role core.ReferenceRole, kind, value string) core.StoreReference {
	t.Helper()
	reference, err := core.NewStoreReference(role, kind, value)
	if err != nil {
		t.Fatalf("NewStoreReference() error = %v", err)
	}
	return reference
}
