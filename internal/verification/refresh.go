package verification

import (
	"context"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// RefreshCustomerProjection recomputes one canonical customer's current entitlements.
func RefreshCustomerProjection(
	ctx context.Context,
	repository persistence.Transaction,
	projector Projector,
	projectID core.ProjectID,
	customerID core.CustomerID,
	occurredAt time.Time,
) error {
	if projector == nil {
		return fmt.Errorf("entitlement projector is not initialized")
	}
	sources, err := repository.CustomerEntitlementSources(ctx, projectID, customerID)
	if err != nil {
		return err
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
		return err
	}
	projections, err := projector.Project(current, candidates)
	if err != nil {
		return err
	}
	for _, projection := range projections {
		if projection.SourceCustomerID == "" {
			projection.SourceCustomerID = projection.CustomerID
		}
		projection.CustomerID = customerID
		projection.ProjectID = projectID
		writeResult, err := repository.PutEntitlement(ctx, projection)
		if err != nil {
			return err
		}
		if !writeResult.Changed {
			continue
		}
		event, err := newEntitlementEvent(writeResult.Entitlement.SourceApplicationID, writeResult.Entitlement, occurredAt)
		if err != nil {
			return err
		}
		if _, err := repository.SaveOutboxEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
