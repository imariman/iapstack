package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// ManualGrantMaxDurationDays bounds a relative manual grant duration.
	ManualGrantMaxDurationDays = 3660
	// StoredActorRequiredMessage is the stable client text for an audited write without a durable actor.
	StoredActorRequiredMessage = "stored credentials are required"
)

// ManualGrantInput asks for one audited grant for a relative duration or until an absolute end.
type ManualGrantInput struct {
	ProjectID      core.ProjectID
	CustomerID     core.CustomerID
	IdempotencyKey string
	EntitlementID  core.EntitlementID
	ProductID      core.ProductID
	ApplicationID  core.ApplicationID
	Reason         string
	// DurationDays and EndsAt are mutually exclusive; exactly one is required.
	DurationDays *int
	EndsAt       *time.Time
}

// ManualGrantRevocationInput ends one grant early for a required reason.
type ManualGrantRevocationInput struct {
	ProjectID  core.ProjectID
	CustomerID core.CustomerID
	GrantID    string
	Reason     string
}

// ManualGrantResult reports the stored grant and what the projection refresh changed.
type ManualGrantResult struct {
	Grant              ManualGrant `json:"grant"`
	Replay             bool        `json:"replay"`
	ProjectionsChanged int         `json:"projections_changed"`
	WebhooksEnqueued   int         `json:"webhooks_enqueued"`
}

// CustomerAliasInput attaches one external id to a canonical customer.
type CustomerAliasInput struct {
	ProjectID  core.ProjectID
	CustomerID core.CustomerID
	ExternalID string
}

// EntitlementInput creates one named entitlement definition or confirms the identical one.
type EntitlementInput struct {
	ProjectID     core.ProjectID
	EntitlementID core.EntitlementID
	Key           string
}

// ProductInput creates one product and its exact entitlement set or confirms the identical one.
type ProductInput struct {
	ProjectID      core.ProjectID
	ProductID      core.ProductID
	Kind           core.ProductKind
	EntitlementIDs []core.EntitlementID
}

// StoreProductInput maps one provider product of an application to an internal product, or confirms
// the identical mapping.
type StoreProductInput struct {
	ProjectID         core.ProjectID
	ApplicationID     core.ApplicationID
	ProviderProductID core.ProviderProductID
	ProductID         core.ProductID
}

var (
	// ErrStoredActorRequired means an audited write came from the bootstrap administrator, which has no
	// stored key or account to record. It is a forbidden error.
	ErrStoredActorRequired = fmt.Errorf("%s: %w", StoredActorRequiredMessage, ErrForbidden)
)

// CreateManualGrant records one audited operator grant and refreshes the projection and outbox. A repeated
// request with the same idempotency key and arguments returns the stored grant as a replay.
func (service *Service) CreateManualGrant(
	ctx context.Context,
	actor Actor,
	input ManualGrantInput,
) (ManualGrantResult, error) {
	if err := actor.AuthorizeWrite(input.ProjectID); err != nil {
		return ManualGrantResult{}, err
	}
	if actor.AuditID == "" {
		return ManualGrantResult{}, ErrStoredActorRequired
	}
	now := service.now()
	endsAt, err := ManualGrantEnd(input.DurationDays, input.EndsAt, now)
	if err != nil {
		return ManualGrantResult{}, validation.Wrap(err)
	}
	write := persistence.ManualGrantWrite{
		ProjectID:      input.ProjectID,
		CustomerID:     input.CustomerID,
		ApplicationID:  input.ApplicationID,
		ProductID:      input.ProductID,
		EntitlementID:  input.EntitlementID,
		Reason:         input.Reason,
		ActorKeyID:     actor.AuditID,
		ActorChannel:   actor.Channel,
		IdempotencyKey: input.IdempotencyKey,
		StartsAt:       now,
		EndsAt:         endsAt,
	}
	if err := write.Validate(); err != nil {
		return ManualGrantResult{}, validation.Wrap(err)
	}
	if service.dependencies.Store == nil {
		return ManualGrantResult{}, errMissingDependency
	}
	var result persistence.ManualGrantResult
	var refresh verification.ProjectionRefreshResult
	err = service.dependencies.Store.Transact(ctx, func(repository persistence.Transaction) error {
		var grantErr error
		refresh = verification.ProjectionRefreshResult{}
		result, grantErr = repository.CreateManualGrant(ctx, write)
		if grantErr != nil || result.Replay {
			return grantErr
		}
		refresh, grantErr = verification.RefreshCustomerProjectionWithOptions(
			ctx, repository, verification.NewDefaultProjector(),
			input.ProjectID, result.CanonicalCustomerID, now, true,
		)
		return grantErr
	})
	if err != nil {
		return ManualGrantResult{}, err
	}
	if !result.Replay {
		slog.Info(
			"manual grant created",
			slog.String("project_id", string(input.ProjectID)),
			slog.String("grant_id", result.Grant.ID),
			slog.String("channel", string(actor.Channel)),
			slog.Int("projections_changed", refresh.ProjectionsChanged),
		)
	}
	return ManualGrantResultView(result, refresh, now), nil
}

