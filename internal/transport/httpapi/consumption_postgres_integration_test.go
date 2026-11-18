//go:build integration

package httpapi

import (
	"crypto/sha256"
	"net/http"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

const (
	// consumptionHTTPApplicationID identifies the App Store application in consumption HTTP tests.
	consumptionHTTPApplicationID = "http-apple-application-1"
	// consumptionHTTPExternalCustomerID is the UUID external id an App Store purchase binds to.
	consumptionHTTPExternalCustomerID = "4f753b4a-32c1-4fe4-a8e6-06efb0b35db7"
)

// TestAppleConsumptionSettingsAndConsentHTTP verifies the operator settings, consent route, and request list.
func TestAppleConsumptionSettingsAndConsentHTTP(t *testing.T) {
	fixture := newAPIIntegrationFixture(t)
	admin := testBootstrapAdminKey
	project := "/v1/admin/projects/" + apiIntegrationProjectID
	fixture.requestJSON(t, http.MethodPut, project, admin, map[string]string{}, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut, project+"/applications/"+consumptionHTTPApplicationID, admin, applicationRequest{
		Provider: core.ProviderAppleAppStore, Environment: core.EnvironmentSandbox, ProviderApplicationID: "com.example.iapstack",
	}, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut, project+"/applications/"+apiIntegrationApplicationID, admin, applicationRequest{
		Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest, ProviderApplicationID: apiIntegrationProviderApplicationID,
	}, http.StatusOK, nil)
	fixture.requestJSON(t, http.MethodPut, project+"/customers/"+apiIntegrationCustomerID, admin,
		customerRequest{ExternalID: consumptionHTTPExternalCustomerID}, http.StatusOK, nil)

	settingsPath := project + "/applications/" + consumptionHTTPApplicationID + "/apple-consumption"
	var initial appleConsumptionStatusResponse
	fixture.requestJSON(t, http.MethodGet, settingsPath, admin, nil, http.StatusOK, &initial)
	if initial.Settings.Enabled || initial.Settings.Revision != 0 || initial.Settings.ConsentMode != "customer_opt_in" ||
		initial.Settings.RefundPreference != "none" || initial.RecentRequests == nil || len(initial.RecentRequests) != 0 {
		t.Fatalf("default consumption status = %#v", initial)
	}
	fixture.requestJSON(t, http.MethodGet, project+"/applications/"+apiIntegrationApplicationID+"/apple-consumption",
		admin, nil, http.StatusBadRequest, nil)
	fixture.requestJSON(t, http.MethodPut, settingsPath, admin, map[string]any{
		"consent_mode": "customer_opt_in", "refund_preference": "none", "expected_revision": 0,
	}, http.StatusBadRequest, nil)
	fixture.requestJSON(t, http.MethodPut, settingsPath, admin, map[string]any{
		"enabled": true, "consent_mode": "everyone", "refund_preference": "none",
		"sample_content_provided": false, "expected_revision": 0,
	}, http.StatusBadRequest, nil)
	var saved appleConsumptionSettingsResponse
	fixture.requestJSON(t, http.MethodPut, settingsPath, admin, map[string]any{
		"enabled": true, "consent_mode": "application_attested", "refund_preference": "grant_full",
		"sample_content_provided": true, "expected_revision": 0,
	}, http.StatusOK, &saved)
	if !saved.Enabled || saved.Revision != 1 || saved.ConsentMode != "application_attested" || saved.UpdatedAt == nil {
		t.Fatalf("saved settings = %#v", saved)
	}
	fixture.requestJSON(t, http.MethodPut, settingsPath, admin, map[string]any{
		"enabled": false, "consent_mode": "application_attested", "refund_preference": "grant_full",
		"sample_content_provided": true, "expected_revision": 0,
	}, http.StatusConflict, nil)

	var applicationKey map[string]string
	fixture.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", admin, keyRequest{
		Role: persistence.APIKeyRoleApplication, ProjectID: apiIntegrationProjectID, ApplicationID: consumptionHTTPApplicationID,
	}, http.StatusCreated, &applicationKey)
	consentPath := "/v1/applications/" + consumptionHTTPApplicationID + "/customers/" + consumptionHTTPExternalCustomerID +
		"/apple-consumption-consent"
	var consent appleConsumptionConsentResponse
	fixture.requestJSON(t, http.MethodPut, consentPath, applicationKey["key"], map[string]bool{"consented": false}, http.StatusOK, &consent)
	if consent.Consented || consent.CustomerID != apiIntegrationCustomerID || consent.ExternalCustomerID != consumptionHTTPExternalCustomerID {
		t.Fatalf("consent = %#v", consent)
	}
	fixture.requestJSON(t, http.MethodPut, consentPath, applicationKey["key"], map[string]bool{"consented": true}, http.StatusOK, &consent)
	if !consent.Consented {
		t.Fatalf("updated consent = %#v", consent)
	}
	fixture.requestJSON(t, http.MethodPut, consentPath, applicationKey["key"], map[string]any{}, http.StatusBadRequest, nil)
	fixture.requestJSON(t, http.MethodPut, consentPath, admin, map[string]bool{"consented": true}, http.StatusUnauthorized, nil)
	fixture.requestJSON(t, http.MethodPut,
		"/v1/applications/"+consumptionHTTPApplicationID+"/customers/unknown-customer/apple-consumption-consent",
		applicationKey["key"], map[string]bool{"consented": true}, http.StatusNotFound, nil)

	signedAt := fixture.now.Add(-time.Hour)
	payload := []byte("protected-consumption-payload")
	if err := fixture.database.store.Operate(t.Context(), func(repository persistence.OperationsTransaction) error {
		_, saveErr := repository.SaveAppleConsumptionRequest(t.Context(), persistence.AppleConsumptionRequest{
			ID: "consumption-http-1", ProjectID: apiIntegrationProjectID, ApplicationID: consumptionHTTPApplicationID,
			CustomerID: apiIntegrationCustomerID, ProviderProductID: "iapstack.pro", ProductType: "auto_renewable_subscription",
			CustomerReason: "unintended_purchase",
			Payload:        protection.Value{Ciphertext: payload, Fingerprint: sha256.Sum256(payload), KeyID: "http-integration-key"},
			SignedAt:       signedAt, ReceivedAt: signedAt, DeadlineAt: signedAt.Add(12 * time.Hour), AvailableAt: signedAt,
		})
		return saveErr
	}); err != nil {
		t.Fatalf("SaveAppleConsumptionRequest() error = %v", err)
	}
	var listed appleConsumptionStatusResponse
	body := fixture.requestJSON(t, http.MethodGet, settingsPath, admin, nil, http.StatusOK, &listed)
	if len(listed.RecentRequests) != 1 || listed.RecentRequests[0].Status != "pending" ||
		listed.RecentRequests[0].CustomerReason != "unintended_purchase" || listed.Settings.Revision != 1 {
		t.Fatalf("listed status = %#v", listed)
	}
	if containsBytes(body, payload) {
		t.Fatal("consumption status exposes the protected payload")
	}
}

// containsBytes reports whether one response body contains a forbidden byte sequence.
func containsBytes(body, value []byte) bool {
	for index := 0; index+len(value) <= len(body); index++ {
		if string(body[index:index+len(value)]) == string(value) {
			return true
		}
	}
	return false
}
