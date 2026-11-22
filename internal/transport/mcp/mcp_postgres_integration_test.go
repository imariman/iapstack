//go:build integration

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/credentials"
	"github.com/imariman/iapstack/internal/financialreports"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	platformprotection "github.com/imariman/iapstack/internal/platform/protection"
	"github.com/imariman/iapstack/internal/stores"
	"github.com/imariman/iapstack/internal/stores/apple"
	"github.com/imariman/iapstack/internal/stores/googleplay"
	"github.com/imariman/iapstack/internal/stores/huawei"
	"github.com/imariman/iapstack/internal/transport/httpapi"
	"github.com/imariman/iapstack/internal/verification"
	"github.com/imariman/iapstack/internal/webhooks"
	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// integrationDatabaseURLKey names the opt-in PostgreSQL connection setting.
	integrationDatabaseURLKey = "IAPSTACK_TEST_DATABASE_URL"
	// integrationDatabaseTimeout bounds setup and cleanup.
	integrationDatabaseTimeout = 30 * time.Second
	// integrationDatabaseAdvisoryLock serializes schema-resetting integration test packages.
	integrationDatabaseAdvisoryLock int64 = 424090117
	// integrationBootstrapKey is the installation credential used once to create a stored admin key.
	integrationBootstrapKey = "mcp-integration-bootstrap-key-32-bytes"
	// integrationProjectID is the project the agent key reads.
	integrationProjectID = "mcp-project-1"
	// integrationOtherProjectID is a second project the agent key must not read.
	integrationOtherProjectID = "mcp-project-2"
	// integrationApplicationID is the Google Play application of the first project.
	integrationApplicationID = "mcp-application-1"
	// integrationExternalCustomerID is the host application's customer identity.
	integrationExternalCustomerID = "mcp-external-customer-1"
	// integrationEntitlementID identifies the seeded entitlement.
	integrationEntitlementID = "mcp-entitlement-1"
	// integrationProductID identifies the seeded product.
	integrationProductID = "mcp-product-1"
	// integrationProviderProductID identifies the Google Play product.
	integrationProviderProductID = "premium_lifetime"
	// integrationPublicURL is the configured external base URL.
	integrationPublicURL = "https://iap.example.com"
	// sentinelCredentialSecret is credential material that must never be returned.
	sentinelCredentialSecret = "sentinel-credential-private-key"
	// sentinelWebhookSecret is the webhook signing secret that must never be returned.
	sentinelWebhookSecret = "sentinel-webhook-signing-secret-of-enough-bytes"
	// sentinelPurchaseToken is the private purchase token that must never be returned.
	sentinelPurchaseToken = "sentinel-purchase-token"
	// sentinelTransactionID is the private store order id that must never be returned.
	sentinelTransactionID = "GPA.9999-8888-7777-66666"
)

// integrationAdapter returns one deterministic Google Play verification result.
type integrationAdapter struct {
	mu     sync.Mutex
	result stores.VerificationResult
}

// integrationClock returns one fixed verification time.
type integrationClock struct {
	now time.Time
}

// integrationStack is the real HTTP API and MCP endpoint over one migrated database.
type integrationStack struct {
	server *httptest.Server
}

