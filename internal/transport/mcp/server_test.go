package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/imariman/iapstack/internal/controlplane"
	"github.com/imariman/iapstack/internal/persistence"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// goldenCatalogPath is the committed tool catalogue the server must match.
	goldenCatalogPath = "testdata/tool_catalog.json"
	// mcpDocumentationPath is the operator documentation that must describe every tool.
	mcpDocumentationPath = "../../../docs/mcp.md"
)

// goldenCatalog is the pinned, versioned tool catalogue.
type goldenCatalog struct {
	CatalogVersion string           `json:"catalog_version"`
	Tools          []map[string]any `json:"tools"`
}

// lockedBuffer collects log output from concurrent handlers.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

var (
	// updateGolden rewrites the committed tool catalogue from the running server.
	updateGolden = flag.Bool("update", false, "rewrite testdata/tool_catalog.json")
	// expectedTools lists the complete phase 1 tool allowlist.
	expectedTools = []string{
		"get_customer", "get_project_overview", "get_setup_status", "get_subscription_metrics",
		"list_financial_reports", "list_projects", "resolve_customer", "search_customers",
	}
	// protectedFieldFragments name data that must never appear as a field in any tool output or schema.
	protectedFieldFragments = []string{
		"receipt", "purchase_token", "signed", "evidence_", "_evidence_payload", "payload", "ciphertext",
		"fingerprint", "secret", "sha256", "private_key", "password", "credential_value",
	}
	// protectedFieldNames are exact field names that must never appear, where a fragment match would be too broad.
	protectedFieldNames = map[string]bool{"evidence": true, "token": true, "jws": true, "receipt_data": true}
)

// TestToolCatalogMatchesGolden pins tool names, annotations, and schemas against the committed catalogue.
func TestToolCatalogMatchesGolden(t *testing.T) {
	session := connect(t, newTestServer(t, newTestHandler(t, Options{})), adminBearer)
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	catalog := goldenCatalog{CatalogVersion: CatalogVersion}
	for _, tool := range listed.Tools {
		var entry map[string]any
		payload, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal tool %s: %v", tool.Name, err)
		}
		if err := json.Unmarshal(payload, &entry); err != nil {
			t.Fatalf("decode tool %s: %v", tool.Name, err)
		}
		catalog.Tools = append(catalog.Tools, entry)
	}
	sort.Slice(catalog.Tools, func(left, right int) bool {
		return catalog.Tools[left]["name"].(string) < catalog.Tools[right]["name"].(string)
	})
	actual, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatalf("marshal catalogue: %v", err)
	}
	actual = append(actual, '\n')
	if *updateGolden {
		if err := os.WriteFile(goldenCatalogPath, actual, 0o600); err != nil {
			t.Fatalf("write golden catalogue: %v", err)
		}
	}
	expected, err := os.ReadFile(goldenCatalogPath)
	if err != nil {
		t.Fatalf("read golden catalogue: %v", err)
	}
	if !bytes.Equal(expected, actual) {
		t.Fatalf("tool catalogue differs from %s; bump CatalogVersion when tools change, then run "+
			"go test ./internal/transport/mcp -run TestToolCatalogMatchesGolden -update\n%s", goldenCatalogPath, actual)
	}

	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil ||
			*tool.Annotations.DestructiveHint || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Fatalf("tool %s annotations = %#v, want read-only, non-destructive, closed world", tool.Name, tool.Annotations)
		}
		input, ok := tool.InputSchema.(map[string]any)
		if !ok || input["type"] != "object" || input["additionalProperties"] != false {
			t.Fatalf("tool %s input schema = %#v, want a strict object", tool.Name, tool.InputSchema)
		}
		if tool.OutputSchema == nil {
			t.Fatalf("tool %s has no output schema", tool.Name)
		}
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(expectedTools, ",") {
		t.Fatalf("tools = %v, want exactly %v", names, expectedTools)
	}
}

