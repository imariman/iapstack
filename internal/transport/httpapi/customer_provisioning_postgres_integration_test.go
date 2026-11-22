//go:build integration

package httpapi

import (
	"net/http"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
)

// TestFirstPurchaseCreatesUnknownCustomer verifies a host can sell to an external id nobody provisioned.
func TestFirstPurchaseCreatesUnknownCustomer(t *testing.T) {
	fixture := newAPIIntegrationFixture(t)
	adminBearer := testBootstrapAdminKey
	derivedCustomerID := core.DerivedCustomerID(apiIntegrationProjectID, apiIntegrationExternalCustomerID)

	fixture.requestJSON(t, http.MethodPut, "/v1/admin/projects/"+apiIntegrationProjectID, adminBearer, nil, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/applications/"+apiIntegrationApplicationID,
		adminBearer,
		applicationRequest{
			Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest,
			ProviderApplicationID: apiIntegrationProviderApplicationID,
		},
		http.StatusOK,
		nil,
	)
	fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/entitlements/"+apiIntegrationEntitlementID,
		adminBearer,
		entitlementRequest{Key: "premium"},
		http.StatusOK,
		nil,
	)
	fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/products/"+apiIntegrationProductID,
		adminBearer,
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
		adminBearer,
		storeProductRequest{ProductID: apiIntegrationProductID},
		http.StatusOK,
		nil,
	)
	var keyResponse map[string]string
	fixture.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", adminBearer, keyRequest{
		Role: persistence.APIKeyRoleApplication, ProjectID: apiIntegrationProjectID,
		ApplicationID: apiIntegrationApplicationID,
	}, http.StatusCreated, &keyResponse)

	var session customerSessionResponse
	fixture.requestJSON(t, http.MethodPost,
		"/v1/applications/"+apiIntegrationApplicationID+"/customer-sessions",
		keyResponse["key"],
		customerSessionRequest{ExternalCustomerID: apiIntegrationExternalCustomerID},
		http.StatusCreated,
		&session,
	)
	entitlementsPath := "/v1/applications/" + apiIntegrationApplicationID + "/customers/" +
		apiIntegrationExternalCustomerID + "/entitlements"
	// Minting the session does not create the customer, so a host can still attach an alias first.
	fixture.requestJSON(t, http.MethodGet, entitlementsPath, session.Token, nil, http.StatusNotFound, nil)

	purchase := map[string]any{
		"external_customer_id": apiIntegrationExternalCustomerID,
		"claimed_products":     []string{apiIntegrationProviderProductID},
		"evidence": map[string]string{
			"purchase_token": apiIntegrationPurchaseToken,
			"product_kind":   "non_consumable",
		},
	}
	for attempt := 1; attempt <= 2; attempt++ {
		var verified verificationResponse
		fixture.requestJSON(t, http.MethodPost,
			"/v1/applications/"+apiIntegrationApplicationID+"/purchases:verify",
			session.Token,
			purchase,
			http.StatusOK,
			&verified,
		)
		assertProvisionedCustomerEntitlement(t, derivedCustomerID, verified.CustomerID, verified.Entitlements)
	}

	var snapshot struct {
		CustomerID   core.CustomerID       `json:"customer_id"`
		Entitlements []entitlementResponse `json:"entitlements"`
	}
	fixture.requestJSON(t, http.MethodGet, entitlementsPath, session.Token, nil, http.StatusOK, &snapshot)
	assertProvisionedCustomerEntitlement(t, derivedCustomerID, snapshot.CustomerID, snapshot.Entitlements)

	var customer map[string]string
	fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/customers/"+string(derivedCustomerID),
		adminBearer,
		customerRequest{ExternalID: apiIntegrationExternalCustomerID},
		http.StatusOK,
		&customer,
	)
	if customer["id"] != string(derivedCustomerID) || customer["external_id"] != apiIntegrationExternalCustomerID {
		t.Fatalf("provisioned customer = %#v, want %q for %q", customer, derivedCustomerID, apiIntegrationExternalCustomerID)
	}
}

// assertProvisionedCustomerEntitlement verifies the purchase landed on the customer the first verification created.
func assertProvisionedCustomerEntitlement(
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
