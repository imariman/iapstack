package dashboard

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const (
	// dashboardOpenAPIContractPath locates the canonical v1 contract from the dashboard package.
	dashboardOpenAPIContractPath = "../contracts/openapi/v1.yaml"
)

// TestDashboardUsesDocumentedAdminOperations verifies every control-plane workflow stays on the public v1 contract.
func TestDashboardUsesDocumentedAdminOperations(t *testing.T) {
	t.Parallel()

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	document, err := loader.LoadFromFile(filepath.Clean(dashboardOpenAPIContractPath))
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	expected := []struct {
		method      string
		path        string
		operationID string
	}{
		{method: http.MethodPost, path: "/v1/admin/api-keys", operationID: "createApiKey"},
		{method: http.MethodGet, path: "/v1/admin/projects", operationID: "listAdminProjects"},
		{method: http.MethodGet, path: "/v1/admin/projects/{project_id}/overview", operationID: "getAdminProjectOverview"},
		{method: http.MethodPut, path: "/v1/admin/projects/{project_id}", operationID: "putAdminProject"},
		{method: http.MethodPut, path: "/v1/admin/projects/{project_id}/applications/{application_id}", operationID: "putAdminApplication"},
		{method: http.MethodPut, path: "/v1/admin/projects/{project_id}/customers/{customer_id}", operationID: "putAdminCustomer"},
		{method: http.MethodPut, path: "/v1/admin/projects/{project_id}/entitlements/{entitlement_id}", operationID: "putAdminEntitlement"},
		{method: http.MethodPut, path: "/v1/admin/projects/{project_id}/products/{product_id}", operationID: "putAdminProduct"},
		{method: http.MethodPut, path: "/v1/admin/projects/{project_id}/applications/{application_id}/store-products/{provider_product_id}", operationID: "putAdminStoreProduct"},
		{method: http.MethodPut, path: "/v1/admin/projects/{project_id}/applications/{application_id}/credentials/{kind}", operationID: "putAdminCredential"},
		{method: http.MethodPut, path: "/v1/admin/projects/{project_id}/applications/{application_id}/webhook", operationID: "putAdminWebhook"},
	}
	for _, operation := range expected {
		pathItem := document.Paths.Value(operation.path)
		if pathItem == nil {
			t.Errorf("OpenAPI path %q is missing", operation.path)
			continue
		}
		documented := pathItem.GetOperation(operation.method)
		if documented == nil || documented.OperationID != operation.operationID {
			t.Errorf("OpenAPI operation %s %s = %#v, want %q", operation.method, operation.path, documented, operation.operationID)
		}
	}

	script, err := assets.ReadFile("app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, fragment := range []string{
		"/v1/admin/api-keys", "/v1/admin/projects", "/overview", "/applications/",
		"/customers/", "/entitlements/", "/products/", "/store-products/", "/credentials/", "/webhook",
	} {
		if !strings.Contains(string(script), fragment) {
			t.Errorf("dashboard script is missing documented route fragment %q", fragment)
		}
	}
}
