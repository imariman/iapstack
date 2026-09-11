package iapstack

import (
	"errors"
	"time"
)

const (
	// accessAllowed is the v1 projection value that currently grants service.
	accessAllowed = "allowed"
)

// CustomerSession is one short-lived opaque bearer returned exactly once.
type CustomerSession struct {
	// Token is the opaque customer bearer returned exactly once.
	Token string `json:"token"`
	// ExpiresAt is the UTC expiry of the minted session.
	ExpiresAt time.Time `json:"expires_at"`
	// ExternalCustomerID is the host identity bound to this session at mint time.
	ExternalCustomerID string `json:"-"`
}

// Entitlement is one current application-scoped projection.
type Entitlement struct {
	// Key is the public entitlement key configured by the application.
	Key string `json:"key"`
	// Access is the forward-compatible access value, currently allowed, denied, or unresolved.
	Access string `json:"access"`
	// Reason is the forward-compatible normalized access reason.
	Reason string `json:"reason"`
	// Version is the projection generation incremented only for logical changes.
	Version int64 `json:"version"`
	// EffectiveStartsAt is the inclusive access period start, when applicable.
	EffectiveStartsAt *time.Time `json:"effective_starts_at"`
	// EffectiveEndsAt is the exclusive access period end, when applicable.
	EffectiveEndsAt *time.Time `json:"effective_ends_at"`
}

// EntitlementSnapshot is the current projection set for one customer.
type EntitlementSnapshot struct {
	// CustomerID is the internal stable customer identifier.
	CustomerID string `json:"customer_id"`
	// Entitlements are all current application-scoped projections for the customer.
	Entitlements []Entitlement `json:"entitlements"`
}

// EntitlementChange is the authenticated v1 entitlement.changed webhook payload.
type EntitlementChange struct {
	// SchemaVersion is the outbox payload contract version.
	SchemaVersion int `json:"schema_version"`
	// ProjectID is the project that owns the changed projection.
	ProjectID string `json:"project_id"`
	// ApplicationID is the application that emitted the event.
	ApplicationID string `json:"application_id"`
	// CustomerID is the internal stable customer identifier.
	CustomerID string `json:"customer_id"`
	// EntitlementID is the durable entitlement definition identifier.
	EntitlementID string `json:"entitlement_id"`
	// EntitlementKey is the public entitlement key configured by the application.
	EntitlementKey string `json:"entitlement_key"`
	// Access is the forward-compatible access value after the change.
	Access string `json:"access"`
	// AccessReason is the forward-compatible normalized access reason.
	AccessReason string `json:"access_reason"`
	// SourceObservationID is the observation that produced this projection.
	SourceObservationID string `json:"source_observation_id"`
	// SourceApplicationID is the application that sourced the observation.
	SourceApplicationID string `json:"source_application_id"`
	// SourceProductID is the product that sourced the observation.
	SourceProductID string `json:"source_product_id"`
	// EffectiveStartsAt is the inclusive access period start, when applicable.
	EffectiveStartsAt *time.Time `json:"effective_starts_at"`
	// EffectiveEndsAt is the exclusive access period end, when applicable.
	EffectiveEndsAt *time.Time `json:"effective_ends_at"`
	// Version is the projection generation incremented only for logical changes.
	Version int64 `json:"version"`
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

// validate reports whether a minted session includes the required contract fields.
func (session CustomerSession) validate() error {
	if session.Token == "" || session.ExpiresAt.IsZero() {
		return &ProtocolError{Message: "IAPStack response did not match the v1 contract"}
	}
	return nil
}

// validate reports whether a snapshot includes the required contract fields.
func (snapshot EntitlementSnapshot) validate() error {
	if snapshot.CustomerID == "" || snapshot.Entitlements == nil {
		return &ProtocolError{Message: "IAPStack response did not match the v1 contract"}
	}
	for _, entitlement := range snapshot.Entitlements {
		if err := entitlement.validate(); err != nil {
			return err
		}
	}
	return nil
}

// validate reports whether one entitlement includes the required contract fields.
func (entitlement Entitlement) validate() error {
	if entitlement.Key == "" || entitlement.Access == "" || entitlement.Reason == "" || entitlement.Version < 1 {
		return &ProtocolError{Message: "IAPStack response did not match the v1 contract"}
	}
	return nil
}

// validate reports whether an authenticated webhook payload includes required fields.
func (change EntitlementChange) validate() error {
	if change.SchemaVersion <= 0 || change.Version < 1 ||
		change.ProjectID == "" || change.ApplicationID == "" || change.CustomerID == "" ||
		change.EntitlementID == "" || change.EntitlementKey == "" || change.Access == "" ||
		change.AccessReason == "" || change.SourceObservationID == "" ||
		change.SourceApplicationID == "" || change.SourceProductID == "" {
		return errors.New("webhook event metadata is invalid")
	}
	return nil
}

// normalize copies timestamps into UTC without sharing the decoded pointers.
func (snapshot *EntitlementSnapshot) normalize() {
	for index := range snapshot.Entitlements {
		snapshot.Entitlements[index].EffectiveStartsAt = utcTime(snapshot.Entitlements[index].EffectiveStartsAt)
		snapshot.Entitlements[index].EffectiveEndsAt = utcTime(snapshot.Entitlements[index].EffectiveEndsAt)
	}
}

// normalize copies timestamps into UTC without sharing the decoded pointers.
func (change *EntitlementChange) normalize() {
	change.EffectiveStartsAt = utcTime(change.EffectiveStartsAt)
	change.EffectiveEndsAt = utcTime(change.EffectiveEndsAt)
}
