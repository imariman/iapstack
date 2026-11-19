// Package reprojection refreshes stored customer entitlements that an earlier projector ranked
// differently, with the same locks, writes, and change events as a normal projection refresh.
package reprojection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// DefaultBatchSize is the number of canonical customers read per keyset page.
	DefaultBatchSize = 100
	// ModeDryRun reports what a refresh would change and rolls every customer transaction back.
	ModeDryRun = "dry_run"
	// ModeApply commits every customer refresh and its change events.
	ModeApply = "apply"
)

// Transactor starts one atomic unit of work, as the PostgreSQL store does.
type Transactor interface {
	// Transact executes one callback in an atomic unit of work and may retry it after concurrency failures.
	Transact(context.Context, persistence.TransactionFunc) error
}

// repository is the persistence surface the reprojection uses inside one transaction.
type repository interface {
	persistence.Transaction
	// ReprojectionProjects returns every project id in id order.
	ReprojectionProjects(context.Context) ([]core.ProjectID, error)
	// ReprojectionCustomers returns one keyset page of canonical customer ids in id order.
	ReprojectionCustomers(context.Context, persistence.ReprojectionPage) ([]core.CustomerID, error)
	// CustomerIsCanonical reports whether one customer exists and has not been merged into another.
	CustomerIsCanonical(context.Context, core.ProjectID, core.CustomerID) (bool, error)
}

// Options selects the customers to reproject and whether the changes are committed.
type Options struct {
	// ProjectID limits the run to one project. Empty walks every project in id order.
	ProjectID core.ProjectID
	// BatchSize is the keyset page size. Zero uses DefaultBatchSize.
	BatchSize int
	// Apply commits the refreshes. Without it every customer transaction is rolled back.
	Apply bool
	// AllCustomers walks every canonical customer instead of only those that hold an allowed
	// import grant and an allowed store observation of another product for one entitlement.
	AllCustomers bool
	// Projector is the entitlement selection policy the server is configured with.
	Projector verification.Projector
	// Clock returns the time each customer refresh is evaluated and its events occur at.
	Clock func() time.Time
}

// Summary counts one run.
type Summary struct {
	Mode               string           `json:"mode"`
	Projects           []core.ProjectID `json:"projects"`
	CustomersScanned   int              `json:"customers_scanned"`
	CustomersSkipped   int              `json:"customers_skipped"`
	CustomersChanged   int              `json:"customers_changed"`
	ProjectionsChanged int              `json:"projections_changed"`
	WebhooksEnqueued   int              `json:"webhooks_enqueued"`
}

// CustomerReport is the outcome of one customer refresh that changed, or in a dry run would
// change, at least one stored projection.
type CustomerReport struct {
	Mode               string              `json:"mode"`
	ProjectID          core.ProjectID      `json:"project_id"`
	CustomerID         core.CustomerID     `json:"customer_id"`
	EvaluatedAt        time.Time           `json:"evaluated_at"`
	ProjectionsChanged int                 `json:"projections_changed"`
	WebhooksEnqueued   int                 `json:"webhooks_enqueued"`
	Changes            []EntitlementChange `json:"changes"`
}

// EntitlementChange is one stored projection before and after the refresh.
type EntitlementChange struct {
	EntitlementID  core.EntitlementID `json:"entitlement_id"`
	EntitlementKey string             `json:"entitlement_key"`
	// Before is nil when the customer had no stored projection for the entitlement.
	Before *ProjectionState `json:"before"`
	After  ProjectionState  `json:"after"`
}

// ProjectionState is the secret-free source, access, and end of one stored projection.
type ProjectionState struct {
	SourceKind          persistence.SourceKind `json:"source_kind"`
	SourceID            string                 `json:"source_id"`
	SourceApplicationID core.ApplicationID     `json:"source_application_id"`
	SourceProductID     core.ProductID         `json:"source_product_id"`
	SourceCustomerID    core.CustomerID        `json:"source_customer_id"`
	// StoredAccess is the access status written on the row.
	StoredAccess core.AccessStatus `json:"stored_access"`
	// Access is what the public read path returns at the evaluation time.
	Access       core.AccessStatus `json:"access"`
	AccessReason core.AccessReason `json:"access_reason"`
	EndsAt       *time.Time        `json:"ends_at"`
	Version      int64             `json:"version"`
}

