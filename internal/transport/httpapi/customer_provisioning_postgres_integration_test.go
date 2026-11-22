//go:build integration

package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/stores"
)

// firstPurchaseCustomerIDPattern matches the internal id a first purchase gives its new customer.
var firstPurchaseCustomerIDPattern = regexp.MustCompile(`^cust-[0-9a-f]{32}$`)

// firstPurchaseFixture is a catalog and customer session with no customer provisioned.
type firstPurchaseFixture struct {
	*apiIntegrationFixture
	admin   string
	session string
}

// firstPurchaseRestore is the restore response shape the first-purchase tests read.
type firstPurchaseRestore struct {
	Results []verificationResponse `json:"results"`
}

// firstPurchaseSnapshot is the entitlement GET response shape the first-purchase tests read.
type firstPurchaseSnapshot struct {
	CustomerID   core.CustomerID       `json:"customer_id"`
	Entitlements []entitlementResponse `json:"entitlements"`
}

// TestFirstPurchaseCreatesUnknownCustomer verifies a host can sell to an external id nobody provisioned.
func TestFirstPurchaseCreatesUnknownCustomer(t *testing.T) {
	fixture := newFirstPurchaseFixture(t)
	// Minting the session does not create the customer, so a host can still attach an alias first.
	fixture.requestJSON(t, http.MethodGet, firstPurchaseEntitlementsPath(), fixture.session, nil, http.StatusNotFound, nil)

	var first verificationResponse
	fixture.requestJSON(t, http.MethodPost, firstPurchasePath("purchases:verify"), fixture.session,
		firstPurchaseBody(), http.StatusOK, &first)
	if !firstPurchaseCustomerIDPattern.MatchString(string(first.CustomerID)) {
		t.Fatalf("created customer id = %q, want a generated cust- id", first.CustomerID)
	}
	assertFirstPurchaseEntitlement(t, first.CustomerID, first.CustomerID, first.Entitlements)
	var replay verificationResponse
	fixture.requestJSON(t, http.MethodPost, firstPurchasePath("purchases:verify"), fixture.session,
		firstPurchaseBody(), http.StatusOK, &replay)
	assertFirstPurchaseEntitlement(t, first.CustomerID, replay.CustomerID, replay.Entitlements)

	var snapshot firstPurchaseSnapshot
	fixture.requestJSON(t, http.MethodGet, firstPurchaseEntitlementsPath(), fixture.session, nil, http.StatusOK, &snapshot)
	assertFirstPurchaseEntitlement(t, first.CustomerID, snapshot.CustomerID, snapshot.Entitlements)
	assertFirstPurchaseCustomerCount(t, fixture, 1)

	// A PUT never moves an external id: another internal id conflicts, the stored one replays.
	customersPath := "/v1/admin/projects/" + apiIntegrationProjectID + "/customers/"
	fixture.requestJSON(t, http.MethodPut, customersPath+"chosen-customer-id", fixture.admin,
		customerRequest{ExternalID: apiIntegrationExternalCustomerID}, http.StatusConflict, nil)
	fixture.requestJSON(t, http.MethodPut, customersPath+string(first.CustomerID), fixture.admin,
		customerRequest{ExternalID: apiIntegrationExternalCustomerID}, http.StatusOK, nil)
	assertFirstPurchaseCustomerCount(t, fixture, 1)
}

// TestFirstRestoreCreatesUnknownCustomer verifies restore creates the customer like verify.
func TestFirstRestoreCreatesUnknownCustomer(t *testing.T) {
	fixture := newFirstPurchaseFixture(t)

	var restored firstPurchaseRestore
	fixture.requestJSON(t, http.MethodPost, firstPurchasePath("purchases:restore"), fixture.session,
		map[string]any{"purchases": []any{firstPurchaseBody()}}, http.StatusOK, &restored)
	if len(restored.Results) != 1 || !firstPurchaseCustomerIDPattern.MatchString(string(restored.Results[0].CustomerID)) {
		t.Fatalf("restore results = %#v, want one purchase on a created customer", restored.Results)
	}
	customerID := restored.Results[0].CustomerID
	assertFirstPurchaseEntitlement(t, customerID, customerID, restored.Results[0].Entitlements)

	var snapshot firstPurchaseSnapshot
	fixture.requestJSON(t, http.MethodGet, firstPurchaseEntitlementsPath(), fixture.session, nil, http.StatusOK, &snapshot)
	assertFirstPurchaseEntitlement(t, customerID, snapshot.CustomerID, snapshot.Entitlements)
	assertFirstPurchaseCustomerCount(t, fixture, 1)
}

