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
	if projector == nil {
		return ProjectionRefreshResult{}, fmt.Errorf("entitlement projector is not initialized")
	}
	sources, err := repository.CustomerEntitlementSources(ctx, projectID, customerID)
	if err != nil {
		return ProjectionRefreshResult{}, err
	}
	candidates := make([]ProjectionCandidate, 0, len(sources))
	for _, source := range sources {
		candidates = append(candidates, ProjectionCandidate{
			Projection:          source.Projection,
			SourceApplicationID: source.SourceApplicationID,
			ObservedAt:          source.ObservedAt,
		})
	}
	current, err := repository.CustomerEntitlements(ctx, projectID, customerID)
	if err != nil {
		return ProjectionRefreshResult{}, err
	}
	projections, err := projector.Project(current, candidates)
	if err != nil {
		return ProjectionRefreshResult{}, err
	}
	result := ProjectionRefreshResult{}
	for _, projection := range projections {
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