var (
	// errDryRunRollback rolls one dry-run transaction back after its writes were measured.
	errDryRunRollback = errors.New("dry run rolls the refresh back")
	// errRepositoryUnavailable reports a store whose transactions cannot list or check customers.
	errRepositoryUnavailable = errors.New("reprojection repository is unavailable")
)

// Run walks canonical customers in keyset pages and refreshes each one in its own transaction,
// locking its closure first as every other refresh does. report receives every customer whose
// projections changed, or would change in a dry run, before the next customer is read. A dry run
// performs the same writes as an apply and then rolls them back, so it reports exactly what an
// apply at the same instant would write. A customer merged after its page was read is skipped,
// because the merge already refreshed its canonical customer. Rerunning is safe: a customer whose
// projections already match its sources writes nothing and enqueues no event.
func Run(
	ctx context.Context,
	store Transactor,
	options Options,
	report func(CustomerReport) error,
) (Summary, error) {
	options, err := options.normalized()
	if err != nil {
		return Summary{}, err
	}
	summary := Summary{Mode: ModeDryRun, Projects: []core.ProjectID{}}
	if options.Apply {
		summary.Mode = ModeApply
	}
	projects := []core.ProjectID{options.ProjectID}
	if options.ProjectID == "" {
		projects, err = listProjects(ctx, store)
		if err != nil {
			return summary, err
		}
	}
	for _, projectID := range projects {
		summary.Projects = append(summary.Projects, projectID)
		if err := runProject(ctx, store, options, projectID, &summary, report); err != nil {
			return summary, fmt.Errorf("reproject project %q: %w", projectID, err)
		}
	}
	return summary, nil
}

// normalized validates the options and fills defaults.
func (options Options) normalized() (Options, error) {
	if options.ProjectID != "" {
		if err := options.ProjectID.Validate(); err != nil {
			return Options{}, err
		}
	}
	if options.BatchSize == 0 {
		options.BatchSize = DefaultBatchSize
	}
	if options.BatchSize < 1 || options.BatchSize > persistence.MaxReprojectionPageSize {
		return Options{}, fmt.Errorf("batch size must be between 1 and %d", persistence.MaxReprojectionPageSize)
	}
	if options.Projector == nil {
		return Options{}, errors.New("entitlement projector is not initialized")
	}
	if options.Clock == nil {
		options.Clock = func() time.Time { return time.Now().UTC() }
	}
	return options, nil
}

// listProjects reads every project id in one transaction.
func listProjects(ctx context.Context, store Transactor) ([]core.ProjectID, error) {
	var projects []core.ProjectID
	err := store.Transact(ctx, func(tx persistence.Transaction) error {
		repository, ok := tx.(repository)
		if !ok {
			return errRepositoryUnavailable
		}
		var listErr error
		projects, listErr = repository.ReprojectionProjects(ctx)
		return listErr
	})
	return projects, err
}

// runProject walks one project's canonical customers in keyset pages.
func runProject(
	ctx context.Context,
	store Transactor,
	options Options,
	projectID core.ProjectID,
	summary *Summary,
	report func(CustomerReport) error,
) error {
	page := persistence.ReprojectionPage{
		ProjectID:    projectID,
		Limit:        options.BatchSize,
		AllCustomers: options.AllCustomers,
	}
	for {
		customers, err := listCustomers(ctx, store, page)
		if err != nil {
			return err
		}
		for _, customerID := range customers {
			if err := ctx.Err(); err != nil {
				return err
			}
			customer, skipped, err := reprojectCustomer(ctx, store, options, projectID, customerID)
			if err != nil {
				return fmt.Errorf("reproject customer %q: %w", customerID, err)
			}
			summary.CustomersScanned++
			if skipped {
				summary.CustomersSkipped++
				continue
			}
			if customer.ProjectionsChanged == 0 {
				continue
			}
			summary.CustomersChanged++
			summary.ProjectionsChanged += customer.ProjectionsChanged
			summary.WebhooksEnqueued += customer.WebhooksEnqueued
			if report != nil {
				if err := report(customer); err != nil {
					return err
				}
			}
		}
		if len(customers) < page.Limit {
			return nil
		}
		page.AfterCustomerID = customers[len(customers)-1]
	}
}