// TestEveryToolIsDocumented verifies docs/mcp.md names the catalogue version and every tool.
func TestEveryToolIsDocumented(t *testing.T) {
	t.Parallel()

	documentation, err := os.ReadFile(filepath.Clean(mcpDocumentationPath))
	if err != nil {
		t.Fatalf("read MCP documentation: %v", err)
	}
	text := string(documentation)
	if !strings.Contains(text, "Tool catalogue version: "+CatalogVersion) {
		t.Fatalf("docs/mcp.md does not state tool catalogue version %s", CatalogVersion)
	}
	for _, definition := range toolDefinitions(controlplane.New(controlplane.Dependencies{}), slog.Default()) {
		if !strings.Contains(text, "### `"+definition.tool.Name+"`") {
			t.Fatalf("docs/mcp.md has no section for tool %s", definition.tool.Name)
		}
	}
}

// TestEndpointRejectsUnsupportedRequests verifies methods, origins, and credentials are checked before MCP work.
func TestEndpointRejectsUnsupportedRequests(t *testing.T) {
	server := newTestServer(t, newTestHandler(t, Options{BodyLimit: 2048}))
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18",` +
		`"capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`
	for _, testCase := range []struct {
		name   string
		method string
		header map[string]string
		body   string
		status int
		code   string
	}{
		{name: "GET", method: http.MethodGet, header: map[string]string{"Authorization": "Bearer " + adminBearer}, status: http.StatusMethodNotAllowed, code: "method_not_allowed"},
		{name: "DELETE", method: http.MethodDelete, header: map[string]string{"Authorization": "Bearer " + adminBearer}, status: http.StatusMethodNotAllowed, code: "method_not_allowed"},
		{name: "missing bearer", method: http.MethodPost, body: initialize, status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "garbage bearer", method: http.MethodPost, header: map[string]string{"Authorization": "Bearer not-a-key"}, body: initialize, status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "basic credentials", method: http.MethodPost, header: map[string]string{"Authorization": "Basic YWRtaW46YWRtaW4="}, body: initialize, status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "dashboard session cookie", method: http.MethodPost, header: map[string]string{"Cookie": "__Secure-iapstack_admin=session-value"}, body: initialize, status: http.StatusUnauthorized, code: "unauthorized"},
		{name: "application key", method: http.MethodPost, header: map[string]string{"Authorization": "Bearer " + applicationBearer}, body: initialize, status: http.StatusForbidden, code: "forbidden"},
		{name: "bootstrap administrator", method: http.MethodPost, header: map[string]string{"Authorization": "Bearer " + bootstrapBearer}, body: initialize, status: http.StatusForbidden, code: "forbidden"},
		{name: "authentication capacity", method: http.MethodPost, header: map[string]string{"Authorization": "Bearer " + capacityBearer}, body: initialize, status: http.StatusServiceUnavailable, code: "authentication_unavailable"},
		{name: "cross-origin browser", method: http.MethodPost, header: map[string]string{"Authorization": "Bearer " + adminBearer, "Origin": "https://evil.example"}, body: initialize, status: http.StatusForbidden, code: "origin_rejected"},
		{name: "opaque origin", method: http.MethodPost, header: map[string]string{"Authorization": "Bearer " + adminBearer, "Origin": "null"}, body: initialize, status: http.StatusForbidden, code: "origin_rejected"},
		{name: "oversized body", method: http.MethodPost, header: map[string]string{"Authorization": "Bearer " + adminBearer}, body: `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` + strings.Repeat("a", 4096) + `"}}`, status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), testCase.method, server.URL+Path, strings.NewReader(testCase.body))
			if err != nil {
				t.Fatalf("NewRequest() error = %v", err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			for name, value := range testCase.header {
				request.Header.Set(name, value)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatalf("Do() error = %v", err)
			}
			defer response.Body.Close()
			var envelope errorEnvelope
			_ = json.NewDecoder(response.Body).Decode(&envelope)
			if response.StatusCode != testCase.status || (testCase.code != "" && envelope.Error.Code != testCase.code) {
				t.Fatalf("status = %d code %q, want %d %q", response.StatusCode, envelope.Error.Code, testCase.status, testCase.code)
			}
			if response.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("response carries a CORS header")
			}
			switch testCase.status {
			case http.StatusMethodNotAllowed:
				if response.Header.Get("Allow") != http.MethodPost {
					t.Fatalf("Allow = %q, want POST", response.Header.Get("Allow"))
				}
			case http.StatusUnauthorized:
				if !strings.HasPrefix(response.Header.Get("WWW-Authenticate"), "Bearer") {
					t.Fatalf("WWW-Authenticate = %q, want a bearer challenge", response.Header.Get("WWW-Authenticate"))
				}
			case http.StatusServiceUnavailable:
				if response.Header.Get("Retry-After") == "" {
					t.Fatal("capacity rejection has no Retry-After")
				}
			}
		})
	}

	sameOrigin, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+Path, strings.NewReader(initialize))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	sameOrigin.Header.Set("Authorization", "Bearer "+agentBearer)
	sameOrigin.Header.Set("Content-Type", "application/json")
	sameOrigin.Header.Set("Accept", "application/json, text/event-stream")
	sameOrigin.Header.Set("Origin", server.URL)
	response, err := server.Client().Do(sameOrigin)
	if err != nil {
		t.Fatalf("same-origin Do() error = %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("same-origin initialize = %d %q %q, want a plain JSON no-store 200", response.StatusCode,
			response.Header.Get("Content-Type"), response.Header.Get("Cache-Control"))
	}
}

