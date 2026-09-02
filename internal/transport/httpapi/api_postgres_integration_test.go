//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/credentials"
	"github.com/imariman/iapstack/internal/jobs"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	platformprotection "github.com/imariman/iapstack/internal/platform/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/apple"
	"github.com/imariman/iapstack/internal/stores/googleplay"
	"github.com/imariman/iapstack/internal/stores/huawei"
	"github.com/imariman/iapstack/internal/verification"
	"github.com/imariman/iapstack/internal/webhooks"
	"github.com/jackc/pgx/v5"
)

const (
	// apiIntegrationDatabaseURLKey names the opt-in PostgreSQL connection setting.
	apiIntegrationDatabaseURLKey = "IAPSTACK_TEST_DATABASE_URL"
	// apiIntegrationDatabaseTimeout bounds setup, execution, and cleanup.
	apiIntegrationDatabaseTimeout = 30 * time.Second
	// apiIntegrationDatabaseAdvisoryLock serializes schema-resetting integration test packages.
	apiIntegrationDatabaseAdvisoryLock int64 = 424090117
	// apiIntegrationProjectID identifies the project created through the HTTP API.
	apiIntegrationProjectID = "http-project-1"
	// apiIntegrationApplicationID identifies the Google Play application created through the HTTP API.
	apiIntegrationApplicationID = "http-application-1"
	// apiIntegrationProviderApplicationID identifies the provider application scope.
	apiIntegrationProviderApplicationID = "com.example.iapstack.integration"
	// apiIntegrationCustomerID identifies the customer created through the HTTP API.
	apiIntegrationCustomerID = "http-customer-1"
	// apiIntegrationExternalCustomerID identifies the customer exposed to application clients.
	apiIntegrationExternalCustomerID = "http-external-customer-1"
	// apiIntegrationEntitlementID identifies the catalog entitlement created through the HTTP API.
	apiIntegrationEntitlementID = "http-entitlement-1"
	// apiIntegrationProductID identifies the catalog product created through the HTTP API.
	apiIntegrationProductID = "http-product-1"
	// apiIntegrationProviderProductID identifies the Google Play product submitted for verification.
	apiIntegrationProviderProductID = "premium_lifetime"
	// apiIntegrationObservationID identifies the deterministic provider observation.
	apiIntegrationObservationID = "http-observation-1"
	// apiIntegrationPurchaseToken identifies the private client evidence used by the fake adapter.
	apiIntegrationPurchaseToken = "http-purchase-token-1"
	// apiIntegrationTransactionID identifies the private provider transaction reference.
	apiIntegrationTransactionID = "GPA.1111-2222-3333-44444"
	// apiIntegrationCredentialSecret identifies test-only credential material that must not be returned.
	apiIntegrationCredentialSecret = "integration-private-key-material"
	// apiIntegrationWebhookSecret identifies test-only webhook material that must not be returned.
	apiIntegrationWebhookSecret = "integration-webhook-signing-secret"
)

// apiIntegrationDatabase owns one migrated connection and one repository pool.
type apiIntegrationDatabase struct {
	ctx      context.Context
	conn     *pgx.Conn
	migrator *postgres.Migrator
	store    *postgres.Store
}

// apiIntegrationClock returns one deterministic verification timestamp.
type apiIntegrationClock struct {
	now time.Time
}

// apiIntegrationAdapter records verification requests and returns one deterministic Google Play result.
type apiIntegrationAdapter struct {
	result         stores.VerificationResult
	verifyRequests []stores.VerificationRequest
	reconcileCalls int
}

// apiIntegrationFixture contains the real HTTP, service, and PostgreSQL dependencies under test.
type apiIntegrationFixture struct {
	database *apiIntegrationDatabase
	server   *httptest.Server
	client   *http.Client
	adapter  *apiIntegrationAdapter
	now      time.Time
}

