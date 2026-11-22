package httpapi

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/verification"
)

// provisioningProjectID is the project every provisioning test customer belongs to.
const provisioningProjectID core.ProjectID = "project-1"

// provisioningRepository resolves and creates customers against deterministic test state.
type provisioningRepository struct {
	persistence.OperationsTransaction
	customers         map[string]core.CustomerID
	aliases           map[string]core.CustomerID
	lookupErr         error
	putErr            error
	aliasDuringCreate core.CustomerID
	lookups           int
	puts              []core.Customer
}

// provisioningOperations runs provisioning callbacks against one repository.
type provisioningOperations struct {
	repository *provisioningRepository
	err        error
	calls      int
}

// recordingCustomerProvisioner records provisioning requests and the order they ran in.
type recordingCustomerProvisioner struct {
	err   error
	calls []string
	order *[]string
}

// orderedVerifier records when verification runs relative to provisioning.
type orderedVerifier struct {
	verifier *scriptedPurchaseVerifier
	order    *[]string
}

// TestEnsureCustomerCreatesUnknownExternalID verifies the first purchase creates its customer.
func TestEnsureCustomerCreatesUnknownExternalID(t *testing.T) {
	t.Parallel()

	repository := newProvisioningRepository()
	if err := ensureCustomer(context.Background(), repository, provisioningProjectID, "hwid_new"); err != nil {
		t.Fatalf("ensureCustomer() error = %v", err)
	}
	want := []core.Customer{{
		ID:         core.DerivedCustomerID(provisioningProjectID, "hwid_new"),
		ProjectID:  provisioningProjectID,
		ExternalID: "hwid_new",
	}}
	if !reflect.DeepEqual(repository.puts, want) {
		t.Fatalf("PutCustomer calls = %#v, want %#v", repository.puts, want)
	}
	if err := ensureCustomer(context.Background(), repository, provisioningProjectID, "hwid_new"); err != nil {
		t.Fatalf("second ensureCustomer() error = %v", err)
	}
	if len(repository.puts) != 1 {
		t.Fatalf("PutCustomer calls after replay = %d, want 1", len(repository.puts))
	}
}

// TestEnsureCustomerLeavesKnownCustomersAndAliases verifies existing identities are never re-created.
func TestEnsureCustomerLeavesKnownCustomersAndAliases(t *testing.T) {
	t.Parallel()

	for _, externalID := range []string{"customer-primary", "customer-alias"} {
		repository := newProvisioningRepository()
		repository.customers["customer-primary"] = "customer-1"
		repository.aliases["customer-alias"] = "customer-1"
		if err := ensureCustomer(context.Background(), repository, provisioningProjectID, externalID); err != nil {
			t.Fatalf("ensureCustomer(%q) error = %v", externalID, err)
		}
		if len(repository.puts) != 0 {
			t.Fatalf("ensureCustomer(%q) PutCustomer calls = %#v, want none", externalID, repository.puts)
		}
	}
}

// TestEnsureCustomerAcceptsAliasAttachedDuringCreation verifies a racing alias wins over a new customer.
func TestEnsureCustomerAcceptsAliasAttachedDuringCreation(t *testing.T) {
	t.Parallel()

	repository := newProvisioningRepository()
	repository.aliasDuringCreate = "customer-1"
	if err := ensureCustomer(context.Background(), repository, provisioningProjectID, "late-alias"); err != nil {
		t.Fatalf("ensureCustomer() error = %v, want the alias owner", err)
	}
	if len(repository.puts) != 1 || repository.lookups != 2 {
		t.Fatalf("calls = (put %d, lookup %d), want one refused create and a second lookup",
			len(repository.puts), repository.lookups)
	}
}

// TestEnsureCustomerReportsStoreFailures verifies lookup and write failures stop the purchase.
func TestEnsureCustomerReportsStoreFailures(t *testing.T) {
	t.Parallel()

	failing := newProvisioningRepository()
	failing.lookupErr = persistence.ErrUnavailable
	if err := ensureCustomer(context.Background(), failing, provisioningProjectID, "hwid_new"); !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("lookup failure = %v, want ErrUnavailable", err)
	}
	if len(failing.puts) != 0 {
		t.Fatalf("PutCustomer after a failed lookup = %#v, want none", failing.puts)
	}

	conflicting := newProvisioningRepository()
	conflicting.putErr = persistence.ErrConflict
	if err := ensureCustomer(context.Background(), conflicting, provisioningProjectID, "hwid_new"); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("conflict without an alias = %v, want ErrConflict", err)
	}

	operations := &provisioningOperations{repository: newProvisioningRepository(), err: persistence.ErrUnavailable}
	provisioner := operationsCustomerProvisioner{operations: operations}
	if err := provisioner.EnsureCustomer(context.Background(), provisioningProjectID, "hwid_new"); !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("operations failure = %v, want ErrUnavailable", err)
	}
}