// RevokeManualGrant ends one operator grant early and refreshes the projection and outbox. Revoking an
// already revoked grant returns the stored revocation as a replay.
func (service *Service) RevokeManualGrant(
	ctx context.Context,
	actor Actor,
	input ManualGrantRevocationInput,
) (ManualGrantResult, error) {
	if err := actor.AuthorizeWrite(input.ProjectID); err != nil {
		return ManualGrantResult{}, err
	}
	if actor.AuditID == "" {
		return ManualGrantResult{}, ErrStoredActorRequired
	}
	now := service.now()
	revocation := persistence.ManualGrantRevocation{
		ProjectID:    input.ProjectID,
		CustomerID:   input.CustomerID,
		GrantID:      input.GrantID,
		Reason:       input.Reason,
		ActorKeyID:   actor.AuditID,
		ActorChannel: actor.Channel,
		RevokedAt:    now,
	}
	if err := revocation.Validate(); err != nil {
		return ManualGrantResult{}, validation.Wrap(err)
	}
	if service.dependencies.Store == nil {
		return ManualGrantResult{}, errMissingDependency
	}
	var result persistence.ManualGrantResult
	var refresh verification.ProjectionRefreshResult
	err := service.dependencies.Store.Transact(ctx, func(repository persistence.Transaction) error {
		var revokeErr error
		refresh = verification.ProjectionRefreshResult{}
		result, revokeErr = repository.RevokeManualGrant(ctx, revocation)
		if revokeErr != nil || result.Replay {
			return revokeErr
		}
		refresh, revokeErr = verification.RefreshCustomerProjectionWithOptions(
			ctx, repository, verification.NewDefaultProjector(),
			input.ProjectID, result.CanonicalCustomerID, now, true,
		)
		return revokeErr
	})
	if err != nil {
		return ManualGrantResult{}, err
	}
	if !result.Replay {
		slog.Info(
			"manual grant revoked",
			slog.String("project_id", string(input.ProjectID)),
			slog.String("grant_id", result.Grant.ID),
			slog.String("channel", string(actor.Channel)),
			slog.Int("projections_changed", refresh.ProjectionsChanged),
		)
	}
	return ManualGrantResultView(result, refresh, now), nil
}

// PutCustomerAlias attaches one external id to a canonical customer and records the actor's role, key,
// and channel. An application key may attach aliases only in its own project while its application exists.
func (service *Service) PutCustomerAlias(
	ctx context.Context,
	actor Actor,
	input CustomerAliasInput,
) (CustomerResolution, error) {
	if err := actor.authorizeAliasWrite(input.ProjectID); err != nil {
		return CustomerResolution{}, err
	}
	if actor.AuditID == "" {
		return CustomerResolution{}, ErrStoredActorRequired
	}
	write := persistence.CustomerAliasWrite{
		ProjectID:           input.ProjectID,
		CanonicalCustomerID: input.CustomerID,
		ExternalID:          input.ExternalID,
		ActorRole:           actor.Role,
		ActorKeyID:          actor.AuditID,
		ActorChannel:        actor.Channel,
	}
	if err := write.Validate(); err != nil {
		return CustomerResolution{}, validation.Wrap(err)
	}
	if service.dependencies.Store == nil {
		return CustomerResolution{}, errMissingDependency
	}
	var resolution persistence.CustomerResolution
	err := service.dependencies.Store.Transact(ctx, func(repository persistence.Transaction) error {
		if actor.Role == persistence.APIKeyRoleApplication {
			if _, loadErr := repository.Application(ctx, input.ProjectID, actor.ApplicationID); loadErr != nil {
				return loadErr
			}
		}
		var putErr error
		resolution, putErr = repository.PutCustomerAlias(ctx, write)
		return putErr
	})
	if err != nil {
		return CustomerResolution{}, err
	}
	return CustomerResolutionView(resolution), nil
}

