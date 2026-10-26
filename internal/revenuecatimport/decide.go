package revenuecatimport

import (
	"time"

	"github.com/imariman/iapstack/internal/core"
)

// accessDecision is the import-time entitlement state for one grouped lineage.
type accessDecision struct {
	access             core.AccessStatus
	reason             core.AccessReason
	start              time.Time
	end                *time.Time
	failure            string
	refundDisagreement bool
}

// decideAccess evaluates one winning export row at the command clock.
func decideAccess(row sourceRow, kind core.ProductKind, kindKnown bool, now time.Time) accessDecision {
	now = now.UTC()
	if kindKnown && kind == core.ProductKindConsumable {
		return accessDecision{failure: CodeUnsupportedProductKind}
	}
	boundary := row.effectiveEnd
	if boundary == nil && row.autoRenewable {
		return accessDecision{failure: CodeMissingEffectiveEnd}
	}
	durable := boundary == nil && !row.autoRenewable
	if kindKnown && durable && kind != core.ProductKindNonConsumable {
		return accessDecision{failure: CodeProductKindMismatch}
	}
	if kindKnown && boundary != nil && kind != core.ProductKindSubscription {
		return accessDecision{failure: CodeProductKindMismatch}
	}
	decision := accessDecision{}
	if row.refunded != nil {
		decision.access = core.AccessDenied
		decision.reason = core.AccessReasonRefunded
		if boundary != nil && boundary.After(now) {
			decision.refundDisagreement = true
		}
	} else if boundary == nil {
		decision.access = core.AccessAllowed
		decision.reason = core.AccessReasonPurchaseValid
	} else if !now.Before(*boundary) {
		decision.access = core.AccessDenied
		switch {
		case row.autoResume != nil && row.autoResume.After(now):
			decision.reason = core.AccessReasonPaused
		case row.billing != nil:
			decision.reason = core.AccessReasonBillingIssue
		default:
			decision.reason = core.AccessReasonExpired
		}
	} else {
		decision.access = core.AccessAllowed
		switch {
		case row.grace != nil && row.grace.After(now) && (row.end == nil || !now.Before(*row.end)):
			decision.reason = core.AccessReasonGracePeriod
		case row.unsubscribe != nil:
			decision.reason = core.AccessReasonCanceledAtPeriodEnd
		default:
			decision.reason = core.AccessReasonPurchaseValid
		}
	}
	if row.start != nil {
		decision.start = row.start.UTC()
	}
	if boundary != nil {
		end := boundary.UTC()
		decision.end = &end
	}
	if decision.access == core.AccessAllowed && decision.start.IsZero() {
		return accessDecision{failure: CodeMissingEffectiveEnd}
	}
	if decision.start.IsZero() {
		decision.end = nil
	}
	if decision.end != nil && !decision.start.IsZero() && !decision.end.After(decision.start) {
		if decision.access == core.AccessAllowed {
			return accessDecision{failure: CodeMissingEffectiveEnd}
		}
		decision.end = nil
	}
	return decision
}
