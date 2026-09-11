package iapstack

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	// parentModulePath is the server module that this SDK must not import.
	parentModulePath = "github.com/imariman/iapstack"
)

// TestOpenAPIDeclaresHostOperations pins the SDK to declared v1 operations and fields.
func TestOpenAPIDeclaresHostOperations(t *testing.T) {
	t.Parallel()

	contract := readRepositoryFile(t, "contracts", "openapi", "v1.yaml")
	for _, needle := range []string{
		"operationId: createCustomerSession",
		"operationId: getCustomerEntitlements",
		"applicationBearer:",
		"customerSessionBearer:",
		"external_customer_id",
		"expires_at",
		"customer_id",
		"entitlements",
		"ErrorEnvelope",
		"ApiError",
	} {
		if !strings.Contains(contract, needle) {
			t.Fatalf("OpenAPI contract is missing %q", needle)
		}
	}
	sessionsBlock := sectionAfter(contract, "/v1/applications/{application_id}/customer-sessions:")
	if !strings.Contains(sessionsBlock, "- applicationBearer: []") {
		t.Fatal("createCustomerSession must require the durable application bearer")
	}
	entitlementsBlock := sectionAfter(contract, "/v1/applications/{application_id}/customers/{external_customer_id}/entitlements:")
	if !strings.Contains(entitlementsBlock, "- customerSessionBearer: []") {
		t.Fatal("getCustomerEntitlements must stay on the customer-session host lookup path")
	}
	if strings.Contains(entitlementsBlock, "- applicationBearer: []") {
		t.Fatal("getCustomerEntitlements must not claim application-bearer authentication")
	}
}

// TestWebhookContractFieldsAreDocumented pins verifier headers to the public webhook contract.
func TestWebhookContractFieldsAreDocumented(t *testing.T) {
	t.Parallel()

	document := readRepositoryFile(t, "docs", "api-v1.md")
	for _, needle := range []string{
		"IAPStack-Event-ID",
		"IAPStack-Timestamp",
		"IAPStack-Signature",
		"HMAC-SHA-256",
		"v2=<hex>",
		"<event_id>",
		"<unix_timestamp>",
		"<exact_raw_body>",
	} {
		if !strings.Contains(document, needle) {
			t.Fatalf("webhook contract is missing %q", needle)
		}
	}
}

// TestSDKDoesNotImportInternalPackages enforces ADR-0004 for the host SDK.
func TestSDKDoesNotImportInternalPackages(t *testing.T) {
	t.Parallel()

	fileSet := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, spec := range file.Imports {
			imported, quoteErr := strconv.Unquote(spec.Path.Value)
			if quoteErr != nil {
				return quoteErr
			}
			if imported == parentModulePath || strings.HasPrefix(imported, parentModulePath+"/") &&
				!strings.HasPrefix(imported, parentModulePath+"/sdk/go") {
				t.Errorf("%s imports %s", path, imported)
			}
			if strings.Contains(imported, "/internal/") {
				t.Errorf("%s imports internal package %s", path, imported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestModuleHasNoThirdPartyDependencies keeps the host SDK stdlib-only.
func TestModuleHasNoThirdPartyDependencies(t *testing.T) {
	t.Parallel()

	module, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if strings.Contains(string(module), "require (") || strings.Contains(string(module), "\nrequire ") {
		t.Fatalf("host SDK go.mod must not require other modules:\n%s", module)
	}
}

// readRepositoryFile loads one repository path relative to sdk/go.
func readRepositoryFile(t *testing.T, parts ...string) string {
	t.Helper()

	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// sectionAfter returns the contract text following a path key until the next absolute path.
func sectionAfter(document, marker string) string {
	start := strings.Index(document, marker)
	if start < 0 {
		return ""
	}
	rest := document[start:]
	next := strings.Index(rest[len(marker):], "\n  /")
	if next < 0 {
		return rest
	}
	return rest[:len(marker)+next]
}