// TestPerKeyConcurrencyCapRejectsWithRetryAfter verifies the cap is per key and leaves no state behind.
func TestPerKeyConcurrencyCapRejectsWithRetryAfter(t *testing.T) {
	handler := newTestHandler(t, Options{MaxConcurrentRequestsPerKey: 2})
	server := newTestServer(t, handler)
	for range 2 {
		if !handler.limiter.acquire("agent-key") {
			t.Fatal("acquire() = false below the cap")
		}
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+Path, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+agentBearer)
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "" {
		t.Fatalf("over-cap status = %d Retry-After %q, want 429 with Retry-After", response.StatusCode, response.Header.Get("Retry-After"))
	}

	// Another key is unaffected by the saturated one.
	other := connect(t, server, otherAgentBearer)
	if _, err := other.ListTools(t.Context(), nil); err != nil {
		t.Fatalf("other key ListTools() error = %v", err)
	}
	handler.limiter.release("agent-key")
	handler.limiter.release("agent-key")
	session := connect(t, server, agentBearer)
	if _, err := session.ListTools(t.Context(), nil); err != nil {
		t.Fatalf("ListTools() after release error = %v", err)
	}
	if size := handler.limiter.size(); size != 0 {
		t.Fatalf("limiter tracks %d idle keys, want none", size)
	}
}