// TestMCPSessionEndToEndOverPostgres runs a real MCP client session over HTTP against seeded PostgreSQL data,
// with an agent key created through the real authentication service.
func TestMCPSessionEndToEndOverPostgres(t *testing.T) {
	stack := newIntegrationStack(t)
	var created map[string]string
	stack.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", integrationBootstrapKey, map[string]any{"role": "admin"}, http.StatusCreated, &created)
	admin := created["key"]

	project := "/v1/admin/projects/" + integrationProjectID
	stack.requestJSON(t, http.MethodPut, project, admin, nil, http.StatusOK, nil)
	stack.requestJSON(t, http.MethodPut, "/v1/admin/projects/"+integrationOtherProjectID, admin, nil, http.StatusOK, nil)
	stack.requestJSON(t, http.MethodPut, project+"/applications/"+integrationApplicationID, admin, map[string]any{
		"provider": "google_play", "environment": "test", "provider_application_id": "com.example.mcp",
	}, http.StatusOK, nil)
	stack.requestJSON(t, http.MethodPut, project+"/entitlements/"+integrationEntitlementID, admin, map[string]any{"key": "premium"}, http.StatusOK, nil)
	stack.requestJSON(t, http.MethodPut, project+"/products/"+integrationProductID, admin, map[string]any{
		"kind": "non_consumable", "entitlement_ids": []string{integrationEntitlementID},
	}, http.StatusOK, nil)
	stack.requestJSON(t, http.MethodPut, project+"/applications/"+integrationApplicationID+"/store-products/"+integrationProviderProductID,
		admin, map[string]any{"product_id": integrationProductID}, http.StatusOK, nil)
	stack.requestJSON(t, http.MethodPut, project+"/applications/"+integrationApplicationID+"/credentials/"+string(googleplay.CredentialKind),
		admin, map[string]any{
			"content_type": googleplay.CredentialContentType, "schema_version": googleplay.CredentialSchemaVersion,
			"expected_revision": 0, "payload": json.RawMessage(fmt.Sprintf(
				`{"client_email":"mcp@example.iam.gserviceaccount.com","private_key_id":"mcp-key","private_key":%q}`,
				sentinelCredentialSecret,
			)),
		}, http.StatusOK, nil)
	stack.requestJSON(t, http.MethodPut, project+"/applications/"+integrationApplicationID+"/webhook", admin, map[string]any{
		"url":               "https://hooks.example.com/hooks/catch/" + sentinelWebhookPath + "/?token=" + sentinelWebhookQuery,
		"signing_secret":    sentinelWebhookSecret,
		"expected_revision": 0,
	}, http.StatusOK, nil)

	stack.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", admin, map[string]any{
		"role": "application", "project_id": integrationProjectID, "application_id": integrationApplicationID,
	}, http.StatusCreated, &created)
	applicationKey := created["key"]
	var session map[string]any
	stack.requestJSON(t, http.MethodPost, "/v1/applications/"+integrationApplicationID+"/customer-sessions", applicationKey,
		map[string]any{"external_customer_id": integrationExternalCustomerID}, http.StatusCreated, &session)
	var verified struct {
		CustomerID string `json:"customer_id"`
	}
	stack.requestJSON(t, http.MethodPost, "/v1/applications/"+integrationApplicationID+"/purchases:verify", session["token"].(string),
		map[string]any{
			"external_customer_id": integrationExternalCustomerID,
			"claimed_products":     []string{integrationProviderProductID},
			"evidence":             json.RawMessage(fmt.Sprintf(`{"purchase_token":%q,"product_kind":"non_consumable"}`, sentinelPurchaseToken)),
		}, http.StatusOK, &verified)
	stack.requestJSON(t, http.MethodPut, project+"/customers/"+verified.CustomerID+"/aliases/"+untrustedExternalID, admin,
		map[string]any{}, http.StatusOK, nil)

	stack.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", admin, map[string]any{
		"role": "agent", "project_id": "missing-project",
	}, http.StatusConflict, nil)
	stack.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", admin, map[string]any{
		"role": "application", "project_id": integrationProjectID, "application_id": "missing-application",
	}, http.StatusConflict, nil)
	stack.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", admin, map[string]any{
		"role": "agent", "project_id": integrationProjectID,
	}, http.StatusCreated, &created)
	agentKey := created["key"]
	stack.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", admin, map[string]any{
		"role": "agent", "project_id": integrationOtherProjectID, "allow_writes": true,
	}, http.StatusCreated, &created)
	otherAgentKey := created["key"]
	stack.requestJSON(t, http.MethodGet, "/v1/admin/projects", agentKey, nil, http.StatusUnauthorized, nil)

	client := connectIntegration(t, stack.server.URL, agentKey)
	tools, err := client.ListTools(t.Context(), nil)
	if err != nil || len(tools.Tools) != len(expectedTools) {
		t.Fatalf("ListTools() = %d tools, %v, want %d", len(tools.Tools), err, len(expectedTools))
	}
	results := map[string]string{}
	for name, arguments := range map[string]map[string]any{
		"list_projects":            {},
		"get_project_overview":     {"project_id": integrationProjectID},
		"get_subscription_metrics": {"project_id": integrationProjectID, "environment": "test", "window_days": 7},
		"search_customers":         {"project_id": integrationProjectID, "query": integrationExternalCustomerID},
		"get_customer":             {"project_id": integrationProjectID, "customer_id": verified.CustomerID},
		"resolve_customer":         {"project_id": integrationProjectID, "external_id": untrustedExternalID},
		"list_financial_reports":   {"project_id": integrationProjectID},
		"get_setup_status":         {"project_id": integrationProjectID},
	} {
		result, err := client.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: arguments})
		if err != nil {
			t.Fatalf("CallTool(%s) error = %v", name, err)
		}
		raw, _ := json.Marshal(result)
		if result.IsError {
			t.Fatalf("CallTool(%s) = %s, want success", name, raw)
		}
		for _, sentinel := range []string{
			sentinelCredentialSecret, sentinelWebhookSecret, sentinelWebhookQuery, sentinelWebhookPath, sentinelPurchaseToken,
			sentinelTransactionID, agentKey, otherAgentKey, admin, applicationKey,
		} {
			if strings.Contains(string(raw), sentinel) {
				t.Fatalf("CallTool(%s) leaked a protected value: %s", name, raw)
			}
		}
		assertNoProtectedFields(t, name, result.StructuredContent)
		structured, _ := json.Marshal(result.StructuredContent)
		results[name] = string(structured)
	}
	for name, fragment := range map[string]string{
		"list_projects":        `"id":"` + integrationProjectID + `"`,
		"get_project_overview": `"credential_configured":true`,
		"search_customers":     `"match":"external_id"`,
		"get_customer":         `"key":"premium"`,
		"resolve_customer":     `"canonical_customer_id":"` + verified.CustomerID + `"`,
		"get_setup_status":     `"url":"` + integrationPublicURL + "/v1/providers/google-play/projects/" + integrationProjectID,
	} {
		if !strings.Contains(results[name], fragment) {
			t.Fatalf("%s = %s, want %s", name, results[name], fragment)
		}
	}
	if strings.Contains(results["list_projects"], integrationOtherProjectID) {
		t.Fatalf("list_projects = %s, want only the agent's project", results["list_projects"])
	}
	if !strings.Contains(results["get_setup_status"], `"origin":"https://hooks.example.com"`) ||
		strings.Contains(results["get_setup_status"], "/hooks/catch") ||
		!strings.Contains(results["get_setup_status"], `"configured":true,"required_kind":"`+string(googleplay.CredentialKind)+`"`) {
		t.Fatalf("get_setup_status = %s, want only the webhook origin and the configured credential", results["get_setup_status"])
	}

	crossProject, err := connectIntegration(t, stack.server.URL, otherAgentKey).CallTool(t.Context(), &sdk.CallToolParams{
		Name: "get_customer", Arguments: map[string]any{"project_id": integrationProjectID, "customer_id": verified.CustomerID},
	})
	if err != nil {
		t.Fatalf("cross-project CallTool() error = %v", err)
	}
	assertToolError(t, "get_customer", crossProject, "forbidden")

	dashboardCookie := stack.dashboardCookie(t, admin)
	for name, header := range map[string]map[string]string{
		"application key":         {"Authorization": "Bearer " + applicationKey},
		"retired bootstrap key":   {"Authorization": "Bearer " + integrationBootstrapKey},
		"dashboard cookie":        {"Cookie": dashboardCookie.Name + "=" + dashboardCookie.Value},
		"dashboard cookie bearer": {"Authorization": "Bearer " + dashboardCookie.Value},
	} {
		status := stack.postMCP(t, header)
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Fatalf("%s on /mcp = %d, want 401 or 403", name, status)
		}
		if name == "application key" && status != http.StatusForbidden {
			t.Fatalf("application key on /mcp = %d, want 403", status)
		}
	}
}

