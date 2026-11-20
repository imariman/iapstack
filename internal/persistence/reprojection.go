package persistence

import (
	"fmt"

	"github.com/imariman/iapstack/internal/core"
)

const (
	// MaxReprojectionPageSize bounds the canonical customers one keyset page examines.
	MaxReprojectionPageSize = 1000
)

// ReprojectionPage examines one keyset page of canonical customers to reproject in one project.
type ReprojectionPage struct {
	ProjectID core.ProjectID
	// AfterCustomerID is the NextAfterCustomerID of the previous page, or empty for the first page.
	AfterCustomerID core.CustomerID
	// Limit is the number of canonical customers the page examines, from 1 to
	// MaxReprojectionPageSize. The filter runs only on those, so one page stays bounded however
	// few customers match.
	Limit int
	// AllCustomers selects every canonical customer instead of only those whose closure holds an
	// allowed import grant and an allowed store observation of another application product for one
	// entitlement, the only pair whose ranking changed.
	AllCustomers bool
}

// ReprojectionPageResult is one examined page of canonical customers.
type ReprojectionPageResult struct {
	// CustomerIDs are the examined customers the page selects, in id order.
	CustomerIDs []core.CustomerID
	// NextAfterCustomerID is the last customer id the page examined, or empty when the page
	// reached the end of the project.
	NextAfterCustomerID core.CustomerID
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
