package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// ManualGrantReasonMaxRunes bounds the operator reason stored with a grant or revocation.
	ManualGrantReasonMaxRunes = 500
	// ManualGrantIdempotencyKeyMaxRunes bounds the replay key accepted for one grant.
	ManualGrantIdempotencyKeyMaxRunes = 200
	// ManualGrantMaxDuration bounds how far ahead an operator grant may end.
	ManualGrantMaxDuration = 3660 * 24 * time.Hour
	// manualGrantIDPrefix identifies operator grant ids, which are never purchase observation ids.
	manualGrantIDPrefix = "mgr-"
)

var (
	// ErrManualGrantProductNotMapped means the chosen application has no store product mapped to the product.
	ErrManualGrantProductNotMapped = errors.New("the application does not map the product to a store product")
)

// ManualGrantRepository stores operator-issued access sources and their early revocations.
type ManualGrantRepository interface {
	// CreateManualGrant stores one grant for the canonical customer or returns the stored replay.
	CreateManualGrant(context.Context, ManualGrantWrite) (ManualGrantResult, error)
	// RevokeManualGrant ends one stored grant early or returns the stored revocation.
	RevokeManualGrant(context.Context, ManualGrantRevocation) (ManualGrantResult, error)
}

// ManualGrantWrite asks for one audited operator grant of an entitlement through one application product.
// CustomerID may name a merged customer; the grant is stored against its canonical customer.
type ManualGrantWrite struct {
	ProjectID      core.ProjectID
	CustomerID     core.CustomerID
	ApplicationID  core.ApplicationID
	ProductID      core.ProductID
	EntitlementID  core.EntitlementID
	Reason         string
	ActorKeyID     string
	ActorChannel   ActorChannel
	IdempotencyKey string
	StartsAt       time.Time
	EndsAt         time.Time
}

// ManualGrantRevocation ends one stored grant before its scheduled end.
type ManualGrantRevocation struct {
	ProjectID    core.ProjectID
	CustomerID   core.CustomerID
	GrantID      string
	Reason       string
	ActorKeyID   string
	ActorChannel ActorChannel
	RevokedAt    time.Time
}

// ManualGrant is one stored operator grant and its audit fields.
type ManualGrant struct {
	ID               string
	ProjectID        core.ProjectID
	CustomerID       core.CustomerID
	ApplicationID    core.ApplicationID
	ProductID        core.ProductID
	EntitlementID    core.EntitlementID
	EntitlementKey   string
	Reason           string
	ActorKeyID       string
	ActorChannel     ActorChannel
	IdempotencyKey   string
	StartsAt         time.Time
	EndsAt           time.Time
	RevokedAt        *time.Time
	RevokedByKeyID   string
	RevokedByChannel ActorChannel
	RevocationReason string
	CreatedAt        time.Time
}

// ManualGrantResult is a newly stored grant or revocation, or a safe replay of the stored row.
// CanonicalCustomerID is the customer whose projection must be refreshed after a change.
type ManualGrantResult struct {
	Grant               ManualGrant
	CanonicalCustomerID core.CustomerID
	Replay              bool
}

// Validate checks one grant request before it reaches PostgreSQL.
func (write ManualGrantWrite) Validate() error {
	var periodError error
	switch {
	case write.StartsAt.IsZero() || write.EndsAt.IsZero():
		periodError = errors.New("manual grant period requires a start and an end")
	case !write.EndsAt.After(write.StartsAt):
		periodError = errors.New("manual grant must end after it starts")
	case write.EndsAt.Sub(write.StartsAt) > ManualGrantMaxDuration:
		periodError = errors.New("manual grant period is too long")
	}
	return errors.Join(
		write.ProjectID.Validate(),
		write.CustomerID.Validate(),
		write.ApplicationID.Validate(),
		write.ProductID.Validate(),
		write.EntitlementID.Validate(),
		ValidateManualGrantReason("manual grant reason", write.Reason),
		core.ValidateOpaqueIdentifier("actor key ID", write.ActorKeyID),
		ValidateTransportActorChannel(write.ActorChannel),
		validateManualGrantIdempotencyKey(write.IdempotencyKey),
		periodError,
	)
}

