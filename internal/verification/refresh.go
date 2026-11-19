package verification

import (
	"context"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// ProjectionRefreshResult counts the durable changes made by one projection refresh.
type ProjectionRefreshResult struct {
	ProjectionsChanged int
	WebhooksEnqueued   int
}

// projectionPlan is what one refresh read for a canonical customer and the projections it would write.
type projectionPlan struct {
	current     []persistence.CustomerEntitlement
	projections []persistence.EntitlementProjection
}

// RefreshCustomerProjection recomputes one canonical customer's entitlements and emits change events.
func RefreshCustomerProjection(
	ctx context.Context,
	repository persistence.Transaction,
	projector Projector,
	projectID core.ProjectID,
	customerID core.CustomerID,
	occurredAt time.Time,
) error {
	_, err := RefreshCustomerProjectionWithOptions(
		ctx, repository, projector, projectID, customerID, occurredAt, true,
	)
	return err
}

// RefreshCustomerProjectionWithOptions recomputes projections and optionally enqueues change events.
func RefreshCustomerProjectionWithOptions(
	ctx context.Context,
	repository persistence.Transaction,
	projector Projector,
	projectID core.ProjectID,
	customerID core.CustomerID,
	occurredAt time.Time,
	enqueueWebhooks bool,
) (ProjectionRefreshResult, error) {
	plan, err := planCustomerProjection(ctx, repository, projector, projectID, customerID)
	if err != nil {
		return ProjectionRefreshResult{}, err
	}
	return applyCustomerProjection(ctx, repository, projectID, customerID, plan, occurredAt, enqueueWebhooks)
}

// planCustomerProjection loads every access source and the current snapshot of one customer's
// closure and projects them without writing anything.
func planCustomerProjection(
	ctx context.Context,
	repository persistence.Transaction,
	projector Projector,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (projectionPlan, error) {
	if projector == nil {
		return projectionPlan{}, fmt.Errorf("entitlement projector is not initialized")
	}
	sources, err := repository.CustomerEntitlementSources(ctx, projectID, customerID)
	if err != nil {
		return projectionPlan{}, err
	}
	candidates := make([]ProjectionCandidate, 0, len(sources))
	for _, source := range sources {
		candidates = append(candidates, ProjectionCandidate{
			Projection:          source.Projection,
			SourceApplicationID: source.SourceApplicationID,
			SourceLineage:       source.SourceLineage,
			ObservedAt:          source.ObservedAt,
		})
	}
	current, err := repository.CustomerEntitlements(ctx, projectID, customerID)
	if err != nil {
		return projectionPlan{}, err
	}
	projections, err := projector.Project(current, candidates)
	if err != nil {
		return projectionPlan{}, err
	}
	return projectionPlan{current: current, projections: projections}, nil
}

// applyCustomerProjection writes one plan's projections and optionally enqueues change events.
func applyCustomerProjection(
	ctx context.Context,
	repository persistence.Transaction,
	projectID core.ProjectID,
	customerID core.CustomerID,
	plan projectionPlan,
	occurredAt time.Time,
	enqueueWebhooks bool,
) (ProjectionRefreshResult, error) {
	result := ProjectionRefreshResult{}
	for _, projection := range plan.projections {
		if projection.SourceCustomerID == "" {
			projection.SourceCustomerID = projection.CustomerID
		}
		projection.CustomerID = customerID
		projection.ProjectID = projectID
		writeResult, err := repository.PutEntitlement(ctx, projection)
		if err != nil {
			return ProjectionRefreshResult{}, err
		}
		if !writeResult.Changed {
			continue
		}
		result.ProjectionsChanged++
		if !enqueueWebhooks {
			continue
		}
		event, err := newEntitlementEvent(writeResult.Entitlement.SourceApplicationID, writeResult.Entitlement, occurredAt)
		if err != nil {
			return ProjectionRefreshResult{}, err
		}
		if _, err := repository.SaveOutboxEvent(ctx, event); err != nil {
			return ProjectionRefreshResult{}, err
		}
		result.WebhooksEnqueued++
	}
	return result, nil
}

// accessAfter returns the access the plan leaves for each entitlement at one instant: a planned
// projection replaces the current row, and an entitlement the plan does not touch keeps it.
func (plan projectionPlan) accessAfter(at time.Time) map[core.EntitlementID]core.AccessStatus {
	access := make(map[core.EntitlementID]core.AccessStatus, len(plan.current)+len(plan.projections))
	for _, entitlement := range plan.current {
		access[entitlement.Projection.EntitlementID] = accessAt(entitlement.Projection, at)
	}
	for _, projection := range plan.projections {
		access[projection.EntitlementID] = accessAt(projection, at)
	}
	return access
}

// usableAt returns the entitlements a customer can use at one instant, either from the stored
// snapshot or from what a refresh of the customer's own sources would project. Reading both keeps
// a snapshot that an earlier projector ranked differently from hiding current access.
func (plan projectionPlan) usableAt(at time.Time) map[core.EntitlementID]struct{} {
	usable := make(map[core.EntitlementID]struct{}, len(plan.current))
	for _, entitlement := range plan.current {
		if accessAt(entitlement.Projection, at) == core.AccessAllowed {
			usable[entitlement.Projection.EntitlementID] = struct{}{}
		}
	}
	for entitlementID, access := range plan.accessAfter(at) {
		if access == core.AccessAllowed {
			usable[entitlementID] = struct{}{}
		}
	}
	return usable
}

// accessAt applies the effective end to one projection, as the public read path does.
func accessAt(projection persistence.EntitlementProjection, at time.Time) core.AccessStatus {
	access, _ := core.AccessAt(projection.Access, projection.AccessReason, projection.EffectivePeriod.EndsAt, at)
	return access
}
