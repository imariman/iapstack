package persistence

import (
	"fmt"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// MaxReprojectionPageSize bounds one keyset page of canonical customers.
	MaxReprojectionPageSize = 1000
)

// ReprojectionPage selects one keyset page of canonical customers to reproject in one project.
type ReprojectionPage struct {
	ProjectID core.ProjectID
	// AfterCustomerID is the last customer id of the previous page, or empty for the first page.
	AfterCustomerID core.CustomerID
	// Limit is the page size, from 1 to MaxReprojectionPageSize.
	Limit int
	// AllCustomers selects every canonical customer instead of only those whose closure holds an
	// allowed import grant and an allowed store observation of another application product for one
	// entitlement, the only pair whose ranking changed.
	AllCustomers bool
}

// Validate checks the project, cursor, and page size.
func (page ReprojectionPage) Validate() error {
	if err := page.ProjectID.Validate(); err != nil {
		return err
	}
	if page.AfterCustomerID != "" {
		if err := page.AfterCustomerID.Validate(); err != nil {
			return err
		}
	}
	if page.Limit < 1 || page.Limit > MaxReprojectionPageSize {
		return fmt.Errorf("reprojection page size must be between 1 and %d", MaxReprojectionPageSize)
	}
	return nil
}
