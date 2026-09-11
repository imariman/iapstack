package iapstack

import "time"

const (
	// accessAllowed is the v1 projection value that currently grants service.
	accessAllowed = "allowed"
)

// CustomerSession is one short-lived opaque bearer returned exactly once.
type CustomerSession struct {
	Token     string
	ExpiresAt time.Time
}

// Entitlement is one current application-scoped projection.
type Entitlement struct {
	Key               string
	Access            string
	Reason            string
	Version           int64
	EffectiveStartsAt *time.Time
	EffectiveEndsAt   *time.Time
}

// EntitlementSnapshot is the current projection set for one customer.
type EntitlementSnapshot struct {
	CustomerID   string
	Entitlements []Entitlement
}

// EntitlementChange is the authenticated v1 entitlement.changed webhook payload.
type EntitlementChange struct {
	SchemaVersion       int
	ProjectID           string
	ApplicationID       string
	CustomerID          string
	EntitlementID       string
	EntitlementKey      string
	Access              string
	AccessReason        string
	SourceObservationID string
	SourceApplicationID string
	SourceProductID     string
	EffectiveStartsAt   *time.Time
	EffectiveEndsAt     *time.Time
	Version             int64
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