// TestEveryToolReturnsSchemaValidRedactedOutput calls every tool and scans results and schemas for protected data.
func TestEveryToolReturnsSchemaValidRedactedOutput(t *testing.T) {
	logs := &lockedBuffer{}
	handler := newTestHandler(t, Options{Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	server := newTestServer(t, handler)
	sentinelDigest := sha256.Sum256([]byte(sentinelReportBytes))
	sentinels := []string{sentinelWebhookQuery, hex.EncodeToString(sentinelDigest[:])}
	for _, bearer := range []string{agentBearer, adminBearer} {
		session := connect(t, server, bearer)
		listed, err := session.ListTools(t.Context(), nil)
		if err != nil {
			t.Fatalf("ListTools() error = %v", err)
		}
		schemas := make(map[string]*jsonschema.Resolved, len(listed.Tools))
		for _, tool := range listed.Tools {
			assertNoProtectedFields(t, tool.Name+" output schema", tool.OutputSchema)
			if strings.Contains(tool.Description, untrustedExternalID) {
				t.Fatalf("tool %s description contains customer data", tool.Name)
			}
			payload, _ := json.Marshal(tool.OutputSchema)
			var schema jsonschema.Schema
			if err := json.Unmarshal(payload, &schema); err != nil {
				t.Fatalf("decode %s output schema: %v", tool.Name, err)
			}
			resolved, err := schema.Resolve(nil)
			if err != nil {
				t.Fatalf("resolve %s output schema: %v", tool.Name, err)
			}
			schemas[tool.Name] = resolved
		}
		calls := map[string]map[string]any{
			"list_projects":            {},
			"get_project_overview":     {"project_id": "project-1"},
			"get_subscription_metrics": {"project_id": "project-1", "environment": "sandbox", "window_days": 7},
			"search_customers":         {"project_id": "project-1", "query": "customer-1", "limit": 5},
			"get_customer":             {"project_id": "project-1", "customer_id": "customer-1"},
			"resolve_customer":         {"project_id": "project-1", "external_id": untrustedExternalID},
			"list_financial_reports":   {"project_id": "project-1", "page_size": 10},
			"get_setup_status":         {"project_id": "project-1"},
		}
		if len(calls) != len(expectedTools) {
			t.Fatalf("redaction test calls %d tools, want %d", len(calls), len(expectedTools))
		}
		for name, arguments := range calls {
			result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: arguments})
			if err != nil {
				t.Fatalf("CallTool(%s) error = %v", name, err)
			}
			raw, _ := json.Marshal(result)
			if result.IsError {
				t.Fatalf("CallTool(%s) = %s, want success", name, raw)
			}
			if err := schemas[name].Validate(result.StructuredContent); err != nil {
				t.Fatalf("CallTool(%s) output does not match its schema: %v", name, err)
			}
			for _, sentinel := range sentinels {
				if strings.Contains(string(raw), sentinel) {
					t.Fatalf("CallTool(%s) leaked sentinel %q: %s", name, sentinel, raw)
				}
			}
			assertNoProtectedFields(t, name+" result", result.StructuredContent)
		}
	}

	setup, err := connect(t, server, agentBearer).CallTool(t.Context(), &sdk.CallToolParams{
		Name: "get_setup_status", Arguments: map[string]any{"project_id": "project-1"},
	})
	if err != nil {
		t.Fatalf("CallTool(get_setup_status) error = %v", err)
	}
	raw, _ := json.Marshal(setup.StructuredContent)
	if !strings.Contains(string(raw), `"url":"https://iap.example.com/v1/providers/apple/projects/project-1/applications/ios-app/notifications"`) ||
		!strings.Contains(string(raw), `"url":"https://hooks.example.com/iapstack"`) {
		t.Fatalf("setup status = %s, want the notification URL and the webhook URL without its query", raw)
	}

	logged := logs.String()
	if strings.Contains(logged, untrustedExternalID) || strings.Contains(logged, "customer-1") {
		t.Fatalf("tool logs contain arguments or results: %s", logged)
	}
	if !strings.Contains(logged, `"tool":"resolve_customer"`) || !strings.Contains(logged, `"key_id":"agent-key"`) ||
		!strings.Contains(logged, `"outcome":"ok"`) || !strings.Contains(logged, `"duration_ms"`) {
		t.Fatalf("tool logs = %s, want one structured line per call", logged)
	}
}

// TestCrossProjectAgentGetsForbiddenToolErrors verifies an agent key never reads another project.
func TestCrossProjectAgentGetsForbiddenToolErrors(t *testing.T) {
	session := connect(t, newTestServer(t, newTestHandler(t, Options{})), otherAgentBearer)
	for name, arguments := range map[string]map[string]any{
		"get_project_overview":     {"project_id": "project-1"},
		"get_subscription_metrics": {"project_id": "project-1"},
		"search_customers":         {"project_id": "project-1"},
		"get_customer":             {"project_id": "project-1", "customer_id": "customer-1"},
		"resolve_customer":         {"project_id": "project-1", "external_id": untrustedExternalID},
		"list_financial_reports":   {"project_id": "project-1"},
		"get_setup_status":         {"project_id": "project-1"},
	} {
		result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: arguments})
		if err != nil {
			t.Fatalf("CallTool(%s) error = %v", name, err)
		}
		assertToolError(t, name, result, "forbidden")
	}
	projects, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "list_projects", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(list_projects) error = %v", err)
	}
	raw, _ := json.Marshal(projects.StructuredContent)
	if projects.IsError || strings.Contains(string(raw), "project-1") || !strings.Contains(string(raw), "project-2") {
		t.Fatalf("list_projects for the project-2 agent = %s, want only project-2", raw)
	}
}

