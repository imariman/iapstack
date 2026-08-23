package core

import (
	"errors"
	"fmt"
	"time"
)

const (
	// LifecyclePending identifies a purchase that has not completed payment.
	LifecyclePending LifecycleState = "pending"
	// LifecycleActive identifies a purchase that currently permits service.
	LifecycleActive LifecycleState = "active"
	// LifecycleGracePeriod identifies temporary service during a billing grace period.
	LifecycleGracePeriod LifecycleState = "grace_period"
	// LifecycleOnHold identifies a subscription blocked by an unresolved billing issue.
	LifecycleOnHold LifecycleState = "on_hold"
	// LifecyclePaused identifies a subscription paused by the customer or provider.
	LifecyclePaused LifecycleState = "paused"
	// LifecycleCanceled identifies a purchase whose renewal or continuation was canceled.
	LifecycleCanceled LifecycleState = "canceled"
	// LifecycleExpired identifies a purchase whose effective period ended.
	LifecycleExpired LifecycleState = "expired"
	// LifecycleRefunded identifies a purchase with a provider-recorded refund.
	LifecycleRefunded LifecycleState = "refunded"
	// LifecycleRevoked identifies a purchase whose service was removed immediately.
	LifecycleRevoked LifecycleState = "revoked"
	// LifecycleUnresolved identifies provider evidence that cannot yet be normalized safely.
	LifecycleUnresolved LifecycleState = "unresolved"
)

const (
	// AccessAllowed indicates that verified store evidence currently permits service.
	AccessAllowed AccessStatus = "allowed"
	// AccessDenied indicates that verified store evidence currently denies service.
	AccessDenied AccessStatus = "denied"
	// AccessUnresolved indicates that store evidence is not yet authoritative enough for access.
	AccessUnresolved AccessStatus = "unresolved"
)

const (
	// AccessReasonPurchaseValid indicates a completed purchase in its effective period.
	AccessReasonPurchaseValid AccessReason = "purchase_valid"
	// AccessReasonGracePeriod indicates access retained during billing recovery.
	AccessReasonGracePeriod AccessReason = "grace_period"
	// AccessReasonPendingPayment indicates that payment has not completed.
	AccessReasonPendingPayment AccessReason = "pending_payment"
	// AccessReasonCanceledAtPeriodEnd indicates cancellation with access until expiry.
	AccessReasonCanceledAtPeriodEnd AccessReason = "canceled_at_period_end"
	// AccessReasonExpired indicates that the effective purchase period ended.
	AccessReasonExpired AccessReason = "expired"
	// AccessReasonBillingIssue indicates access affected by a provider billing problem.
	AccessReasonBillingIssue AccessReason = "billing_issue"
	// AccessReasonPaused indicates access affected by a paused subscription.
	AccessReasonPaused AccessReason = "paused"
	// AccessReasonRefunded indicates access evaluated after a refund.
	AccessReasonRefunded AccessReason = "refunded"
	// AccessReasonRevoked indicates that the provider removed service immediately.
	AccessReasonRevoked AccessReason = "revoked"
	// AccessReasonProviderDecision indicates an authoritative provider-specific decision.
	AccessReasonProviderDecision AccessReason = "provider_decision"
	// AccessReasonUnresolved indicates that no safe access decision is available yet.
	AccessReasonUnresolved AccessReason = "unresolved"
)

const (
	// OwnershipPurchased indicates that the current customer bought the product.
	OwnershipPurchased Ownership = "purchased"
	// OwnershipFamilyShared indicates that access comes from provider family sharing.
	OwnershipFamilyShared Ownership = "family_shared"
	// OwnershipUnknown indicates that the provider does not expose ownership detail.
	OwnershipUnknown Ownership = "unknown"
)

const (
	// RenewalNone identifies a purchase that does not renew automatically.
	RenewalNone RenewalMode = "none"
	// RenewalAuto identifies a provider-managed automatically renewing purchase.
	RenewalAuto RenewalMode = "auto"
	// RenewalPrepaid identifies a fixed prepaid subscription period.
	RenewalPrepaid RenewalMode = "prepaid"
	// RenewalUnknown identifies renewal behavior that cannot yet be determined.
	RenewalUnknown RenewalMode = "unknown"
)