var (
	// apiIntegrationAdapterContract verifies the provider fake implements the complete adapter boundary.
	_ stores.Adapter = (*apiIntegrationAdapter)(nil)
	// apiIntegrationClockContract verifies the fixed clock implements verification time.
	_ verification.Clock = apiIntegrationClock{}
)

// TestAPISuccessPathPersistsAndReturnsPostgreSQLState verifies the complete control-plane and data-plane HTTP journey.
func TestAPISuccessPathPersistsAndReturnsPostgreSQLState(t *testing.T) {
	fixture := newAPIIntegrationFixture(t)
	adminBearer := testBootstrapAdminKey

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
	var customer map[string]string
	fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/customers/"+apiIntegrationCustomerID,
		adminBearer,
		customerRequest{ExternalID: apiIntegrationExternalCustomerID},
		http.StatusOK,
		&customer,
	)
	if customer["id"] != apiIntegrationCustomerID || customer["external_id"] != apiIntegrationExternalCustomerID {
		t.Fatalf("customer response = %#v", customer)
	}
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
			Kind: core.ProductKindNonConsumable,
			EntitlementIDs: []core.EntitlementID{
				apiIntegrationEntitlementID,
			},
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

	credentialPayload := json.RawMessage(fmt.Sprintf(`{
		"client_email":"integration@example.iam.gserviceaccount.com",
		"private_key_id":"integration-key-id",
		"private_key":%q
	}`, apiIntegrationCredentialSecret))
	var credentialMetadata credentials.Metadata
	credentialBody := fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/applications/"+apiIntegrationApplicationID+
			"/credentials/"+string(googleplay.CredentialKind),
		adminBearer,
		credentialRequest{
			ContentType: googleplay.CredentialContentType, SchemaVersion: googleplay.CredentialSchemaVersion,
			ExpectedRevision: 0, Payload: credentialPayload,
		},
		http.StatusOK,
		&credentialMetadata,
	)
	if credentialMetadata.Revision != 1 || credentialMetadata.ApplicationID != apiIntegrationApplicationID {
		t.Fatalf("credential metadata = %#v", credentialMetadata)
	}
	if bytes.Contains(credentialBody, []byte(apiIntegrationCredentialSecret)) {
		t.Fatalf("credential response exposed private material: %s", credentialBody)
	}

	var webhookMetadata struct {
		URL      string `json:"url"`
		Revision int64  `json:"revision"`
	}
	webhookBody := fixture.requestJSON(t, http.MethodPut,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/applications/"+apiIntegrationApplicationID+"/webhook",
		adminBearer,
		webhookRequest{
			URL: "https://hooks.example.com/iapstack", SigningSecret: apiIntegrationWebhookSecret,
			ExpectedRevision: 0,
		},
		http.StatusOK,
		&webhookMetadata,
	)
	if webhookMetadata.Revision != 1 || webhookMetadata.URL != "https://hooks.example.com/iapstack" {
		t.Fatalf("webhook metadata = %#v", webhookMetadata)
	}
	if bytes.Contains(webhookBody, []byte(apiIntegrationWebhookSecret)) {
		t.Fatalf("webhook response exposed signing secret: %s", webhookBody)
	}

	var keyResponse map[string]string
	fixture.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", adminBearer, keyRequest{
		Role: persistence.APIKeyRoleApplication, ProjectID: apiIntegrationProjectID,
		ApplicationID: apiIntegrationApplicationID,
	}, http.StatusCreated, &keyResponse)
	applicationBearer := keyResponse["key"]
	if !strings.HasPrefix(applicationBearer, "iap_") {
		t.Fatalf("application bearer = %q, want generated iap_ key", applicationBearer)
	}

	var session customerSessionResponse
	fixture.requestJSON(t, http.MethodPost,
		"/v1/applications/"+apiIntegrationApplicationID+"/customer-sessions",
		applicationBearer,
		customerSessionRequest{ExternalCustomerID: apiIntegrationExternalCustomerID},
		http.StatusCreated,
		&session,
	)
	if !strings.HasPrefix(session.Token, "iaps_") || !session.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("customer session = %#v", session)
	}

	purchase := jobs.VerificationPayload{
		ExternalCustomerID: apiIntegrationExternalCustomerID,
		ClaimedProducts:    []core.ProviderProductID{apiIntegrationProviderProductID},
		Evidence: json.RawMessage(fmt.Sprintf(
			`{"purchase_token":%q,"product_kind":"non_consumable"}`,
			apiIntegrationPurchaseToken,
		)),
	}
	var verified verificationResponse
	fixture.requestJSON(t, http.MethodPost,
		"/v1/applications/"+apiIntegrationApplicationID+"/purchases:verify",
		session.Token,
		purchase,
		http.StatusOK,
		&verified,
	)
	assertAPIIntegrationEntitlement(t, verified.CustomerID, verified.Entitlements)
	if !verified.VerifiedAt.Equal(fixture.now) {
		t.Fatalf("verified time = %s, want %s", verified.VerifiedAt, fixture.now)
	}

	var restored struct {
		Results []verificationResponse `json:"results"`
	}
	fixture.requestJSON(t, http.MethodPost,
		"/v1/applications/"+apiIntegrationApplicationID+"/purchases:restore",
		session.Token,
		restoreRequest{Purchases: []verificationRequest{purchase}},
		http.StatusOK,
		&restored,
	)
	if len(restored.Results) != 1 {
		t.Fatalf("restore results = %#v, want one result", restored.Results)
	}
	assertAPIIntegrationEntitlement(t, restored.Results[0].CustomerID, restored.Results[0].Entitlements)

	var entitlementSnapshot struct {
		CustomerID   core.CustomerID       `json:"customer_id"`
		Entitlements []entitlementResponse `json:"entitlements"`
	}
	fixture.requestJSON(t, http.MethodGet,
		"/v1/applications/"+apiIntegrationApplicationID+"/customers/"+
			apiIntegrationExternalCustomerID+"/entitlements",
		session.Token,
		nil,
		http.StatusOK,
		&entitlementSnapshot,
	)
	assertAPIIntegrationEntitlement(t, entitlementSnapshot.CustomerID, entitlementSnapshot.Entitlements)

	var projects adminProjectsResponse
	fixture.requestJSON(t, http.MethodGet, "/v1/admin/projects", adminBearer, nil, http.StatusOK, &projects)
	if len(projects.Projects) != 1 || projects.Projects[0].ID != apiIntegrationProjectID ||
		projects.Projects[0].ApplicationCount != 1 || projects.Projects[0].CustomerCount != 1 ||
		projects.Projects[0].ProductCount != 1 {
		t.Fatalf("admin projects = %#v", projects.Projects)
	}

	var keys apiKeyCollectionResponse
	keysBody := fixture.requestJSON(t, http.MethodGet, "/v1/admin/api-keys", adminBearer, nil, http.StatusOK, &keys)
	if len(keys.APIKeys) != 1 || keys.APIKeys[0].ApplicationID != apiIntegrationApplicationID {
		t.Fatalf("API key collection = %#v", keys.APIKeys)
	}
	if bytes.Contains(keysBody, []byte(applicationBearer)) {
		t.Fatalf("API key collection exposed bearer: %s", keysBody)
	}

	var overview adminOverviewResponse
	overviewBody := fixture.requestJSON(t, http.MethodGet,
		"/v1/admin/projects/"+apiIntegrationProjectID+"/overview",
		adminBearer,
		nil,
		http.StatusOK,
		&overview,
	)
	assertAPIIntegrationOverview(t, overview)
	for _, secret := range []string{
		apiIntegrationCredentialSecret,
		apiIntegrationWebhookSecret,
		apiIntegrationPurchaseToken,
		apiIntegrationTransactionID,
	} {
		if bytes.Contains(overviewBody, []byte(secret)) {
			t.Fatalf("admin overview exposed private value %q: %s", secret, overviewBody)
		}
	}

	if len(fixture.adapter.verifyRequests) != 2 || fixture.adapter.reconcileCalls != 0 {
		t.Fatalf("adapter calls = verify %d, reconcile %d", len(fixture.adapter.verifyRequests), fixture.adapter.reconcileCalls)
	}
	for index, request := range fixture.adapter.verifyRequests {
		if request.Application.ID != apiIntegrationApplicationID || request.CustomerID != apiIntegrationCustomerID ||
			len(request.ClaimedProducts) != 1 || request.ClaimedProducts[0] != apiIntegrationProviderProductID ||
			len(request.ExpectedCustomerBindings) != 1 ||
			request.ExpectedCustomerBindings[0].Value() != apiIntegrationExternalCustomerID {
			t.Fatalf("adapter request %d = %#v", index, request)
		}
	}

	assertAPIIntegrationTableCount(t, fixture.database, "application_credentials", 1)
	assertAPIIntegrationTableCount(t, fixture.database, "webhook_endpoints", 1)
	assertAPIIntegrationTableCount(t, fixture.database, "purchase_evidence", 1)
	assertAPIIntegrationTableCount(t, fixture.database, "verified_artifacts", 1)
	assertAPIIntegrationTableCount(t, fixture.database, "purchase_observations", 1)
	assertAPIIntegrationTableCount(t, fixture.database, "customer_entitlements", 1)
	assertAPIIntegrationTableCount(t, fixture.database, "outbox_events", 1)
	assertAPIIntegrationTableCount(t, fixture.database, "reconciliation_jobs", 0)
}

