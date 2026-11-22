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
	store  *postgres.Store
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
	if err != nil || len(tools.Tools) != len(expectedReadTools) {
		t.Fatalf("ListTools() = %d tools, %v, want %d", len(tools.Tools), err, len(expectedReadTools))
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

// TestMCPWriteToolsRecordTheChannelOverPostgres runs write tools through a real MCP client session with a
// writing agent key, and verifies the stored audit rows carry the agent key and the mcp channel while the
// same writes over /v1 record the api or dashboard channel.
func TestMCPWriteToolsRecordTheChannelOverPostgres(t *testing.T) {
	stack := newIntegrationStack(t)
	var created map[string]string
	stack.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", integrationBootstrapKey, map[string]any{"role": "admin"}, http.StatusCreated, &created)
	admin := created["key"]
	adminKeyID := keyIDFromBearer(t, admin)

	project := "/v1/admin/projects/" + integrationProjectID
	stack.requestJSON(t, http.MethodPut, project, admin, nil, http.StatusOK, nil)
	stack.requestJSON(t, http.MethodPut, "/v1/admin/projects/"+integrationOtherProjectID, admin, nil, http.StatusOK, nil)
	stack.requestJSON(t, http.MethodPut, project+"/applications/"+integrationApplicationID, admin, map[string]any{
		"provider": "google_play", "environment": "test", "provider_application_id": "com.example.mcp",
	}, http.StatusOK, nil)
	stack.requestJSON(t, http.MethodPut, project+"/customers/mcp-customer-1", admin,
		map[string]any{"external_id": integrationExternalCustomerID}, http.StatusOK, nil)

	stack.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", admin, map[string]any{
		"role": "agent", "project_id": integrationProjectID, "allow_writes": true,
	}, http.StatusCreated, &created)
	writerKey := created["key"]
	writerKeyID := keyIDFromBearer(t, writerKey)
	stack.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", admin, map[string]any{
		"role": "agent", "project_id": integrationProjectID,
	}, http.StatusCreated, &created)
	readOnlyKey := created["key"]
	stack.requestJSON(t, http.MethodPost, "/v1/admin/api-keys", admin, map[string]any{
		"role": "agent", "project_id": integrationOtherProjectID, "allow_writes": true,
	}, http.StatusCreated, &created)
	otherWriterKey := created["key"]

	writer := connectIntegration(t, stack.server.URL, writerKey)
	listed, err := writer.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) != len(allExpectedTools()) {
		t.Fatalf("writer ListTools() = %v, %v, want the full catalogue", listed, err)
	}
	call := func(session *sdk.ClientSession, name string, arguments map[string]any) map[string]any {
		t.Helper()
		result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: arguments})
		if err != nil {
			t.Fatalf("CallTool(%s) error = %v", name, err)
		}
		raw, _ := json.Marshal(result)
		if result.IsError {
			t.Fatalf("CallTool(%s) = %s, want success", name, raw)
		}
		if strings.Contains(string(raw), writerKey) || strings.Contains(string(raw), admin) {
			t.Fatalf("CallTool(%s) leaked a bearer: %s", name, raw)
		}
		assertNoProtectedFields(t, name, result.StructuredContent)
		return result.StructuredContent.(map[string]any)
	}

	// Definitions are written by the agent; a different key for an existing entitlement is a conflict.
	call(writer, "put_entitlement", map[string]any{"project_id": integrationProjectID, "entitlement_id": integrationEntitlementID, "key": "premium"})
	call(writer, "put_entitlement", map[string]any{"project_id": integrationProjectID, "entitlement_id": integrationEntitlementID, "key": "premium"})
	call(writer, "put_product", map[string]any{
		"project_id": integrationProjectID, "product_id": integrationProductID, "kind": "non_consumable",
		"entitlement_ids": []string{integrationEntitlementID},
	})
	call(writer, "put_store_product", map[string]any{
		"project_id": integrationProjectID, "application_id": integrationApplicationID,
		"provider_product_id": integrationProviderProductID, "product_id": integrationProductID,
	})
	conflict, err := writer.CallTool(t.Context(), &sdk.CallToolParams{Name: "put_entitlement", Arguments: map[string]any{
		"project_id": integrationProjectID, "entitlement_id": integrationEntitlementID, "key": "other-key",
	}})
	if err != nil {
		t.Fatalf("CallTool(put_entitlement) error = %v", err)
	}
	assertToolError(t, "put_entitlement", conflict, "conflict")
	var key string
	if err := stack.store.Pool().QueryRow(t.Context(), `SELECT key FROM entitlements WHERE id = $1`, integrationEntitlementID).Scan(&key); err != nil || key != "premium" {
		t.Fatalf("stored entitlement key = %q, %v, want premium", key, err)
	}

	grantArguments := map[string]any{
		"project_id": integrationProjectID, "customer_id": "mcp-customer-1", "idempotency_key": "mcp-grant-1",
		"entitlement_id": integrationEntitlementID, "product_id": integrationProductID,
		"application_id": integrationApplicationID, "reason": "Support ticket 42", "duration_days": 30,
	}
	granted := call(writer, "create_manual_grant", grantArguments)
	grant := granted["grant"].(map[string]any)
	if granted["replay"] != false || grant["actor_key_id"] != writerKeyID || grant["actor_channel"] != "mcp" || grant["status"] != "active" {
		t.Fatalf("create_manual_grant = %#v, want an active grant by the writer key over mcp", granted)
	}
	if replayed := call(writer, "create_manual_grant", grantArguments); replayed["replay"] != true {
		t.Fatalf("repeated create_manual_grant = %#v, want a replay", replayed)
	}
	grantID := grant["id"].(string)
	revoked := call(writer, "revoke_manual_grant", map[string]any{
		"project_id": integrationProjectID, "customer_id": "mcp-customer-1", "grant_id": grantID, "reason": "Refunded",
	})["grant"].(map[string]any)
	if revoked["status"] != "revoked" || revoked["revoked_by_key_id"] != writerKeyID || revoked["revoked_by_channel"] != "mcp" {
		t.Fatalf("revoke_manual_grant = %#v, want a revocation by the writer key over mcp", revoked)
	}
	if resolution := call(writer, "put_customer_alias", map[string]any{
		"project_id": integrationProjectID, "customer_id": "mcp-customer-1", "external_id": "mcp-alias-agent",
	}); resolution["match"] != "alias" || resolution["canonical_customer_id"] != "mcp-customer-1" {
		t.Fatalf("put_customer_alias = %#v, want an alias of mcp-customer-1", resolution)
	}

	// The same writes over /v1 record the api channel for a bearer and the dashboard channel for a session.
	var apiGrant controlplane.ManualGrantResult
	stack.requestJSON(t, http.MethodPost, project+"/customers/mcp-customer-1/manual-grants", admin, map[string]any{
		"idempotency_key": "api-grant-1", "entitlement_id": integrationEntitlementID, "product_id": integrationProductID,
		"application_id": integrationApplicationID, "reason": "Goodwill", "duration_days": 7,
	}, http.StatusOK, &apiGrant)
	if apiGrant.Grant.ActorKeyID != adminKeyID || apiGrant.Grant.ActorChannel != controlplane.ChannelAPI {
		t.Fatalf("/v1 grant = %#v, want the admin key over api", apiGrant.Grant)
	}
	stack.requestJSON(t, http.MethodPut, project+"/customers/mcp-customer-1/aliases/mcp-alias-api", admin, map[string]any{}, http.StatusOK, nil)
	stack.dashboardPut(t, stack.dashboardCookie(t, admin), project+"/customers/mcp-customer-1/aliases/mcp-alias-dashboard")

	type auditRow struct{ role, keyID, channel string }
	aliases := map[string]auditRow{}
	rows, err := stack.store.Pool().Query(t.Context(), `
		SELECT external_id, actor_role, actor_key_id, actor_channel FROM customer_aliases WHERE project_id = $1
	`, integrationProjectID)
	if err != nil {
		t.Fatalf("query aliases: %v", err)
	}
	for rows.Next() {
		var externalID string
		var row auditRow
		if err := rows.Scan(&externalID, &row.role, &row.keyID, &row.channel); err != nil {
			t.Fatalf("scan alias: %v", err)
		}
		aliases[externalID] = row
	}
	rows.Close()
	for externalID, expected := range map[string]auditRow{
		"mcp-alias-agent":     {role: "agent", keyID: writerKeyID, channel: "mcp"},
		"mcp-alias-api":       {role: "admin", keyID: adminKeyID, channel: "api"},
		"mcp-alias-dashboard": {role: "admin", keyID: adminKeyID, channel: "dashboard"},
	} {
		if aliases[externalID] != expected {
			t.Fatalf("alias %s audit = %#v, want %#v", externalID, aliases[externalID], expected)
		}
	}
	var actorKeyID, actorChannel, revokedByKeyID, revokedByChannel string
	if err := stack.store.Pool().QueryRow(t.Context(), `
		SELECT actor_key_id, actor_channel, revoked_by_key_id, revoked_by_channel FROM manual_grants WHERE id = $1
	`, grantID).Scan(&actorKeyID, &actorChannel, &revokedByKeyID, &revokedByChannel); err != nil {
		t.Fatalf("query grant: %v", err)
	}
	if actorKeyID != writerKeyID || actorChannel != "mcp" || revokedByKeyID != writerKeyID || revokedByChannel != "mcp" {
		t.Fatalf("stored grant audit = %s %s %s %s, want the writer key over mcp", actorKeyID, actorChannel, revokedByKeyID, revokedByChannel)
	}
	if err := stack.store.Pool().QueryRow(t.Context(), `SELECT actor_channel FROM manual_grants WHERE id = $1`,
		apiGrant.Grant.ID).Scan(&actorChannel); err != nil || actorChannel != "api" {
		t.Fatalf("stored /v1 grant channel = %q, %v, want api", actorChannel, err)
	}

	// The customer detail shows the channel on /v1 and through get_customer.
	var detail controlplane.CustomerDetail
	stack.requestJSON(t, http.MethodGet, project+"/customers/mcp-customer-1", admin, nil, http.StatusOK, &detail)
	channels := map[string]controlplane.Channel{}
	for _, alias := range detail.Aliases {
		channels["alias "+alias.ExternalID] = alias.ActorChannel
	}
	for _, grant := range detail.ManualGrants {
		channels["grant "+grant.ID] = grant.ActorChannel
		if grant.ID == grantID {
			channels["revocation"] = grant.RevokedByChannel
		}
	}
	timelineChannels := map[controlplane.Channel]bool{}
	for _, event := range detail.Timeline {
		if event.ActorChannel != "" {
			timelineChannels[event.ActorChannel] = true
		}
	}
	if channels["alias mcp-alias-agent"] != controlplane.ChannelMCP || channels["alias mcp-alias-dashboard"] != controlplane.ChannelDashboard ||
		channels["grant "+grantID] != controlplane.ChannelMCP || channels["revocation"] != controlplane.ChannelMCP ||
		channels["grant "+apiGrant.Grant.ID] != controlplane.ChannelAPI ||
		!timelineChannels[controlplane.ChannelMCP] || !timelineChannels[controlplane.ChannelAPI] || !timelineChannels[controlplane.ChannelDashboard] {
		t.Fatalf("customer detail channels = %#v, timeline %#v", channels, timelineChannels)
	}
	customer := call(writer, "get_customer", map[string]any{"project_id": integrationProjectID, "customer_id": "mcp-customer-1"})
	raw, _ := json.Marshal(customer)
	if !strings.Contains(string(raw), `"actor_channel":"mcp"`) || !strings.Contains(string(raw), `"revoked_by_channel":"mcp"`) ||
		!strings.Contains(string(raw), `"actor_role":"agent"`) {
		t.Fatalf("get_customer = %s, want the mcp channel and the agent role", raw)
	}

	// A read-only agent is not offered write tools and cannot call them; another project's writer is refused.
	readOnly := connectIntegration(t, stack.server.URL, readOnlyKey)
	if tools, err := readOnly.ListTools(t.Context(), nil); err != nil || len(tools.Tools) != len(expectedReadTools) {
		t.Fatalf("read-only ListTools() = %v, %v, want only the read tools", tools, err)
	}
	refusedArguments := map[string]any{
		"project_id": integrationProjectID, "customer_id": "mcp-customer-1", "external_id": "mcp-alias-refused",
	}
	if result, err := readOnly.CallTool(t.Context(), &sdk.CallToolParams{Name: "put_customer_alias", Arguments: refusedArguments}); err == nil && !result.IsError {
		t.Fatal("read-only put_customer_alias succeeded")
	}
	crossProject, err := connectIntegration(t, stack.server.URL, otherWriterKey).CallTool(t.Context(), &sdk.CallToolParams{
		Name: "put_customer_alias", Arguments: refusedArguments,
	})
	if err != nil {
		t.Fatalf("cross-project CallTool() error = %v", err)
	}
	assertToolError(t, "put_customer_alias", crossProject, "forbidden")
	var refused int
	if err := stack.store.Pool().QueryRow(t.Context(), `
		SELECT count(*) FROM customer_aliases WHERE external_id = 'mcp-alias-refused'
	`).Scan(&refused); err != nil || refused != 0 {
		t.Fatalf("refused alias rows = %d, %v, want none", refused, err)
	}
}

// keyIDFromBearer returns the public key id of an iap_<id>.<secret> bearer.
func keyIDFromBearer(t *testing.T, bearer string) string {
	t.Helper()
	id, _, found := strings.Cut(strings.TrimPrefix(bearer, "iap_"), ".")
	if !found || id == "" {
		t.Fatal("bearer does not have the iap_<id>.<secret> shape")
	}
	return id
}

// dashboardPut sends one same-origin dashboard mutation with a session cookie and an empty JSON body.
func (stack *integrationStack) dashboardPut(t *testing.T, cookie *http.Cookie, path string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, stack.server.URL+path, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", stack.server.URL)
	request.Header.Set("X-IAPStack-Dashboard", "1")
	request.AddCookie(cookie)
	response, err := stack.server.Client().Do(request)
	if err != nil {
		t.Fatalf("dashboard PUT %s error = %v", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		t.Fatalf("dashboard PUT %s = %d %s, want 200", path, response.StatusCode, payload)
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
			Store: store, Operations: store, Admin: store, Setup: store, FinancialReports: reports, Protection: keyring,
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
	return &integrationStack{server: server, store: store}
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