// listCustomers reads one keyset page of canonical customer ids.
func listCustomers(
	ctx context.Context,
	store Transactor,
	page persistence.ReprojectionPage,
) ([]core.CustomerID, error) {
	var customers []core.CustomerID
	err := store.Transact(ctx, func(tx persistence.Transaction) error {
		repository, ok := tx.(repository)
		if !ok {
			return errRepositoryUnavailable
		}
		var listErr error
		customers, listErr = repository.ReprojectionCustomers(ctx, page)
		return listErr
	})
	return customers, err
}

// reprojectCustomer refreshes one canonical customer in its own transaction and reports what the
// refresh wrote. It reports skipped when the customer was merged or removed after it was listed.
func reprojectCustomer(
	ctx context.Context,
	store Transactor,
	options Options,
	projectID core.ProjectID,
	customerID core.CustomerID,
) (CustomerReport, bool, error) {
	var customer CustomerReport
	skipped := false
	err := store.Transact(ctx, func(tx persistence.Transaction) error {
		customer = CustomerReport{}
		skipped = false
		repository, ok := tx.(repository)
		if !ok {
			return errRepositoryUnavailable
		}
		if err := repository.LockCustomerClosure(ctx, projectID, customerID); err != nil {
			if errors.Is(err, persistence.ErrNotFound) {
				skipped = true
				return nil
			}
			return err
		}
		canonical, err := repository.CustomerIsCanonical(ctx, projectID, customerID)
		if err != nil {
			return err
		}
		if !canonical {
			skipped = true
			return nil
		}
		evaluatedAt := options.Clock().UTC()
		refreshed, err := verification.RefreshCustomerProjectionWithOptions(
			ctx, repository, options.Projector, projectID, customerID, evaluatedAt, true,
		)
		if err != nil {
			return err
		}
		customer = newCustomerReport(options, projectID, customerID, evaluatedAt, refreshed)
		if !options.Apply {
			return errDryRunRollback
		}
		return nil
	})
	if errors.Is(err, errDryRunRollback) {
		err = nil
	}
	if err != nil {
		return CustomerReport{}, false, err
	}
	return customer, skipped, nil
}

// newCustomerReport converts one refresh result into the secret-free customer report.
func newCustomerReport(
	options Options,
	projectID core.ProjectID,
	customerID core.CustomerID,
	evaluatedAt time.Time,
	refreshed verification.ProjectionRefreshResult,
) CustomerReport {
	customer := CustomerReport{
		Mode:               ModeDryRun,
		ProjectID:          projectID,
		CustomerID:         customerID,
		EvaluatedAt:        evaluatedAt,
		ProjectionsChanged: refreshed.ProjectionsChanged,
		WebhooksEnqueued:   refreshed.WebhooksEnqueued,
		Changes:            make([]EntitlementChange, 0, len(refreshed.Changes)),
	}
	if options.Apply {
		customer.Mode = ModeApply
	}
	for _, change := range refreshed.Changes {
		entry := EntitlementChange{
			EntitlementID:  change.After.Projection.EntitlementID,
			EntitlementKey: change.After.Key,
			After:          newProjectionState(change.After, evaluatedAt),
		}
		if change.Before != nil {
			before := newProjectionState(*change.Before, evaluatedAt)
			entry.Before = &before
		}
		customer.Changes = append(customer.Changes, entry)
	}
	return customer
}

// newProjectionState describes one stored projection with its access at the evaluation time.
func newProjectionState(entitlement persistence.CustomerEntitlement, at time.Time) ProjectionState {
	projection := entitlement.Projection.Normalized()
	access, reason := core.AccessAt(projection.Access, projection.AccessReason, projection.EffectivePeriod.EndsAt, at)
	state := ProjectionState{
		SourceKind:          projection.SourceKind,
		SourceApplicationID: entitlement.SourceApplicationID,
		SourceProductID:     projection.SourceProductID,
		SourceCustomerID:    projection.SourceCustomerID,
		StoredAccess:        projection.Access,
		Access:              access,
		AccessReason:        reason,
		Version:             entitlement.Version,
	}
	switch projection.SourceKind {
	case persistence.SourceKindImport:
		state.SourceID = projection.SourceImportID
	case persistence.SourceKindManual:
		state.SourceID = projection.SourceManualGrantID
	default:
		state.SourceID = string(projection.SourceObservationID)
	}
	if projection.EffectivePeriod.EndsAt != nil {
		endsAt := projection.EffectivePeriod.EndsAt.UTC()
		state.EndsAt = &endsAt
	}
	return state
}