// Provider identifies Google Play as the provider exercised by the HTTP integration fixture.
func (adapter *apiIntegrationAdapter) Provider() core.Provider {
	return core.ProviderGooglePlay
}

// Verify records one immutable request snapshot and returns the configured authoritative result.
func (adapter *apiIntegrationAdapter) Verify(
	_ context.Context,
	request stores.VerificationRequest,
) (stores.VerificationResult, error) {
	request.ClaimedProducts = append([]core.ProviderProductID(nil), request.ClaimedProducts...)
	request.ExpectedCustomerBindings = append([]core.StoreReference(nil), request.ExpectedCustomerBindings...)
	adapter.verifyRequests = append(adapter.verifyRequests, request)
	return adapter.result, nil
}

// Reconcile records an unexpected refresh while still satisfying the complete provider contract.
func (adapter *apiIntegrationAdapter) Reconcile(
	_ context.Context,
	_ stores.ReconciliationRequest,
) (stores.VerificationResult, error) {
	adapter.reconcileCalls++
	return adapter.result, nil
}

// Now returns the deterministic HTTP integration timestamp.
func (clock apiIntegrationClock) Now() time.Time {
	return clock.now
}

// newAPIIntegrationFixture composes the production HTTP services around a real PostgreSQL store.
func newAPIIntegrationFixture(t *testing.T) *apiIntegrationFixture {
	t.Helper()
	database := openAPIIntegrationDatabase(t)
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	keyringConfig, err := platformprotection.NewConfig(
		"http-integration-key",
		map[string][]byte{"http-integration-key": bytes.Repeat([]byte{31}, 32)},
		bytes.Repeat([]byte{47}, 32),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	keyring, err := platformprotection.New(keyringConfig)
	if err != nil {
		t.Fatalf("New() keyring error = %v", err)
	}
	credentialService, err := credentials.NewService(database.store, keyring)
	if err != nil {
		t.Fatalf("NewService() credentials error = %v", err)
	}
	huaweiAdapter, err := huawei.New(credentialService, time.Second, false)
	if err != nil {
		t.Fatalf("New() Huawei adapter error = %v", err)
	}
	appleAdapter, err := apple.New(credentialService, time.Second)
	if err != nil {
		t.Fatalf("New() Apple adapter error = %v", err)
	}
	googlePlayAdapter, err := googleplay.New(credentialService, time.Second)
	if err != nil {
		t.Fatalf("New() Google Play adapter error = %v", err)
	}

	providerResult := newAPIIntegrationProviderResult(t, now)
	adapter := &apiIntegrationAdapter{result: providerResult}
	registry, err := stores.NewRegistry(adapter)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	verificationService, err := verification.NewService(
		database.store,
		registry,
		keyring,
		verification.NewDefaultProjector(),
		apiIntegrationClock{now: now},
	)
	if err != nil {
		t.Fatalf("NewService() verification error = %v", err)
	}
	authentication, err := auth.NewService(database.store, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() authentication error = %v", err)
	}
	adminSessions, err := auth.NewAdminSessions(database.store, keyring)
	if err != nil {
		t.Fatalf("NewAdminSessions() error = %v", err)
	}
	customerSessions, err := auth.NewCustomerSessions(database.store, keyring)
	if err != nil {
		t.Fatalf("NewCustomerSessions() error = %v", err)
	}
	webhookService, err := webhooks.NewService(database.store, keyring, time.Second, false)
	if err != nil {
		t.Fatalf("NewService() webhooks error = %v", err)
	}
	api, err := New(Dependencies{
		Store: database.store, Operations: database.store, Admin: database.store,
		Authentication: authentication, AdminSessions: adminSessions, CustomerSessions: customerSessions,
		Credentials: credentialService, Webhooks: webhookService, Verification: verificationService,
		Huawei: huaweiAdapter, Apple: appleAdapter, GooglePlay: googlePlayAdapter,
		Protection: keyring, BodyLimit: 1 << 20,
	})
	if err != nil {
		t.Fatalf("New() HTTP API error = %v", err)
	}
	api.clock = func() time.Time { return now }
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	return &apiIntegrationFixture{
		database: database, server: server, client: server.Client(), adapter: adapter, now: now,
	}
}

// newAPIIntegrationProviderResult creates one valid non-consumable Google Play purchase result.
func newAPIIntegrationProviderResult(t *testing.T, now time.Time) stores.VerificationResult {
	t.Helper()
	application := core.Application{
		ID: apiIntegrationApplicationID, ProjectID: apiIntegrationProjectID,
		Store: core.StoreApplication{
			Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest,
			ID: apiIntegrationProviderApplicationID,
		},
	}
	artifact := newAPIIntegrationEvidence(t, []byte(`{"source":"google-play-test"}`))
	return stores.VerificationResult{
		VerifiedAt: now,
		Artifacts:  []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: artifact}},
		Observations: []core.PurchaseObservation{{
			ID: apiIntegrationObservationID, ApplicationID: application.ID, Store: application.Store,
			ProductID: apiIntegrationProviderProductID, ProductKind: core.ProductKindNonConsumable,
			State: core.LifecycleActive, ProviderState: "PURCHASED", Access: core.AccessAllowed,
			AccessReason: core.AccessReasonPurchaseValid, Ownership: core.OwnershipPurchased, Quantity: 1,
			Price:      &core.PurchasePrice{Milliunits: 4990, Currency: "USD"},
			OccurredAt: now.Add(-time.Minute), ObservedAt: now,
			EffectivePeriod: core.EffectivePeriod{StartsAt: now.Add(-time.Minute)},
			Renewal:         core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable},
			References: []core.StoreReference{
				newAPIIntegrationReference(t, core.ReferenceQuery, "purchase_token", apiIntegrationPurchaseToken),
				newAPIIntegrationReference(t, core.ReferenceTransaction, "order_id", apiIntegrationTransactionID),
				newAPIIntegrationReference(
					t,
					core.ReferenceCustomerBinding,
					"obfuscated_external_account_id",
					apiIntegrationExternalCustomerID,
				),
			},
		}},
	}
}

