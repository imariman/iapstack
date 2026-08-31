package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/credentials"
	"github.com/imariman/iapstack/internal/persistence"
)

const (
	// openAPIContractPath locates the canonical v1 document from this package test directory.
	openAPIContractPath = "../../../contracts/openapi/v1.yaml"
)

// contractOperation identifies one method and path declared by the OpenAPI document.
type contractOperation struct {
	Method      string
	Path        string
	OperationID string
}

// TestOpenAPIContractIsStructurallyValid verifies references, parameters, schemas, and operations comply with OpenAPI 3.
func TestOpenAPIContractIsStructurallyValid(t *testing.T) {
	t.Parallel()

	document := loadOpenAPIContract(t)
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if document.OpenAPI != "3.0.3" {
		t.Fatalf("OpenAPI version = %q, want 3.0.3", document.OpenAPI)
	}
	if document.Info == nil || document.Info.Version != "1.0.0" {
		t.Fatalf("contract info version = %#v, want 1.0.0", document.Info)
	}
}

// TestOpenAPIContractMatchesRegisteredRoutes prevents undocumented endpoints and stale documented operations.
func TestOpenAPIContractMatchesRegisteredRoutes(t *testing.T) {
	t.Parallel()

	documented := contractOperations(loadOpenAPIContract(t))
	registered := (&API{}).v1Routes()
	if len(documented) != len(registered) {
		t.Fatalf("documented operations = %d, registered routes = %d\ndocumented: %s\nregistered: %s",
			len(documented), len(registered), formatContractOperations(documented), formatRoutes(registered))
	}
	byRoute := make(map[string]contractOperation, len(documented))
	for _, operation := range documented {
		key := operation.Method + " " + operation.Path
		if _, exists := byRoute[key]; exists {
			t.Fatalf("duplicate documented operation %s", key)
		}
		byRoute[key] = operation
	}
	operationIDs := make(map[string]string, len(registered))
	for _, route := range registered {
		key := route.Method + " " + route.Path
		operation, exists := byRoute[key]
		if !exists {
			t.Errorf("registered route %s is missing from OpenAPI", key)
			continue
		}
		if operation.OperationID != route.OperationID {
			t.Errorf("operation ID for %s = %q, want %q", key, operation.OperationID, route.OperationID)
		}
		if previous, exists := operationIDs[route.OperationID]; exists {
			t.Errorf("registered operation ID %q is shared by %s and %s", route.OperationID, previous, key)
		}
		operationIDs[route.OperationID] = key
	}
}

// TestOpenAPIContractKeepsStableClientSchemas verifies the SDK and dashboard compatibility boundary remains present.
func TestOpenAPIContractKeepsStableClientSchemas(t *testing.T) {
	t.Parallel()

	document := loadOpenAPIContract(t)
	for name, required := range map[string][]string{
		"AppleCredentialPayload":       {"issuer_id", "key_id", "bundle_id", "private_key", "root_certificates"},
		"AppleEvidence":                {"signed_transaction", "product_kind"},
		"AppleNotificationV2":          {"signedPayload"},
		"GooglePlayCredentialPayload":  {"client_email", "private_key_id", "private_key"},
		"GooglePlayRTDNConfiguration":  {"subscription", "push_service_account_email", "audience"},
		"GooglePlayEvidence":           {"purchase_token", "product_kind"},
		"GooglePlayPubSubPushEnvelope": {"message", "subscription"},
		"GooglePlayPubSubMessage":      {"data", "messageId"},
		"PurchaseSubmission":           {"external_customer_id", "claimed_products", "evidence"},
		"CustomerSessionRequest":       {"external_customer_id"},
		"CustomerSession":              {"token", "expires_at"},
		"DashboardSession":             {"expires_at"},
		"VerificationResult":           {"verified_at", "customer_id", "entitlements"},
		"RestoreResult":                {"results"},
		"EntitlementSnapshot":          {"customer_id", "entitlements"},
		"ErrorEnvelope":                {"error"},
		"AdminProjectOverview":         {"project", "applications", "products", "customers", "recent_transactions", "queues", "recent_webhook_events"},
		"CredentialMetadata":           {"project_id", "application_id", "kind", "content_type", "schema_version", "revision", "created_at", "updated_at"},
		"ApiKeyCollection":             {"api_keys"},
		"ApiKey":                       {"id", "role", "created_at", "current"},
	} {
		schemaReference, exists := document.Components.Schemas[name]
		if !exists || schemaReference.Value == nil {
			t.Fatalf("schema %q is missing", name)
		}
		for _, field := range required {
			if _, exists := schemaReference.Value.Properties[field]; !exists {
				t.Errorf("schema %q is missing property %q", name, field)
			}
			if !containsString(schemaReference.Value.Required, field) {
				t.Errorf("schema %q property %q is not required", name, field)
			}
		}
	}
}