// PutEntitlement creates one entitlement definition or confirms the identical one. A different key for an
// existing id is a conflict; nothing is overwritten.
func (service *Service) PutEntitlement(ctx context.Context, actor Actor, input EntitlementInput) error {
	if err := actor.AuthorizeWrite(input.ProjectID); err != nil {
		return err
	}
	entitlement := core.Entitlement{ID: input.EntitlementID, ProjectID: input.ProjectID, Key: input.Key}
	if err := entitlement.Validate(); err != nil {
		return validation.Wrap(err)
	}
	return service.operate(ctx, func(repository persistence.OperationsTransaction) error {
		return repository.PutEntitlementDefinition(ctx, entitlement)
	})
}

// PutProduct creates one product and its exact entitlement set or confirms the identical one. A different
// kind or entitlement set for an existing id is a conflict; nothing is overwritten.
func (service *Service) PutProduct(ctx context.Context, actor Actor, input ProductInput) error {
	if err := actor.AuthorizeWrite(input.ProjectID); err != nil {
		return err
	}
	product := core.Product{
		ID: input.ProductID, ProjectID: input.ProjectID, Kind: input.Kind, EntitlementIDs: input.EntitlementIDs,
	}
	if err := product.Validate(); err != nil {
		return validation.Wrap(err)
	}
	return service.operate(ctx, func(repository persistence.OperationsTransaction) error {
		return repository.PutProduct(ctx, product)
	})
}

// PutStoreProduct maps one provider product to an internal product or confirms the identical mapping. A
// mapping to a different product is a conflict; nothing is overwritten.
func (service *Service) PutStoreProduct(ctx context.Context, actor Actor, input StoreProductInput) error {
	if err := actor.AuthorizeWrite(input.ProjectID); err != nil {
		return err
	}
	mapping := core.StoreProduct{
		ApplicationID: input.ApplicationID, ProductID: input.ProductID, ProviderID: input.ProviderProductID,
	}
	if err := errors.Join(input.ProjectID.Validate(), mapping.Validate()); err != nil {
		return validation.Wrap(err)
	}
	return service.operate(ctx, func(repository persistence.OperationsTransaction) error {
		return repository.PutStoreProduct(ctx, input.ProjectID, mapping)
	})
}

// ManualGrantEnd resolves exactly one of a relative duration or an absolute end after now.
func ManualGrantEnd(durationDays *int, endsAt *time.Time, now time.Time) (time.Time, error) {
	switch {
	case durationDays != nil && endsAt != nil:
		return time.Time{}, errors.New("manual grant takes duration_days or ends_at, not both")
	case durationDays != nil:
		if *durationDays < 1 || *durationDays > ManualGrantMaxDurationDays {
			return time.Time{}, errors.New("manual grant duration_days is out of range")
		}
		return now.AddDate(0, 0, *durationDays), nil
	case endsAt != nil:
		end := endsAt.UTC()
		if !end.After(now) {
			return time.Time{}, errors.New("manual grant ends_at must be in the future")
		}
		if end.Sub(now) > persistence.ManualGrantMaxDuration {
			return time.Time{}, errors.New("manual grant ends_at is too far in the future")
		}
		return end, nil
	default:
		return time.Time{}, errors.New("manual grant requires duration_days or ends_at")
	}
}

// ManualGrantResultView maps one grant write and its projection refresh counts.
func ManualGrantResultView(
	result persistence.ManualGrantResult,
	refresh verification.ProjectionRefreshResult,
	now time.Time,
) ManualGrantResult {
	return ManualGrantResult{
		Grant:              ManualGrantView(result.Grant, now),
		Replay:             result.Replay,
		ProjectionsChanged: refresh.ProjectionsChanged,
		WebhooksEnqueued:   refresh.WebhooksEnqueued,
	}
}

// operate runs one catalog definition write in an operations transaction.
func (service *Service) operate(ctx context.Context, operation persistence.OperationsFunc) error {
	if service.dependencies.Operations == nil {
		return errMissingDependency
	}
	return service.dependencies.Operations.Operate(ctx, operation)
}