// newAPIIntegrationEvidence constructs one validated provider artifact.
func newAPIIntegrationEvidence(t *testing.T, payload []byte) stores.Evidence {
	t.Helper()
	evidence, err := stores.NewEvidence("application/json", payload)
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	return evidence
}

// newAPIIntegrationReference constructs one valid private provider reference.
func newAPIIntegrationReference(
	t *testing.T,
	role core.ReferenceRole,
	kind string,
	value string,
) core.StoreReference {
	t.Helper()
	reference, err := core.NewStoreReference(role, kind, value)
	if err != nil {
		t.Fatalf("NewStoreReference(%q, %q) error = %v", role, kind, err)
	}
	return reference
}

// requestJSON sends one request through the real HTTP server and decodes its stable JSON response.
func (fixture *apiIntegrationFixture) requestJSON(
	t *testing.T,
	method string,
	path string,
	bearer string,
	input any,
	expectedStatus int,
	output any,
) []byte {
	t.Helper()
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("marshal %s %s request: %v", method, path, err)
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, fixture.server.URL+path, body)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, path, err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatalf("send %s %s request: %v", method, path, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, path, err)
	}
	if response.StatusCode != expectedStatus {
		t.Fatalf("%s %s status = %d, want %d: %s", method, path, response.StatusCode, expectedStatus, payload)
	}
	if response.Header.Get("Content-Type") != "application/json" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%s %s response headers = %#v", method, path, response.Header)
	}
	if output != nil {
		if err := json.Unmarshal(payload, output); err != nil {
			t.Fatalf("decode %s %s response: %v: %s", method, path, err, payload)
		}
	}
	return payload
}