// TestOperationsCustomerProvisionerCreatesCustomer verifies the production provisioner uses one operations transaction.
func TestOperationsCustomerProvisionerCreatesCustomer(t *testing.T) {
	t.Parallel()

	operations := &provisioningOperations{repository: newProvisioningRepository()}
	provisioner := operationsCustomerProvisioner{operations: operations}
	if err := provisioner.EnsureCustomer(context.Background(), provisioningProjectID, "hwid_new"); err != nil {
		t.Fatalf("EnsureCustomer() error = %v", err)
	}
	if operations.calls != 1 || len(operations.repository.puts) != 1 {
		t.Fatalf("calls = (operate %d, put %d), want one transaction that creates the customer",
			operations.calls, len(operations.repository.puts))
	}
}

// TestVerifyProvisionsCustomerBeforeVerification verifies the customer exists before the purchase is verified.
func TestVerifyProvisionsCustomerBeforeVerification(t *testing.T) {
	t.Parallel()

	api, verifier, _, _, _ := newVerifyScheduleAPI(t, nil, nil, nil)
	order := []string{}
	provisioner := &recordingCustomerProvisioner{order: &order}
	api.customers = provisioner
	api.verification = orderedVerifier{verifier: verifier, order: &order}
	input := subscriptionVerificationRequest("token-1")
	if _, err := api.verify(context.Background(), verifySchedulePrincipal(), input); err != nil {
		t.Fatalf("verify() error = %v", err)
	}
	if !reflect.DeepEqual(provisioner.calls, []string{"project-1/customer-external"}) {
		t.Fatalf("provisioning calls = %#v, want the session customer", provisioner.calls)
	}
	if !reflect.DeepEqual(order, []string{"provision", "verify"}) {
		t.Fatalf("call order = %#v, want provisioning before verification", order)
	}

	failing, failingVerifier, _, _, _ := newVerifyScheduleAPI(t, nil, nil, nil)
	failing.customers = &recordingCustomerProvisioner{err: persistence.ErrUnavailable}
	if _, err := failing.verify(context.Background(), verifySchedulePrincipal(), input); !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("verify() error = %v, want ErrUnavailable", err)
	}
	if len(failingVerifier.commands) != 0 {
		t.Fatalf("verifier commands = %d, want none after a provisioning failure", len(failingVerifier.commands))
	}
}

// newProvisioningRepository returns an empty project with no customers or aliases.
func newProvisioningRepository() *provisioningRepository {
	return &provisioningRepository{
		customers: make(map[string]core.CustomerID),
		aliases:   make(map[string]core.CustomerID),
	}
}

// CustomerByExternalID resolves a primary or alias external id.
func (repository *provisioningRepository) CustomerByExternalID(
	_ context.Context,
	projectID core.ProjectID,
	externalID string,
) (core.Customer, error) {
	repository.lookups++
	if repository.lookupErr != nil {
		return core.Customer{}, repository.lookupErr
	}
	if id, exists := repository.customers[externalID]; exists {
		return core.Customer{ID: id, ProjectID: projectID, ExternalID: externalID}, nil
	}
	if id, exists := repository.aliases[externalID]; exists {
		return core.Customer{ID: id, ProjectID: projectID}, nil
	}
	return core.Customer{}, persistence.ErrNotFound
}

// PutCustomer records one create request and applies it unless a failure or racing alias is scripted.
func (repository *provisioningRepository) PutCustomer(_ context.Context, customer core.Customer) (core.Customer, error) {
	repository.puts = append(repository.puts, customer)
	if repository.aliasDuringCreate != "" {
		repository.aliases[customer.ExternalID] = repository.aliasDuringCreate
		return core.Customer{}, persistence.ErrConflict
	}
	if repository.putErr != nil {
		return core.Customer{}, repository.putErr
	}
	repository.customers[customer.ExternalID] = customer.ID
	return customer, nil
}

// Operate runs one provisioning callback unless a storage failure is scripted.
func (operations *provisioningOperations) Operate(_ context.Context, operation persistence.OperationsFunc) error {
	operations.calls++
	if operations.err != nil {
		return operations.err
	}
	return operation(operations.repository)
}

// EnsureCustomer records one provisioning request.
func (provisioner *recordingCustomerProvisioner) EnsureCustomer(
	_ context.Context,
	projectID core.ProjectID,
	externalID string,
) error {
	provisioner.calls = append(provisioner.calls, string(projectID)+"/"+externalID)
	if provisioner.order != nil {
		*provisioner.order = append(*provisioner.order, "provision")
	}
	return provisioner.err
}

// Verify records the verification step and delegates to the scripted verifier.
func (verifier orderedVerifier) Verify(ctx context.Context, command verification.Command) (verification.Result, error) {
	*verifier.order = append(*verifier.order, "verify")
	return verifier.verifier.Verify(ctx, command)
}