// TestGoResponsesMatchOpenAPI validates representative server JSON against the canonical response schemas.
func TestGoResponsesMatchOpenAPI(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	document := loadOpenAPIContract(t)
	tests := []struct {
		name   string
		schema string
		value  any
	}{
		{name: "error envelope", schema: "ErrorEnvelope", value: errorEnvelope{Error: apiError{
			Code: "invalid_request", Message: "request values are invalid", RequestID: "request-1",
		}}},
		{name: "verification result", schema: "VerificationResult", value: verificationResponse{
			VerifiedAt: now, CustomerID: "customer-1", Entitlements: []entitlementResponse{{
				Key: "premium", Access: core.AccessAllowed, Reason: core.AccessReasonPurchaseValid,
				Version: 1,
			}},
		}},
		{name: "customer session", schema: "CustomerSession", value: customerSessionResponse{
			Token: "iaps_opaque", ExpiresAt: now.Add(15 * time.Minute),
		}},
		{name: "dashboard session", schema: "DashboardSession", value: dashboardSessionResponse{
			ExpiresAt: now.Add(12 * time.Hour),
		}},
		{name: "project collection", schema: "AdminProjects", value: adminProjectsResponse{
			Projects: []adminProjectResponse{{ID: "project-1", CreatedAt: now}},
		}},
		{name: "API key collection", schema: "ApiKeyCollection", value: apiKeyCollectionResponse{
			APIKeys: []apiKeyResponse{{
				ID: "0123456789abcdef01234567", Role: persistence.APIKeyRoleApplication,
				ProjectID: "project-1", ApplicationID: "application-1", CreatedAt: now, Current: false,
			}},
		}},
		{name: "project overview", schema: "AdminProjectOverview", value: adminOverviewResponse{
			Project:      adminProjectResponse{ID: "project-1", CreatedAt: now},
			Applications: []adminApplicationResponse{}, Products: []adminProductResponse{},
			Customers: []adminCustomerResponse{}, RecentTransactions: []adminTransactionResponse{},
			Queues: []adminQueueResponse{}, RecentWebhookEvents: []adminWebhookEventResponse{},
		}},
		{name: "credential metadata", schema: "CredentialMetadata", value: credentials.Metadata{
			ProjectID: "project-1", ApplicationID: "application-1", Kind: "huawei_server_api",
			ContentType: "application/vnd.iapstack.huawei-credentials+json", SchemaVersion: 1,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		}},
		{name: "Apple credential metadata", schema: "CredentialMetadata", value: credentials.Metadata{
			ProjectID: "project-1", ApplicationID: "application-2", Kind: "apple_app_store_server_api",
			ContentType: "application/vnd.iapstack.apple-credentials+json", SchemaVersion: 1,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		}},
		{name: "Google Play credential metadata", schema: "CredentialMetadata", value: credentials.Metadata{
			ProjectID: "project-1", ApplicationID: "application-3", Kind: "google_play_android_publisher",
			ContentType: "application/vnd.iapstack.google-play-credentials+json", SchemaVersion: 1,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		}},
		{name: "webhook metadata", schema: "WebhookConfiguration", value: map[string]any{
			"url": "https://example.com/iapstack/events", "revision": int64(1),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			validateResponseAgainstSchema(t, document, test.schema, test.value)
		})
	}
}

// loadOpenAPIContract loads the canonical repository contract and fails the calling test on errors.
func loadOpenAPIContract(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromFile(filepath.Clean(openAPIContractPath))
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	return document
}

// validateResponseAgainstSchema converts a Go response through JSON and applies the named OpenAPI schema.
func validateResponseAgainstSchema(t *testing.T, document *openapi3.T, schemaName string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	schemaReference, exists := document.Components.Schemas[schemaName]
	if !exists || schemaReference.Value == nil {
		t.Fatalf("schema %q is missing", schemaName)
	}
	if err := document.ValidateSchemaJSON(schemaReference.Value, decoded); err != nil {
		t.Fatalf("response %s does not match schema %q: %v", payload, schemaName, err)
	}
}

// contractOperations flattens supported HTTP methods from an OpenAPI path collection.
func contractOperations(document *openapi3.T) []contractOperation {
	operations := make([]contractOperation, 0)
	for path, item := range document.Paths.Map() {
		for _, method := range []string{
			http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
		} {
			operation := item.GetOperation(method)
			if operation == nil {
				continue
			}
			operations = append(operations, contractOperation{
				Method: method, Path: path, OperationID: operation.OperationID,
			})
		}
	}
	sort.Slice(operations, func(left, right int) bool {
		return operations[left].Method+operations[left].Path < operations[right].Method+operations[right].Path
	})
	return operations
}

// formatContractOperations returns deterministic diagnostics for documented operation mismatches.
func formatContractOperations(operations []contractOperation) string {
	values := make([]string, 0, len(operations))
	for _, operation := range operations {
		values = append(values, fmt.Sprintf("%s %s (%s)", operation.Method, operation.Path, operation.OperationID))
	}
	return strings.Join(values, ", ")
}

// formatRoutes returns deterministic diagnostics for registered route mismatches.
func formatRoutes(routes []v1Route) string {
	values := make([]string, 0, len(routes))
	for _, route := range routes {
		values = append(values, fmt.Sprintf("%s %s (%s)", route.Method, route.Path, route.OperationID))
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}

// containsString reports whether a required-property collection contains one exact field.
func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