// TestFirstPurchaseUsesAliasAttachedBeforehand verifies an alias attached before the first purchase keeps its owner.
func TestFirstPurchaseUsesAliasAttachedBeforehand(t *testing.T) {
	fixture := newFirstPurchaseFixture(t)
	owner := "/v1/admin/projects/" + apiIntegrationProjectID + "/customers/" + apiIntegrationCustomerID
	fixture.requestJSON(t, http.MethodPut, owner, fixture.admin,
		customerRequest{ExternalID: "http-owner-external"}, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut, owner+"/aliases/"+apiIntegrationExternalCustomerID, fixture.admin,
		map[string]string{}, http.StatusOK, nil)

	var verified verificationResponse
	fixture.requestJSON(t, http.MethodPost, firstPurchasePath("purchases:verify"), fixture.session,
		firstPurchaseBody(), http.StatusOK, &verified)
	assertFirstPurchaseEntitlement(t, apiIntegrationCustomerID, verified.CustomerID, verified.Entitlements)
	assertFirstPurchaseCustomerCount(t, fixture, 1)
}

// TestFailedFirstPurchaseCreatesNoCustomer verifies a purchase the provider rejects leaves no customer behind.
func TestFailedFirstPurchaseCreatesNoCustomer(t *testing.T) {
	fixture := newFirstPurchaseFixture(t)
	fixture.adapter.verifyErr = stores.NewFailure(
		core.ProviderGooglePlay, "verify", stores.FailureInvalidEvidence, 0, errors.New("purchase is not valid"),
	)

	fixture.requestJSON(t, http.MethodPost, firstPurchasePath("purchases:verify"), fixture.session,
		firstPurchaseBody(), http.StatusUnprocessableEntity, nil)
	fixture.requestJSON(t, http.MethodGet, firstPurchaseEntitlementsPath(), fixture.session, nil, http.StatusNotFound, nil)
	assertFirstPurchaseCustomerCount(t, fixture, 0)
}

// TestConcurrentFirstPurchasesShareOneCustomer verifies racing first purchases for one external id converge.
func TestConcurrentFirstPurchasesShareOneCustomer(t *testing.T) {
	fixture := newFirstPurchaseFixture(t)
	payload, err := json.Marshal(firstPurchaseBody())
	if err != nil {
		t.Fatalf("marshal purchase: %v", err)
	}

	const attempts = 4
	statuses := make([]int, attempts)
	customers := make([]core.CustomerID, attempts)
	failures := make([]error, attempts)
	var group sync.WaitGroup
	for attempt := range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			statuses[attempt], customers[attempt], failures[attempt] = postFirstPurchase(fixture, payload)
		}()
	}
	group.Wait()

	for attempt := range attempts {
		if failures[attempt] != nil || statuses[attempt] != http.StatusOK || customers[attempt] != customers[0] {
			t.Fatalf("attempt %d = (status %d, customer %q, %v), want every purchase on customer %q",
				attempt, statuses[attempt], customers[attempt], failures[attempt], customers[0])
		}
	}
	if !firstPurchaseCustomerIDPattern.MatchString(string(customers[0])) {
		t.Fatalf("shared customer id = %q, want a generated cust- id", customers[0])
	}
	assertFirstPurchaseCustomerCount(t, fixture, 1)
}