const (
	// RenewalNotApplicable indicates that renewal status does not apply.
	RenewalNotApplicable RenewalStatus = "not_applicable"
	// RenewalEnabled indicates that automatic renewal is enabled.
	RenewalEnabled RenewalStatus = "enabled"
	// RenewalDisabled indicates that automatic renewal is disabled.
	RenewalDisabled RenewalStatus = "disabled"
	// RenewalStatusUnknown indicates that automatic renewal status is unavailable.
	RenewalStatusUnknown RenewalStatus = "unknown"
)

type LifecycleState string

type AccessStatus string

type AccessReason string

type Ownership string

type RenewalMode string

type RenewalStatus string

type Renewal struct {
	Mode          RenewalMode
	Status        RenewalStatus
	NextProductID ProviderProductID
	NextRenewalAt *time.Time
}

type EffectivePeriod struct {
	StartsAt time.Time
	EndsAt   *time.Time
}

// PurchaseObservation is a verified, normalized view of one provider line item
// at a point in time. ID must be a non-sensitive deterministic idempotency key;
// provider tokens belong in redacted StoreReference values instead.
type PurchaseObservation struct {
	ID              ObservationID
	ApplicationID   ApplicationID
	Store           StoreApplication
	ProductID       ProviderProductID
	ProductKind     ProductKind
	State           LifecycleState
	ProviderState   string
	Access          AccessStatus
	AccessReason    AccessReason
	Ownership       Ownership
	Quantity        uint32
	OccurredAt      time.Time
	ObservedAt      time.Time
	EffectivePeriod EffectivePeriod
	Renewal         Renewal
	References      []StoreReference
}

func (state LifecycleState) Validate() error {
	switch state {
	case LifecyclePending, LifecycleActive, LifecycleGracePeriod, LifecycleOnHold,
		LifecyclePaused, LifecycleCanceled, LifecycleExpired, LifecycleRefunded,
		LifecycleRevoked, LifecycleUnresolved:
		return nil
	default:
		return fmt.Errorf("unsupported lifecycle state %q", state)
	}
}

func (status AccessStatus) Validate() error {
	switch status {
	case AccessAllowed, AccessDenied, AccessUnresolved:
		return nil
	default:
		return fmt.Errorf("unsupported access status %q", status)
	}
}

func (reason AccessReason) Validate() error {
	switch reason {
	case AccessReasonPurchaseValid, AccessReasonGracePeriod, AccessReasonPendingPayment,
		AccessReasonCanceledAtPeriodEnd, AccessReasonExpired, AccessReasonBillingIssue,
		AccessReasonPaused, AccessReasonRefunded, AccessReasonRevoked,
		AccessReasonProviderDecision, AccessReasonUnresolved:
		return nil
	default:
		return fmt.Errorf("unsupported access reason %q", reason)
	}
}

func (ownership Ownership) Validate() error {
	switch ownership {
	case OwnershipPurchased, OwnershipFamilyShared, OwnershipUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported ownership %q", ownership)
	}
}

func (renewal Renewal) Validate(productKind ProductKind) error {
	switch renewal.Mode {
	case RenewalNone:
		if renewal.Status != RenewalNotApplicable {
			return errors.New("non-renewing purchase must use not_applicable renewal status")
		}
	case RenewalAuto:
		if renewal.Status != RenewalEnabled && renewal.Status != RenewalDisabled && renewal.Status != RenewalStatusUnknown {
			return errors.New("auto-renewing purchase has invalid renewal status")
		}
	case RenewalPrepaid:
		if renewal.Status != RenewalNotApplicable {
			return errors.New("prepaid purchase must use not_applicable renewal status")
		}
	case RenewalUnknown:
		if renewal.Status != RenewalStatusUnknown {
			return errors.New("unknown renewal mode must use unknown renewal status")
		}
	default:
		return fmt.Errorf("unsupported renewal mode %q", renewal.Mode)
	}

	if productKind != ProductKindSubscription && renewal.Mode != RenewalNone {
		return errors.New("non-subscription product cannot have a renewal mode")
	}
	if renewal.NextProductID != "" {
		if err := renewal.NextProductID.Validate(); err != nil {
			return err
		}
		if renewal.Mode != RenewalAuto {
			return errors.New("next renewal product requires auto-renewal mode")
		}
		if renewal.Status == RenewalDisabled {
			return errors.New("disabled renewal cannot have a next product")
		}
	}
	if renewal.NextRenewalAt != nil {
		if renewal.NextRenewalAt.IsZero() {
			return errors.New("next renewal time must not be zero")
		}
		if renewal.Mode != RenewalAuto || renewal.Status == RenewalDisabled {
			return errors.New("next renewal time requires enabled or unresolved auto-renewal")
		}
	}
	return nil
}

