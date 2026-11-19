package reprojection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/verification"
)

// unavailableStore runs callbacks with a transaction that lacks the reprojection methods.
type unavailableStore struct{}

// TestRunValidatesOptions verifies invalid options fail before any transaction starts.
func TestRunValidatesOptions(t *testing.T) {
	t.Parallel()

	projector := verification.NewDefaultProjector()
	cases := []Options{
		{ProjectID: "project-1", BatchSize: -1, Projector: projector},
		{ProjectID: "project-1", BatchSize: persistence.MaxReprojectionPageSize + 1, Projector: projector},
		{ProjectID: "project-1"},
		{ProjectID: "bad\tproject", Projector: projector},
	}
	for _, options := range cases {
		if _, err := Run(context.Background(), nil, options, nil); err == nil {
			t.Fatalf("Run(%#v) error = nil", options)
		}
	}
	normalized, err := Options{Projector: projector}.normalized()
	if err != nil || normalized.BatchSize != DefaultBatchSize || normalized.Clock == nil {
		t.Fatalf("normalized() = %#v, %v", normalized, err)
	}
}

// TestRunRefusesAStoreWithoutTheReprojectionRepository verifies an unsupported store fails closed.
func TestRunRefusesAStoreWithoutTheReprojectionRepository(t *testing.T) {
	t.Parallel()

	summary, err := Run(context.Background(), unavailableStore{}, Options{
		ProjectID: "project-1", Projector: verification.NewDefaultProjector(),
	}, nil)
	if !errors.Is(err, errRepositoryUnavailable) || summary.Mode != ModeDryRun || summary.CustomersScanned != 0 {
		t.Fatalf("Run() = %#v, %v", summary, err)
	}
}

// TestNewCustomerReportDescribesSourcesAndAccessAtTheEvaluationTime verifies the report fields.
func TestNewCustomerReportDescribesSourcesAndAccessAtTheEvaluationTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	lapsed := now.Add(-24 * time.Hour)
	current := now.Add(30 * 24 * time.Hour)
	before := persistence.CustomerEntitlement{
		Projection: persistence.EntitlementProjection{
			ProjectID: "project-1", CustomerID: "customer-1", EntitlementID: "entitlement-1",
			SourceKind: persistence.SourceKindStoreObservation, SourceObservationID: "observation-1",
			SourceProductID: "product-store", Access: core.AccessAllowed, AccessReason: core.AccessReasonPurchaseValid,
			EffectivePeriod: core.EffectivePeriod{StartsAt: lapsed.Add(-time.Hour), EndsAt: &lapsed},
		},
		SourceApplicationID: "application-1", Key: "premium", Version: 1,
	}
	after := persistence.CustomerEntitlement{
		Projection: persistence.EntitlementProjection{
			ProjectID: "project-1", CustomerID: "customer-1", EntitlementID: "entitlement-1",
			SourceKind: persistence.SourceKindManual, SourceManualGrantID: "mgr-1",
			SourceProductID: "product-manual", Access: core.AccessAllowed, AccessReason: core.AccessReasonManualGrant,
			EffectivePeriod: core.EffectivePeriod{StartsAt: now.Add(-time.Hour), EndsAt: &current},
		},
		SourceApplicationID: "application-1", Key: "premium", Version: 2,
	}
	report := newCustomerReport(Options{Apply: true}, "project-1", "customer-1", now, verification.ProjectionRefreshResult{
		ProjectionsChanged: 1, WebhooksEnqueued: 1,
		Changes: []verification.ProjectionChange{{Before: &before, After: after}},
	})
	if report.Mode != ModeApply || len(report.Changes) != 1 {
		t.Fatalf("report = %#v", report)
	}
	change := report.Changes[0]
	if change.EntitlementKey != "premium" || change.Before == nil ||
		change.Before.SourceID != "observation-1" || change.Before.StoredAccess != core.AccessAllowed ||
		change.Before.Access != core.AccessDenied || change.Before.SourceCustomerID != "customer-1" ||
		change.After.SourceID != "mgr-1" || change.After.Access != core.AccessAllowed ||
		change.After.EndsAt == nil || !change.After.EndsAt.Equal(current) {
		t.Fatalf("change = %#v", change)
	}
}

// Transact runs the callback with a transaction that does not implement the reprojection methods.
func (unavailableStore) Transact(_ context.Context, operation persistence.TransactionFunc) error {
	return operation(nil)
}