// Provider identifies Google Play.
func (adapter *integrationAdapter) Provider() core.Provider {
	return core.ProviderGooglePlay
}

// Verify returns the configured result.
func (adapter *integrationAdapter) Verify(context.Context, stores.VerificationRequest) (stores.VerificationResult, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.result, nil
}

// Reconcile returns the configured result.
func (adapter *integrationAdapter) Reconcile(context.Context, stores.ReconciliationRequest) (stores.VerificationResult, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.result, nil
}

// Now returns the fixed verification time.
func (clock integrationClock) Now() time.Time {
	return clock.now
}

// newIntegrationStack migrates a clean database and serves the real HTTP API and MCP endpoint.
func newIntegrationStack(t *testing.T) *integrationStack {
	t.Helper()
	store := openIntegrationDatabase(t)
	now := time.Now().UTC().Truncate(time.Second)
	keyringConfig, err := platformprotection.NewConfig(
		"mcp-integration-key", map[string][]byte{"mcp-integration-key": bytes.Repeat([]byte{23}, 32)}, bytes.Repeat([]byte{29}, 32),
	)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	keyring, err := platformprotection.New(keyringConfig)
	if err != nil {
		t.Fatalf("New() keyring error = %v", err)
	}
	credentialService, err := credentials.NewService(store, keyring)
	if err != nil {
		t.Fatalf("credentials.NewService() error = %v", err)
	}
	huaweiAdapter, err := huawei.New(credentialService, time.Second, false)
	if err != nil {
		t.Fatalf("huawei.New() error = %v", err)
	}
	appleAdapter, err := apple.New(credentialService, time.Second)
	if err != nil {
		t.Fatalf("apple.New() error = %v", err)
	}
	googlePlayAdapter, err := googleplay.New(credentialService, time.Second)
	if err != nil {
		t.Fatalf("googleplay.New() error = %v", err)
	}
	registry, err := stores.NewRegistry(&integrationAdapter{result: integrationProviderResult(t, now)})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	verificationService, err := verification.NewService(store, registry, keyring, verification.NewDefaultProjector(), integrationClock{now: now})
	if err != nil {
		t.Fatalf("verification.NewService() error = %v", err)
	}
	authentication, err := auth.NewService(store, integrationBootstrapKey, 4)
	if err != nil {
		t.Fatalf("auth.NewService() error = %v", err)
	}
	adminSessions, err := auth.NewAdminSessions(store, keyring)
	if err != nil {
		t.Fatalf("NewAdminSessions() error = %v", err)
	}
	customerSessions, err := auth.NewCustomerSessions(store, keyring)
	if err != nil {
		t.Fatalf("NewCustomerSessions() error = %v", err)
	}
	webhookService, err := webhooks.NewService(store, keyring, time.Second, false)
	if err != nil {
		t.Fatalf("webhooks.NewService() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	reports, err := financialreports.NewService(store, logger, financialreports.DefaultByteLimit, financialreports.DefaultRowLimit)
	if err != nil {
		t.Fatalf("financialreports.NewService() error = %v", err)
	}
	api, err := httpapi.New(httpapi.Dependencies{
		Store: store, Operations: store, Admin: store, Authentication: authentication,
		AdminSessions: adminSessions, CustomerSessions: customerSessions, Credentials: credentialService,
		Webhooks: webhookService, Verification: verificationService, Huawei: huaweiAdapter, Apple: appleAdapter,
		GooglePlay: googlePlayAdapter, Protection: keyring, FinancialReports: reports, BodyLimit: 1 << 20,
	})
	if err != nil {
		t.Fatalf("httpapi.New() error = %v", err)
	}
	handler, err := NewHandler(Options{
		Authentication: authentication,
		ControlPlane: controlplane.New(controlplane.Dependencies{
			Store: store, Admin: store, Setup: store, FinancialReports: reports, Protection: keyring,
			PublicURL: integrationPublicURL,
		}),
		BodyLimit: 1 << 20, Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", api)
	mux.Handle(Path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &integrationStack{server: server}
}

// integrationProviderResult returns one allowed non-consumable Google Play purchase with private references.
func integrationProviderResult(t *testing.T, now time.Time) stores.VerificationResult {
	t.Helper()
	store := core.StoreApplication{Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest, ID: "com.example.mcp"}
	evidence, err := stores.NewEvidence("application/json", []byte(`{"source":"mcp-integration"}`))
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	reference := func(role core.ReferenceRole, kind, value string) core.StoreReference {
		created, err := core.NewStoreReference(role, kind, value)
		if err != nil {
			t.Fatalf("NewStoreReference() error = %v", err)
		}
		return created
	}
	return stores.VerificationResult{
		VerifiedAt: now,
		Artifacts:  []stores.VerifiedArtifact{{Kind: "provider_response", Evidence: evidence}},
		Observations: []core.PurchaseObservation{{
			ID: "mcp-observation-1", ApplicationID: integrationApplicationID, Store: store,
			ProductID: integrationProviderProductID, ProductKind: core.ProductKindNonConsumable,
			State: core.LifecycleActive, ProviderState: "PURCHASED", Access: core.AccessAllowed,
			AccessReason: core.AccessReasonPurchaseValid, Ownership: core.OwnershipPurchased, Quantity: 1,
			OccurredAt: now.Add(-time.Minute), ObservedAt: now,
			EffectivePeriod: core.EffectivePeriod{StartsAt: now.Add(-time.Minute)},
			Renewal:         core.Renewal{Mode: core.RenewalNone, Status: core.RenewalNotApplicable},
			References: []core.StoreReference{
				reference(core.ReferenceQuery, "purchase_token", sentinelPurchaseToken),
				reference(core.ReferenceTransaction, "order_id", sentinelTransactionID),
				reference(core.ReferenceCustomerBinding, "obfuscated_external_account_id", integrationExternalCustomerID),
			},
		}},
	}
}

// requestJSON sends one JSON request to the HTTP API and decodes the response.
func (stack *integrationStack) requestJSON(
	t *testing.T,
	method string,
	path string,
	bearer string,
	input any,
	expectedStatus int,
	output any,
) {
	t.Helper()
	var body io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			t.Fatalf("marshal %s %s: %v", method, path, err)
		}
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(t.Context(), method, stack.server.URL+path, body)
	if err != nil {
		t.Fatalf("NewRequest(%s %s) error = %v", method, path, err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := stack.server.Client().Do(request)
	if err != nil {
		t.Fatalf("%s %s error = %v", method, path, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(response.Body)
	if response.StatusCode != expectedStatus {
		t.Fatalf("%s %s = %d %s, want %d", method, path, response.StatusCode, payload, expectedStatus)
	}
	if output != nil {
		if err := json.Unmarshal(payload, output); err != nil {
			t.Fatalf("decode %s %s: %v", method, path, err)
		}
	}
}

// dashboardCookie exchanges an admin bearer for a dashboard session cookie.
func (stack *integrationStack) dashboardCookie(t *testing.T, admin string) *http.Cookie {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, stack.server.URL+"/v1/admin/dashboard-session", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+admin)
	response, err := stack.server.Client().Do(request)
	if err != nil {
		t.Fatalf("create dashboard session error = %v", err)
	}
	_ = response.Body.Close()
	for _, cookie := range response.Cookies() {
		if cookie.Value != "" {
			return cookie
		}
	}
	t.Fatalf("dashboard session response = %d with no cookie", response.StatusCode)
	return nil
}

// postMCP sends one initialize request to /mcp with the given headers and returns the status.
func (stack *integrationStack) postMCP(t *testing.T, header map[string]string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, stack.server.URL+Path, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},`+
			`"clientInfo":{"name":"test","version":"1"}}}`,
	))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	for name, value := range header {
		request.Header.Set(name, value)
	}
	response, err := stack.server.Client().Do(request)
	if err != nil {
		t.Fatalf("POST /mcp error = %v", err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

// connectIntegration opens one MCP client session against the integration server.
func connectIntegration(t *testing.T, baseURL, bearer string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "iapstack-integration", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &sdk.StreamableClientTransport{
		Endpoint:   baseURL + Path,
		HTTPClient: &http.Client{Transport: bearerTransport{bearer: bearer, base: http.DefaultTransport}},
	}, nil)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// openIntegrationDatabase resets the configured database to the latest schema and opens a store.
func openIntegrationDatabase(t *testing.T) *postgres.Store {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(integrationDatabaseURLKey))
	if databaseURL == "" {
		t.Skipf("%s is not configured", integrationDatabaseURLKey)
	}
	ctx, cancel := context.WithTimeout(context.Background(), integrationDatabaseTimeout)
	t.Cleanup(cancel)
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to integration PostgreSQL: %v", err)
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, integrationDatabaseAdvisoryLock); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("lock integration PostgreSQL: %v", err)
	}
	migrator, err := postgres.NewMigrator(ctx, connection)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("NewMigrator() error = %v", err)
	}
	for _, step := range []func() error{
		func() error { return postgres.RemoveRiver(ctx, databaseURL) },
		func() error { return postgres.ClearIdentityMigrationGuards(ctx, connection) },
		func() error { return migrator.MigrateTo(ctx, 0) },
		func() error { return migrator.Up(ctx) },
		func() error { return postgres.MigrateRiver(ctx, databaseURL) },
	} {
		if err := step(); err != nil {
			_ = connection.Close(ctx)
			t.Fatalf("prepare integration PostgreSQL: %v", err)
		}
	}
	store, err := postgres.OpenStore(ctx, databaseURL)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() {
		store.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), integrationDatabaseTimeout)
		defer cleanupCancel()
		if err := postgres.RemoveRiver(cleanupCtx, databaseURL); err != nil {
			t.Errorf("reset River schema during cleanup: %v", err)
		}
		if err := postgres.ClearIdentityMigrationGuards(cleanupCtx, connection); err != nil {
			t.Errorf("clear identity migration guards during cleanup: %v", err)
		}
		if err := migrator.MigrateTo(cleanupCtx, 0); err != nil {
			t.Errorf("reset integration PostgreSQL during cleanup: %v", err)
		}
		if _, err := connection.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1)`, integrationDatabaseAdvisoryLock); err != nil {
			t.Errorf("unlock integration PostgreSQL: %v", err)
		}
		if err := connection.Close(cleanupCtx); err != nil {
			t.Errorf("close integration PostgreSQL: %v", err)
		}
	})
	return store
}