// Validate checks one revocation request before it reaches PostgreSQL.
func (revocation ManualGrantRevocation) Validate() error {
	return errors.Join(
		revocation.ProjectID.Validate(),
		revocation.CustomerID.Validate(),
		ValidateManualGrantID(revocation.GrantID),
		ValidateManualGrantReason("revocation reason", revocation.Reason),
		core.ValidateOpaqueIdentifier("actor key ID", revocation.ActorKeyID),
		ValidateTransportActorChannel(revocation.ActorChannel),
		validateTime("revocation time", revocation.RevokedAt),
	)
}

// Validate checks one stored grant and its all-or-nothing revocation fields.
func (grant ManualGrant) Validate() error {
	var revocationError error
	if grant.RevokedAt != nil {
		revocationError = errors.Join(
			core.ValidateOpaqueIdentifier("revoking actor key ID", grant.RevokedByKeyID),
			ValidateTransportActorChannel(grant.RevokedByChannel),
			ValidateManualGrantReason("revocation reason", grant.RevocationReason),
		)
	} else if grant.RevokedByKeyID != "" || grant.RevokedByChannel != "" || grant.RevocationReason != "" {
		revocationError = errors.New("manual grant revocation fields must be set together")
	}
	var periodError error
	if !grant.EndsAt.After(grant.StartsAt) {
		periodError = errors.New("manual grant must end after it starts")
	}
	return errors.Join(
		ValidateManualGrantID(grant.ID),
		grant.ProjectID.Validate(),
		grant.CustomerID.Validate(),
		grant.ApplicationID.Validate(),
		grant.ProductID.Validate(),
		grant.EntitlementID.Validate(),
		ValidateManualGrantReason("manual grant reason", grant.Reason),
		core.ValidateOpaqueIdentifier("actor key ID", grant.ActorKeyID),
		ValidateTransportActorChannel(grant.ActorChannel),
		validateManualGrantIdempotencyKey(grant.IdempotencyKey),
		validateTime("manual grant start", grant.StartsAt),
		periodError,
		revocationError,
		validateTime("manual grant creation time", grant.CreatedAt),
	)
}

// Active reports whether the grant still allows access at one instant.
func (grant ManualGrant) Active(now time.Time) bool {
	return grant.RevokedAt == nil && !now.Before(grant.StartsAt) && now.Before(grant.EndsAt)
}

// ValidateManualGrantID checks the server-generated grant identity shape.
func ValidateManualGrantID(value string) error {
	if len(value) != len(manualGrantIDPrefix)+32 || !strings.HasPrefix(value, manualGrantIDPrefix) {
		return errors.New("manual grant ID is invalid")
	}
	for _, character := range value[len(manualGrantIDPrefix):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return errors.New("manual grant ID is invalid")
		}
	}
	return nil
}

// ValidateManualGrantReason checks bounded single-line operator text.
func ValidateManualGrantReason(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have leading or trailing whitespace", name)
	}
	if utf8.RuneCountInString(value) > ManualGrantReasonMaxRunes {
		return fmt.Errorf("%s must be at most %d characters", name, ManualGrantReasonMaxRunes)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}

// validateManualGrantIdempotencyKey checks one bounded replay key.
func validateManualGrantIdempotencyKey(value string) error {
	if err := core.ValidateOpaqueIdentifier("manual grant idempotency key", value); err != nil {
		return err
	}
	if utf8.RuneCountInString(value) > ManualGrantIdempotencyKeyMaxRunes {
		return fmt.Errorf("manual grant idempotency key must be at most %d characters", ManualGrantIdempotencyKeyMaxRunes)
	}
	return nil
}