func (period EffectivePeriod) Validate() error {
	if period.StartsAt.IsZero() {
		if period.EndsAt != nil {
			return errors.New("effective period cannot end without a start")
		}
		return nil
	}
	if period.EndsAt != nil && !period.EndsAt.After(period.StartsAt) {
		return errors.New("effective period end must be after its start")
	}
	return nil
}

func (observation PurchaseObservation) Validate() error {
	if err := errors.Join(
		observation.ID.Validate(),
		observation.ApplicationID.Validate(),
		observation.Store.Validate(),
		observation.ProductID.Validate(),
		observation.ProductKind.Validate(),
		observation.State.Validate(),
		validateIdentifier("provider state", observation.ProviderState),
		observation.Access.Validate(),
		observation.AccessReason.Validate(),
		observation.Ownership.Validate(),
		observation.EffectivePeriod.Validate(),
		observation.Renewal.Validate(observation.ProductKind),
	); err != nil {
		return err
	}

	if observation.Quantity == 0 {
		return errors.New("purchase quantity must be greater than zero")
	}
	if observation.ObservedAt.IsZero() {
		return errors.New("observation time is required")
	}
	if observation.Access == AccessAllowed {
		if !observation.hasReferenceRole(ReferenceTransaction) {
			return errors.New("allowed access requires a provider transaction reference")
		}
		if observation.OccurredAt.IsZero() {
			return errors.New("allowed access requires a transaction occurrence time")
		}
		if observation.EffectivePeriod.StartsAt.IsZero() {
			return errors.New("allowed access requires an effective period start")
		}
		if observation.ProductKind == ProductKindSubscription && observation.EffectivePeriod.EndsAt == nil {
			return errors.New("allowed subscription access requires an effective period end")
		}
	}
	if observation.State == LifecyclePending && observation.Access != AccessUnresolved {
		return errors.New("pending purchase access must be unresolved")
	}
	if observation.State == LifecycleExpired && observation.Access != AccessDenied {
		return errors.New("expired purchase access must be denied")
	}
	if observation.State == LifecycleRevoked && observation.Access != AccessDenied {
		return errors.New("revoked purchase access must be denied")
	}

	if err := validateReferenceSet(observation.References); err != nil {
		return err
	}
	if !observation.hasReferenceRole(ReferenceTransaction) && !observation.hasReferenceRole(ReferenceQuery) {
		return errors.New("observation requires a transaction or query reference")
	}

	return nil
}

func (observation PurchaseObservation) ReferencesFor(role ReferenceRole) []StoreReference {
	references := make([]StoreReference, 0, len(observation.References))
	for _, reference := range observation.References {
		if reference.Role == role {
			references = append(references, reference)
		}
	}
	return references
}

func (observation PurchaseObservation) hasReferenceRole(role ReferenceRole) bool {
	for _, reference := range observation.References {
		if reference.Role == role {
			return true
		}
	}
	return false
}

func validateReferenceSet(references []StoreReference) error {
	for i, reference := range references {
		if err := reference.Validate(); err != nil {
			return fmt.Errorf("store reference %d: %w", i, err)
		}
		for j := 0; j < i; j++ {
			if reference.Equal(references[j]) {
				return errors.New("duplicate store reference")
			}
		}
	}
	return nil
}
