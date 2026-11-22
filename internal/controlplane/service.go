package controlplane

import (
	"context"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

// FinancialReportLister lists the stored financial report imports of one project.
type FinancialReportLister interface {
	// List returns secret-free import metadata for one project.
	List(context.Context, core.ProjectID) ([]persistence.FinancialReport, error)
}

// Dependencies holds the persistence ports and services the control plane reads through.
// A nil dependency makes only the operations that need it fail.
type Dependencies struct {
	Store            persistence.Store
	Admin            persistence.AdminQueryStore
	Setup            persistence.AdminSetupStore
	FinancialReports FinancialReportLister
	// Protection fingerprints search text so customer search can match stored transaction ids.
	// Nil disables transaction id matching.
	Protection protection.Protector
	// PublicURL is the validated external base URL used to print full store notification URLs.
	// Empty means only paths are reported.
	PublicURL string
	// Clock returns the current time. Nil uses the system clock.
	Clock func() time.Time
}

// Service runs control-plane reads behind one shared authorization model.
type Service struct {
	dependencies Dependencies
}

// New constructs a control-plane service over the supplied dependencies.
func New(dependencies Dependencies) *Service {
	return &Service{dependencies: dependencies}
}

// now returns the service clock in UTC.
func (service *Service) now() time.Time {
	if service.dependencies.Clock == nil {
		return time.Now().UTC()
	}
	return service.dependencies.Clock().UTC()
}