// TestInvalidToolArgumentsAreRejectedWithoutEcho verifies strict schemas and fixed error prose.
func TestInvalidToolArgumentsAreRejectedWithoutEcho(t *testing.T) {
	session := connect(t, newTestServer(t, newTestHandler(t, Options{})), agentBearer)
	for _, testCase := range []struct {
		name      string
		arguments map[string]any
		code      string
	}{
		{name: "get_customer", arguments: map[string]any{"project_id": "project-1"}, code: "invalid_request"},
		{name: "get_customer", arguments: map[string]any{"project_id": "project-1", "customer_id": "c", "extra": "x-unknown-field"}, code: "invalid_request"},
		{name: "get_subscription_metrics", arguments: map[string]any{"project_id": "project-1", "window_days": "seven"}, code: "invalid_request"},
		{name: "get_subscription_metrics", arguments: map[string]any{"project_id": "project-1", "window_days": 365}, code: "invalid_request"},
		{name: "list_financial_reports", arguments: map[string]any{"project_id": "project-1", "page_size": 0}, code: "invalid_request"},
		{name: "list_financial_reports", arguments: map[string]any{"project_id": "project-1", "page_token": "x-forged-token"}, code: "invalid_request"},
		{name: "search_customers", arguments: map[string]any{"project_id": "project-1", "page_token": "x-forged-token"}, code: "invalid_request"},
		{name: "resolve_customer", arguments: map[string]any{"project_id": "project-1", "external_id": "x-missing-customer"}, code: "not_found"},
	} {
		result, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: testCase.name, Arguments: testCase.arguments})
		if err != nil {
			t.Fatalf("CallTool(%s) error = %v", testCase.name, err)
		}
		assertToolError(t, testCase.name, result, testCase.code)
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), "x-unknown-field") || strings.Contains(string(raw), "x-forged-token") ||
			strings.Contains(string(raw), "x-missing-customer") || strings.Contains(string(raw), "seven") {
			t.Fatalf("CallTool(%s) error echoes its arguments: %s", testCase.name, raw)
		}
	}
}

// TestListToolsPaginate verifies list tools page with opaque tokens bound to their list and scope.
func TestListToolsPaginate(t *testing.T) {
	session := connect(t, newTestServer(t, newTestHandler(t, Options{})), adminBearer)
	first, err := session.CallTool(t.Context(), &sdk.CallToolParams{Name: "list_projects", Arguments: map[string]any{"page_size": 1}})
	if err != nil || first.IsError {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	page := first.StructuredContent.(map[string]any)
	token, _ := page["next_page_token"].(string)
	if len(page["projects"].([]any)) != 1 || token == "" {
		t.Fatalf("first page = %#v, want one project and a token", page)
	}
	second, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "list_projects", Arguments: map[string]any{"page_size": 1, "page_token": token},
	})
	if err != nil || second.IsError {
		t.Fatalf("second page = %#v, %v", second, err)
	}
	last := second.StructuredContent.(map[string]any)
	if _, more := last["next_page_token"]; more || last["projects"].([]any)[0].(map[string]any)["id"] != "project-2" {
		t.Fatalf("second page = %#v, want project-2 and no token", last)
	}
	reused, err := session.CallTool(t.Context(), &sdk.CallToolParams{
		Name: "list_financial_reports", Arguments: map[string]any{"project_id": "project-1", "page_token": token},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	assertToolError(t, "list_financial_reports", reused, "invalid_request")
}

// TestOversizedToolResultIsRejected verifies the response size cap returns a typed tool error.
func TestOversizedToolResultIsRejected(t *testing.T) {
	t.Parallel()

	definition := defineTool(&sdk.Tool{Name: "oversized"}, slog.New(slog.DiscardHandler), nil,
		func(context.Context, controlplane.Actor, struct{}) (map[string]string, error) {
			return map[string]string{"value": strings.Repeat("a", MaxResponseBytes)}, nil
		})
	ctx := context.WithValue(context.Background(), actorContextKey{}, controlplane.Actor{
		Role: persistence.APIKeyRoleAdmin, KeyID: "admin-key",
	})
	result, err := definition.handler(ctx, &sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{Name: "oversized"}})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	assertToolError(t, "oversized", result, codeResponseTooLarge)

	unauthenticated, err := definition.handler(context.Background(), &sdk.CallToolRequest{Params: &sdk.CallToolParamsRaw{Name: "oversized"}})
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	assertToolError(t, "oversized", unauthenticated, codeUnauthorized)
}

