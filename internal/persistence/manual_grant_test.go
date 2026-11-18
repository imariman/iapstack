package persistence_test

import (
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// TestManualGrantWriteValidation verifies reason, actor, replay key, and period bounds before persistence.
func TestManualGrantWriteValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	valid := persistence.ManualGrantWrite{
		ProjectID: "project-1", CustomerID: "customer-1", ApplicationID: "application-1",
		ProductID: "product-1", EntitlementID: "entitlement-1", Reason: "Support ticket 42",
		ActorKeyID: "admin-key", IdempotencyKey: "grant-1", StartsAt: now, EndsAt: now.AddDate(0, 0, 30),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid write error = %v", err)
	}
	for name, mutate := range map[string]func(*persistence.ManualGrantWrite){
		"empty reason":     func(write *persistence.ManualGrantWrite) { write.Reason = "" },
		"padded reason":    func(write *persistence.ManualGrantWrite) { write.Reason = " reason" },
		"newline reason":   func(write *persistence.ManualGrantWrite) { write.Reason = "line\nline" },
		"long reason":      func(write *persistence.ManualGrantWrite) { write.Reason = strings.Repeat("x", 501) },
		"bootstrap actor":  func(write *persistence.ManualGrantWrite) { write.ActorKeyID = "" },
		"long replay key":  func(write *persistence.ManualGrantWrite) { write.IdempotencyKey = strings.Repeat("k", 201) },
		"missing end":      func(write *persistence.ManualGrantWrite) { write.EndsAt = time.Time{} },
		"end before start": func(write *persistence.ManualGrantWrite) { write.EndsAt = now.Add(-time.Second) },
		"too long": func(write *persistence.ManualGrantWrite) {
			write.EndsAt = now.Add(persistence.ManualGrantMaxDuration + time.Hour)
		},
		"missing product": func(write *persistence.ManualGrantWrite) { write.ProductID = "" },
	} {
		write := valid
		mutate(&write)
		if err := write.Validate(); err == nil {
			t.Fatalf("%s write validated", name)
		}
	}
	if err := persistence.ValidateManualGrantReason("reason", strings.Repeat("é", 500)); err != nil {
		t.Fatalf("500 multibyte runes error = %v, want accepted", err)
	}
}

// TestManualGrantIdentityAndProjectionSource verifies grant ids and that a manual source cites nothing else.
func TestManualGrantIdentityAndProjectionSource(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"mgr-0123456789abcdef0123456789abcdef"} {
		if err := persistence.ValidateManualGrantID(id); err != nil {
			t.Fatalf("ValidateManualGrantID(%q) error = %v", id, err)
		}
	}
	for _, id := range []string{"", "mgr-", "mgr-0123456789ABCDEF0123456789abcdef", "imp-0123456789abcdef0123456789abcdef", "mgr-0123456789abcdef0123456789abcdef0"} {
		if err := persistence.ValidateManualGrantID(id); err == nil {
			t.Fatalf("ValidateManualGrantID(%q) accepted", id)
		}
	}

	start := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 30)
	projection := persistence.EntitlementProjection{
		ProjectID: "project-1", CustomerID: "customer-1", EntitlementID: "entitlement-1",
		SourceKind: persistence.SourceKindManual, SourceManualGrantID: "mgr-0123456789abcdef0123456789abcdef",
		SourceProductID: "product-1", Access: core.AccessAllowed, AccessReason: core.AccessReasonPurchaseValid,
		EffectivePeriod: core.EffectivePeriod{StartsAt: start, EndsAt: &end},
	}
	if err := projection.Validate(); err != nil {
		t.Fatalf("manual projection error = %v", err)
	}
	withObservation := projection
	withObservation.SourceObservationID = "observation-1"
	withImport := projection
	withImport.SourceImportID = "imp-0123456789abcdef0123456789abcdef"
	storeWithGrant := projection
	storeWithGrant.SourceKind = persistence.SourceKindStoreObservation
	storeWithGrant.SourceObservationID = "observation-1"
	missingGrant := projection
	missingGrant.SourceManualGrantID = ""
	for name, invalid := range map[string]persistence.EntitlementProjection{
		"manual with observation": withObservation,
		"manual with import":      withImport,
		"store citing a grant":    storeWithGrant,
		"manual without grant":    missingGrant,
	} {
		if err := invalid.Validate(); err == nil {
			t.Fatalf("%s projection validated", name)
		}
	}

	grant := persistence.ManualGrant{
		ID: "mgr-0123456789abcdef0123456789abcdef", ProjectID: "project-1", CustomerID: "customer-1",
		ApplicationID: "application-1", ProductID: "product-1", EntitlementID: "entitlement-1",
		Reason: "Support", ActorKeyID: "admin-key", IdempotencyKey: "grant-1",
		StartsAt: start, EndsAt: end, CreatedAt: start,
	}
	if !grant.Active(start) || grant.Active(end) || grant.Active(start.Add(-time.Second)) {
		t.Fatal("manual grant activity window is not [start, end)")
	}
	revokedAt := start.Add(time.Hour)
	grant.RevokedAt = &revokedAt
	if grant.Active(start.Add(2*time.Hour)) || grant.Validate() == nil {
		t.Fatal("revoked grant without revocation audit fields was accepted or active")
	}
	grant.RevokedByKeyID, grant.RevocationReason = "admin-key", "Wrong account"
	if err := grant.Validate(); err != nil {
		t.Fatalf("revoked grant error = %v", err)
	}
}
