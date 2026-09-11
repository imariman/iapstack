package iapstack

import "time"

const (
	// accessAllowed is the v1 projection value that currently grants service.
	accessAllowed = "allowed"
)

// CustomerSession is one short-lived opaque bearer returned exactly once.
type CustomerSession struct {
	// Token is the opaque customer bearer returned exactly once.
	Token string
	// ExpiresAt is the UTC expiry of the minted session.
	ExpiresAt time.Time
}

// Entitlement is one current application-scoped projection.
type Entitlement struct {
	// Key is the public entitlement key configured by the application.
	Key string
	// Access is the forward-compatible access value, currently allowed, denied, or unresolved.
	Access string
	// Reason is the forward-compatible normalized access reason.
	Reason string
	// Version is the projection generation incremented only for logical changes.
	Version int64
	// EffectiveStartsAt is the inclusive access period start, when applicable.
	EffectiveStartsAt *time.Time
	// EffectiveEndsAt is the exclusive access period end, when applicable.
	EffectiveEndsAt *time.Time
}

// EntitlementSnapshot is the current projection set for one customer.
type EntitlementSnapshot struct {
	// CustomerID is the internal stable customer identifier.
	CustomerID string
	// Entitlements are all current application-scoped projections for the customer.
	Entitlements []Entitlement
}

// EntitlementChange is the authenticated v1 entitlement.changed webhook payload.
type EntitlementChange struct {
	// SchemaVersion is the outbox payload contract version.
	SchemaVersion int
	// ProjectID is the project that owns the changed projection.
	ProjectID string
	// ApplicationID is the application that emitted the event.
	ApplicationID string
	// CustomerID is the internal stable customer identifier.
	CustomerID string
	// EntitlementID is the durable entitlement definition identifier.
	EntitlementID string
	// EntitlementKey is the public entitlement key configured by the application.
	EntitlementKey string
	// Access is the forward-compatible access value after the change.
	Access string
	// AccessReason is the forward-compatible normalized access reason.
	AccessReason string
	// SourceObservationID is the observation that produced this projection.
	SourceObservationID string
	// SourceApplicationID is the application that sourced the observation.
	SourceApplicationID string
	// SourceProductID is the product that sourced the observation.
	SourceProductID string
	// EffectiveStartsAt is the inclusive access period start, when applicable.
	EffectiveStartsAt *time.Time
	// EffectiveEndsAt is the exclusive access period end, when applicable.
	EffectiveEndsAt *time.Time
	// Version is the projection generation incremented only for logical changes.
	Version int64
}

// GrantsAccess reports whether the projection permits access at the current time.
func (entitlement Entitlement) GrantsAccess() bool {
	return entitlement.GrantsAccessAt(time.Now())
}

// GrantsAccessAt reports whether the projection permits access at the supplied instant.
func (entitlement Entitlement) GrantsAccessAt(instant time.Time) bool {
	if entitlement.Access != accessAllowed {
		return false
	}
	if entitlement.EffectiveEndsAt == nil {
		return true
	}
	return instant.UTC().Before(entitlement.EffectiveEndsAt.UTC())
}

// Entitlement converts one webhook payload into the provider-neutral projection shape.
func (change EntitlementChange) Entitlement() Entitlement {
	return Entitlement{
		Key:               change.EntitlementKey,
		Access:            change.Access,
		Reason:            change.AccessReason,
		Version:           change.Version,
		EffectiveStartsAt: change.EffectiveStartsAt,
		EffectiveEndsAt:   change.EffectiveEndsAt,
	}
}
