package core

import (
	"errors"
	"fmt"
	"time"
)

type LifecycleState string

const (
	LifecyclePending     LifecycleState = "pending"
	LifecycleActive      LifecycleState = "active"
	LifecycleGracePeriod LifecycleState = "grace_period"
	LifecycleOnHold      LifecycleState = "on_hold"
	LifecyclePaused      LifecycleState = "paused"
	LifecycleCanceled    LifecycleState = "canceled"
	LifecycleExpired     LifecycleState = "expired"
	LifecycleRefunded    LifecycleState = "refunded"
	LifecycleRevoked     LifecycleState = "revoked"
	LifecycleUnresolved  LifecycleState = "unresolved"
)

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

type AccessStatus string

const (
	AccessAllowed    AccessStatus = "allowed"
	AccessDenied     AccessStatus = "denied"
	AccessUnresolved AccessStatus = "unresolved"
)

func (status AccessStatus) Validate() error {
	switch status {
	case AccessAllowed, AccessDenied, AccessUnresolved:
		return nil
	default:
		return fmt.Errorf("unsupported access status %q", status)
	}
}

type AccessReason string

const (
	AccessReasonPurchaseValid       AccessReason = "purchase_valid"
	AccessReasonGracePeriod         AccessReason = "grace_period"
	AccessReasonPendingPayment      AccessReason = "pending_payment"
	AccessReasonCanceledAtPeriodEnd AccessReason = "canceled_at_period_end"
	AccessReasonExpired             AccessReason = "expired"
	AccessReasonBillingIssue        AccessReason = "billing_issue"
	AccessReasonPaused              AccessReason = "paused"
	AccessReasonRefunded            AccessReason = "refunded"
	AccessReasonRevoked             AccessReason = "revoked"
	AccessReasonProviderDecision    AccessReason = "provider_decision"
	AccessReasonUnresolved          AccessReason = "unresolved"
)

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

type Ownership string

const (
	OwnershipPurchased    Ownership = "purchased"
	OwnershipFamilyShared Ownership = "family_shared"
	OwnershipUnknown      Ownership = "unknown"
)

func (ownership Ownership) Validate() error {
	switch ownership {
	case OwnershipPurchased, OwnershipFamilyShared, OwnershipUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported ownership %q", ownership)
	}
}

type RenewalMode string

const (
	RenewalNone    RenewalMode = "none"
	RenewalAuto    RenewalMode = "auto"
	RenewalPrepaid RenewalMode = "prepaid"
	RenewalUnknown RenewalMode = "unknown"
)

type RenewalStatus string

const (
	RenewalNotApplicable RenewalStatus = "not_applicable"
	RenewalEnabled       RenewalStatus = "enabled"
	RenewalDisabled      RenewalStatus = "disabled"
	RenewalStatusUnknown RenewalStatus = "unknown"
)

type Renewal struct {
	Mode          RenewalMode
	Status        RenewalStatus
	NextProductID ProviderProductID
	NextRenewalAt *time.Time
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

type EffectivePeriod struct {
	StartsAt time.Time
	EndsAt   *time.Time
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