// assertAPIIntegrationEntitlement verifies the public projection returned across verification endpoints.
func assertAPIIntegrationEntitlement(
	t *testing.T,
	customerID core.CustomerID,
	entitlements []entitlementResponse,
) {
	t.Helper()
	if customerID != apiIntegrationCustomerID || len(entitlements) != 1 {
		t.Fatalf("entitlement snapshot = customer %q, values %#v", customerID, entitlements)
	}
	entitlement := entitlements[0]
	if entitlement.Key != "premium" || entitlement.Access != core.AccessAllowed ||
		entitlement.Reason != core.AccessReasonPurchaseValid || entitlement.Version != 1 {
		t.Fatalf("entitlement = %#v", entitlement)
	}
}

// assertAPIIntegrationOverview verifies the administrator projection reflects the completed HTTP journey.
func assertAPIIntegrationOverview(t *testing.T, overview adminOverviewResponse) {
	t.Helper()
	if overview.Project.ID != apiIntegrationProjectID || len(overview.Applications) != 1 ||
		len(overview.Products) != 1 || len(overview.Customers) != 1 ||
		len(overview.RecentTransactions) != 1 {
		t.Fatalf("admin overview collections = %#v", overview)
	}
	application := overview.Applications[0]
	if application.ID != apiIntegrationApplicationID || !application.CredentialConfigured ||
		application.CredentialRevision != 1 || !application.WebhookConfigured || application.WebhookRevision != 1 {
		t.Fatalf("admin application = %#v", application)
	}
	if overview.Products[0].ID != apiIntegrationProductID || overview.Products[0].StoreMappingCount != 1 ||
		len(overview.Products[0].EntitlementKeys) != 1 || overview.Products[0].EntitlementKeys[0] != "premium" {
		t.Fatalf("admin product = %#v", overview.Products[0])
	}
	if overview.Customers[0].ID != apiIntegrationCustomerID || overview.Customers[0].AllowedCount != 1 ||
		overview.Customers[0].EntitlementCount != 1 {
		t.Fatalf("admin customer = %#v", overview.Customers[0])
	}
	transaction := overview.RecentTransactions[0]
	if transaction.ID != apiIntegrationObservationID || transaction.Access != core.AccessAllowed ||
		transaction.ProviderProductID != apiIntegrationProviderProductID {
		t.Fatalf("admin transaction = %#v", transaction)
	}
	if overview.Analytics.VerifiedCount != 1 || overview.Analytics.AllowedCount != 1 ||
		overview.Analytics.ActiveEntitlementCount != 1 || overview.Analytics.SandboxValue.TransactionCount != 1 ||
		len(overview.Analytics.SandboxValue.Amounts) != 1 ||
		overview.Analytics.SandboxValue.Amounts[0].Milliunits != 4990 ||
		overview.Analytics.SandboxValue.Amounts[0].Currency != "USD" {
		t.Fatalf("admin analytics = %#v", overview.Analytics)
	}
}