// TestWriteGateRejectsReadOnlyAgentKeys verifies MCP actors carry the key's write flag into the shared write gate.
func TestWriteGateRejectsReadOnlyAgentKeys(t *testing.T) {
	t.Parallel()

	for bearer, allowed := range map[string]bool{agentBearer: false, writerBearer: true, adminBearer: true} {
		principal, err := fakeAuthenticator{}.Authenticate(context.Background(), bearer)
		if err != nil {
			t.Fatalf("Authenticate(%s) error = %v", bearer, err)
		}
		actor := controlplane.ActorFromPrincipal(principal, controlplane.ChannelMCP)
		err = actor.AuthorizeWrite("project-1")
		if (err == nil) != allowed || (err != nil && !errors.Is(err, controlplane.ErrForbidden)) {
			t.Fatalf("AuthorizeWrite() for %s = %v, want allowed %t", bearer, err, allowed)
		}
		if actor.Channel != controlplane.ChannelMCP {
			t.Fatalf("actor channel = %q, want mcp", actor.Channel)
		}
	}
	writer, _ := fakeAuthenticator{}.Authenticate(context.Background(), writerBearer)
	if err := controlplane.ActorFromPrincipal(writer, controlplane.ChannelMCP).AuthorizeWrite("project-2"); !errors.Is(err, controlplane.ErrForbidden) {
		t.Fatalf("AuthorizeWrite() cross-project = %v, want ErrForbidden", err)
	}
}

// TestOpenAPIResourceIsServed verifies the embedded contract resource.
func TestOpenAPIResourceIsServed(t *testing.T) {
	session := connect(t, newTestServer(t, newTestHandler(t, Options{})), agentBearer)
	resources, err := session.ListResources(t.Context(), nil)
	if err != nil || len(resources.Resources) != 1 || resources.Resources[0].URI != OpenAPIResourceURI {
		t.Fatalf("ListResources() = %#v, %v", resources, err)
	}
	read, err := session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: OpenAPIResourceURI})
	if err != nil || len(read.Contents) != 1 || !strings.HasPrefix(read.Contents[0].Text, "openapi:") {
		t.Fatalf("ReadResource() = %#v, %v", read, err)
	}
}

// TestNewHandlerValidatesOptions verifies incomplete dependencies fail fast.
func TestNewHandlerValidatesOptions(t *testing.T) {
	t.Parallel()

	service := controlplane.New(controlplane.Dependencies{})
	for name, options := range map[string]Options{
		"no authentication": {ControlPlane: service, BodyLimit: 1},
		"no control plane":  {Authentication: fakeAuthenticator{}, BodyLimit: 1},
		"no body limit":     {Authentication: fakeAuthenticator{}, ControlPlane: service},
		"negative per key":  {Authentication: fakeAuthenticator{}, ControlPlane: service, BodyLimit: 1, MaxConcurrentRequestsPerKey: -1},
	} {
		if _, err := NewHandler(options); err == nil {
			t.Fatalf("NewHandler(%s) error = nil", name)
		}
	}
}

// assertToolError verifies one isError result whose only text content carries a stable code. A failed call
// must not set structured content, because clients validate it against the success output schema.
func assertToolError(t *testing.T, name string, result *sdk.CallToolResult, code string) {
	t.Helper()
	if !result.IsError || result.StructuredContent != nil || len(result.Content) != 1 {
		t.Fatalf("CallTool(%s) = isError %t, structured %v, %d contents, want one text error without structured content",
			name, result.IsError, result.StructuredContent, len(result.Content))
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s) content = %T, want text", name, result.Content[0])
	}
	var output toolErrorOutput
	if err := json.Unmarshal([]byte(text.Text), &output); err != nil || output.Error.Code != code || output.Error.Message == "" {
		t.Fatalf("CallTool(%s) error text = %s, want error code %s", name, text.Text, code)
	}
}

// assertNoProtectedFields walks a decoded JSON value and fails on any protected field name.
func assertNoProtectedFields(t *testing.T, location string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			lowered := strings.ToLower(key)
			if protectedFieldNames[lowered] {
				t.Fatalf("%s contains protected field %q", location, key)
			}
			for _, fragment := range protectedFieldFragments {
				if strings.Contains(lowered, fragment) {
					t.Fatalf("%s contains protected field %q", location, key)
				}
			}
			assertNoProtectedFields(t, location, nested)
		}
	case []any:
		for _, nested := range typed {
			assertNoProtectedFields(t, location, nested)
		}
	default:
		raw, err := json.Marshal(value)
		if err != nil || len(raw) == 0 || (raw[0] != '{' && raw[0] != '[') {
			return
		}
		var decoded any
		if json.Unmarshal(raw, &decoded) == nil {
			assertNoProtectedFields(t, location, decoded)
		}
	}
}

// Write appends log output under the buffer lock.
func (buffer *lockedBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(payload)
}

// String returns the collected log output.
func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}
