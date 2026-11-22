package httpapi

import (
	"context"
	"errors"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// customerProvisioner makes sure a customer session's external id names a customer before
// IAPStack stores the first purchase for it.
type customerProvisioner interface {
	// EnsureCustomer creates the customer for an unknown external id and leaves a known
	// customer or alias unchanged.
	EnsureCustomer(context.Context, core.ProjectID, string) error
}

// customerProvisioningRepository is the part of the operations transaction that
// provisioning reads and writes.
type customerProvisioningRepository interface {
	// CustomerByExternalID resolves one external id, primary or alias, to its canonical customer.
	CustomerByExternalID(context.Context, core.ProjectID, string) (core.Customer, error)
	// PutCustomer creates one external customer identity or returns its existing record.
	PutCustomer(context.Context, core.Customer) (core.Customer, error)
}

// operationsCustomerProvisioner creates first-purchase customers through the control-plane store.
type operationsCustomerProvisioner struct {
	operations persistence.OperationsStore
}

// EnsureCustomer runs the provisioning check in one retry-safe operations transaction.
func (provisioner operationsCustomerProvisioner) EnsureCustomer(
	ctx context.Context,
	projectID core.ProjectID,
	externalID string,
) error {
	return provisioner.operations.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		return ensureCustomer(ctx, repository, projectID, externalID)
	})
}

// ensureCustomer creates the customer for externalID under its derived internal id unless
// the id already resolves to a customer, directly or as an alias. Customer sessions never
// create customers, so a host can still mint a session before it attaches an alias; only
// the first purchase makes the external id a customer of its own.
func ensureCustomer(
	ctx context.Context,
	repository customerProvisioningRepository,
	projectID core.ProjectID,
	externalID string,
) error {
	_, err := repository.CustomerByExternalID(ctx, projectID, externalID)
	if !errors.Is(err, persistence.ErrNotFound) {
		return err
	}
	_, err = repository.PutCustomer(ctx, core.Customer{
		ID:         core.DerivedCustomerID(projectID, externalID),
		ProjectID:  projectID,
		ExternalID: externalID,
	})
	if !errors.Is(err, persistence.ErrConflict) {
		return err
	}
	// PutCustomer refuses an external id that became an alias after the lookup above. The
	// alias owner is then the customer; any other conflict stays a conflict.
	if _, lookupErr := repository.CustomerByExternalID(ctx, projectID, externalID); lookupErr == nil {
		return nil
	}
	return err
}