// openAPIIntegrationDatabase resets and opens one migrated PostgreSQL fixture.
func openAPIIntegrationDatabase(t *testing.T) *apiIntegrationDatabase {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(apiIntegrationDatabaseURLKey))
	if databaseURL == "" {
		t.Skipf("%s is not configured", apiIntegrationDatabaseURLKey)
	}
	ctx, cancel := context.WithTimeout(context.Background(), apiIntegrationDatabaseTimeout)
	t.Cleanup(cancel)
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to HTTP integration PostgreSQL: %v", err)
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, apiIntegrationDatabaseAdvisoryLock); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("lock HTTP integration PostgreSQL: %v", err)
	}
	migrator, err := postgres.NewMigrator(ctx, connection)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("NewMigrator() error = %v", err)
	}
	if err := postgres.RemoveRiver(ctx, databaseURL); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset HTTP integration River schema: %v", err)
	}
	if err := migrator.MigrateTo(ctx, 0); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset HTTP integration PostgreSQL: %v", err)
	}
	if err := migrator.Up(ctx); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("migrate HTTP integration PostgreSQL: %v", err)
	}
	if err := postgres.MigrateRiver(ctx, databaseURL); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("migrate HTTP integration River schema: %v", err)
	}
	database := &apiIntegrationDatabase{ctx: ctx, conn: connection, migrator: migrator}
	database.store, err = postgres.OpenStore(ctx, databaseURL)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() {
		database.store.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), apiIntegrationDatabaseTimeout)
		defer cleanupCancel()
		if err := postgres.RemoveRiver(cleanupCtx, databaseURL); err != nil {
			t.Errorf("reset HTTP integration River schema during cleanup: %v", err)
		}
		if err := migrator.MigrateTo(cleanupCtx, 0); err != nil {
			t.Errorf("reset HTTP integration PostgreSQL during cleanup: %v", err)
		}
		if _, err := connection.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1)`, apiIntegrationDatabaseAdvisoryLock); err != nil {
			t.Errorf("unlock HTTP integration PostgreSQL: %v", err)
		}
		if err := connection.Close(cleanupCtx); err != nil {
			t.Errorf("close HTTP integration PostgreSQL: %v", err)
		}
	})
	return database
}

// assertAPIIntegrationTableCount verifies committed rows in one fixed HTTP integration table.
func assertAPIIntegrationTableCount(
	t *testing.T,
	database *apiIntegrationDatabase,
	table string,
	expected int,
) {
	t.Helper()
	allowedTables := map[string]struct{}{
		"application_credentials": {},
		"customer_entitlements":   {},
		"outbox_events":           {},
		"purchase_evidence":       {},
		"purchase_observations":   {},
		"reconciliation_jobs":     {},
		"verified_artifacts":      {},
		"webhook_endpoints":       {},
	}
	if _, allowed := allowedTables[table]; !allowed {
		t.Fatalf("table %q is not allowed by HTTP integration helper", table)
	}
	var actual int
	if err := database.conn.QueryRow(database.ctx, "SELECT count(*) FROM "+table).Scan(&actual); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if actual != expected {
		t.Fatalf("%s count = %d, want %d", table, actual, expected)
	}
}