// newFirstPurchaseFixture creates the project catalog, an application key, and a customer session,
// but no customer for the session's external id.
func newFirstPurchaseFixture(t *testing.T) firstPurchaseFixture {
	t.Helper()
	fixture := newAPIIntegrationFixture(t)
	// Alias writes need a durable actor, so follow the bootstrap sequence and switch to a stored admin key.
	var adminKey map[string]string
	fixture.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", testBootstrapAdminKey,
		keyRequest{Role: persistence.APIKeyRoleAdmin}, http.StatusCreated, &adminKey)
	admin := adminKey["key"]

	fixture.requestJSON(t, http.MethodPut, "/v1/admin/projects/"+apiIntegrationProjectID, admin, nil, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/applications/"+apiIntegrationApplicationID,
		admin,
		applicationRequest{
			Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest,
			ProviderApplicationID: apiIntegrationProviderApplicationID,
		},
		http.StatusOK,
		nil,
	)
	fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/entitlements/"+apiIntegrationEntitlementID,
		admin, entitlementRequest{Key: "premium"}, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/products/"+apiIntegrationProductID,
		admin,
		productRequest{
			Kind:           core.ProductKindNonConsumable,
			EntitlementIDs: []core.EntitlementID{apiIntegrationEntitlementID},
		},
		http.StatusOK,
		nil,
	)
	fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/applications/"+apiIntegrationApplicationID+
			"/store-products/"+apiIntegrationProviderProductID,
		admin, storeProductRequest{ProductID: apiIntegrationProductID}, http.StatusOK, nil)

	var keyResponse map[string]string
	fixture.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", admin, keyRequest{
		Role: persistence.APIKeyRoleApplication, ProjectID: apiIntegrationProjectID,
		ApplicationID: apiIntegrationApplicationID,
	}, http.StatusCreated, &keyResponse)
	var session customerSessionResponse
	fixture.requestJSON(t, http.MethodPost, firstPurchasePath("customer-sessions"), keyResponse["key"],
		customerSessionRequest{ExternalCustomerID: apiIntegrationExternalCustomerID}, http.StatusCreated, &session)
	return firstPurchaseFixture{apiIntegrationFixture: fixture, admin: admin, session: session.Token}
}

// firstPurchasePath returns one application route under the integration application.
func firstPurchasePath(operation string) string {
	return "/v1/applications/" + apiIntegrationApplicationID + "/" + operation
}

// firstPurchaseEntitlementsPath returns the session customer's entitlement route.
func firstPurchaseEntitlementsPath() string {
	return firstPurchasePath("customers/" + apiIntegrationExternalCustomerID + "/entitlements")
}

// firstPurchaseBody returns the purchase the fake provider verifies for the session customer.
func firstPurchaseBody() map[string]any {
	return map[string]any{
		"external_customer_id": apiIntegrationExternalCustomerID,
		"claimed_products":     []string{apiIntegrationProviderProductID},
		"evidence": map[string]string{
			"purchase_token": apiIntegrationPurchaseToken,
			"product_kind":   "non_consumable",
		},
	}
}

// postFirstPurchase sends one verify request without failing the test from a worker goroutine.
func postFirstPurchase(fixture firstPurchaseFixture, payload []byte) (int, core.CustomerID, error) {
	request, err := http.NewRequest(http.MethodPost, fixture.server.URL+firstPurchasePath("purchases:verify"), bytes.NewReader(payload))
	if err != nil {
		return 0, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+fixture.session)
	response, err := fixture.client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, "", err
	}
	var verified verificationResponse
	if response.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &verified); err != nil {
			return response.StatusCode, "", err
		}
	}
	return response.StatusCode, verified.CustomerID, nil
}

// assertFirstPurchaseEntitlement verifies the purchase landed on the expected customer.
func assertFirstPurchaseEntitlement(
	t *testing.T,
	want core.CustomerID,
	customerID core.CustomerID,
	entitlements []entitlementResponse,
) {
	t.Helper()
	if customerID != want || len(entitlements) != 1 {
		t.Fatalf("entitlement snapshot = customer %q, values %#v, want customer %q", customerID, entitlements, want)
	}
	if entitlements[0].Key != "premium" || entitlements[0].Access != core.AccessAllowed {
		t.Fatalf("entitlement = %#v", entitlements[0])
	}
}

// assertFirstPurchaseCustomerCount verifies how many customers the project holds.
func assertFirstPurchaseCustomerCount(t *testing.T, fixture firstPurchaseFixture, want int64) {
	t.Helper()
	var projects adminProjectsResponse
	fixture.requestJSON(t, http.MethodGet, "/v1/admin/projects", fixture.admin, nil, http.StatusOK, &projects)
	if len(projects.Projects) != 1 || projects.Projects[0].CustomerCount != want {
		t.Fatalf("admin projects = %#v, want %d customers", projects.Projects, want)
	}
}
