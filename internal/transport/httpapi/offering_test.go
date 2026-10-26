package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/offering"
	"github.com/imariman/iapstack/internal/persistence"
)

// offeringMembershipCatalog is a read-only store-product double. It has no insert method.
type offeringMembershipCatalog struct {
	products map[core.ApplicationID]map[core.ProviderProductID]struct{}
	calls    int
}

// ConfirmStoreProducts accepts only provider products already present for the application.
func (catalog *offeringMembershipCatalog) ConfirmStoreProducts(
	_ context.Context,
	applicationID core.ApplicationID,
	ids []core.ProviderProductID,
) error {
	catalog.calls++
	known := catalog.products[applicationID]
	for _, id := range ids {
		if _, exists := known[id]; !exists {
			return persistence.ErrNotFound
		}
	}
	return nil
}

// TestGetApplicationOfferingReturnsHappyDocument verifies the public body, revision ETag, and cache policy.
func TestGetApplicationOfferingReturnsHappyDocument(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	writeOfferingHTTPFixture(t, directory, happyOfferingDocument("application-1"))
	catalog := &offeringMembershipCatalog{products: map[core.ApplicationID]map[core.ProviderProductID]struct{}{
		"application-1": {"premium_monthly": {}},
	}}
	api := newOfferingAPI(directory, catalog)

	recorder := getOffering(api, "application-1", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("ETag") != `"revision-1"` {
		t.Fatalf("ETag = %q, want quoted revision", recorder.Header().Get("ETag"))
	}
	if recorder.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	assertOfferingAllowlist(t, recorder.Body.Bytes())
	var document offering.Document
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if document.SchemaVersion != 1 || document.ApplicationID != "application-1" ||
		document.Revision != "revision-1" || document.Offerings[0].Packages[0].StoreProductID != "premium_monthly" {
		t.Fatalf("document = %#v", document)
	}
}

// TestGetApplicationOfferingRejectsUnknownStoreProduct verifies a bad file is not served and a sibling still is.
func TestGetApplicationOfferingRejectsUnknownStoreProduct(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	rejected := happyOfferingDocument("application-bad")
	rejected.Offerings[0].Packages = append(rejected.Offerings[0].Packages, offering.Package{
		ID: "extra", StoreProductID: "missing_product", DisplayKey: "missing_product",
	})
	writeOfferingHTTPFixture(t, directory, rejected)
	writeOfferingHTTPFixture(t, directory, happyOfferingDocument("application-ok"))
	catalog := &offeringMembershipCatalog{products: map[core.ApplicationID]map[core.ProviderProductID]struct{}{
		"application-bad": {"premium_monthly": {}},
		"application-ok":  {"premium_monthly": {}},
	}}
	before := len(catalog.products["application-bad"])
	api := newOfferingAPI(directory, catalog)

	rejectedResponse := getOffering(api, "application-bad", "customer_id=customer-sentinel", nil)
	assertOfferingNotFound(t, rejectedResponse)
	if strings.Contains(rejectedResponse.Body.String(), "missing_product") ||
		strings.Contains(rejectedResponse.Body.String(), "premium_monthly") ||
		strings.Contains(rejectedResponse.Body.String(), "customer-sentinel") {
		t.Fatalf("rejected offering leaked document or customer data: %s", rejectedResponse.Body.String())
	}
	if len(catalog.products["application-bad"]) != before {
		t.Fatal("unknown store product created a catalog row")
	}

	sibling := getOffering(api, "application-ok", "", nil)
	if sibling.Code != http.StatusOK || !strings.Contains(sibling.Body.String(), `"application_id":"application-ok"`) {
		t.Fatalf("sibling offering = %d %s", sibling.Code, sibling.Body.String())
	}
}

// TestGetApplicationOfferingMissingFileNotFound verifies a missing file and an empty directory are not-found.
func TestGetApplicationOfferingMissingFileNotFound(t *testing.T) {
	t.Parallel()

	catalog := &offeringMembershipCatalog{}
	missing := getOffering(newOfferingAPI(t.TempDir(), catalog), "application-1", "", nil)
	assertOfferingNotFound(t, missing)

	unconfigured := getOffering(newOfferingAPI("", nil), "application-1", "", nil)
	assertOfferingNotFound(t, unconfigured)
	if catalog.calls != 0 {
		t.Fatalf("catalog calls = %d, want 0", catalog.calls)
	}
}

// TestGetApplicationOfferingRejectsOversizedDocument verifies a document above 64 KiB is not served.
func TestGetApplicationOfferingRejectsOversizedDocument(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	payload := make([]byte, offering.MaxDocumentBytes+1)
	for index := range payload {
		payload[index] = 'x'
	}
	if err := os.WriteFile(filepath.Join(directory, "application-1.json"), payload, 0o600); err != nil {
		t.Fatalf("write oversized offering: %v", err)
	}
	catalog := &offeringMembershipCatalog{products: map[core.ApplicationID]map[core.ProviderProductID]struct{}{
		"application-1": {"premium_monthly": {}},
	}}

	recorder := getOffering(newOfferingAPI(directory, catalog), "application-1", "", nil)
	assertOfferingNotFound(t, recorder)
	if catalog.calls != 0 {
		t.Fatalf("catalog calls = %d, want 0", catalog.calls)
	}
}

// TestGetApplicationOfferingResponseHasNoPriceOrCustomer verifies the body and headers stay presentation-only.
func TestGetApplicationOfferingResponseHasNoPriceOrCustomer(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	writeOfferingHTTPFixture(t, directory, happyOfferingDocument("application-1"))
	catalog := &offeringMembershipCatalog{products: map[core.ApplicationID]map[core.ProviderProductID]struct{}{
		"application-1": {"premium_monthly": {}},
	}}
	api := newOfferingAPI(directory, catalog)
	headers := map[string]string{
		"Authorization": "Bearer credential-sentinel",
		"X-Customer-Id": "customer-sentinel",
	}

	first := getOffering(api, "application-1", "customer_id=customer-sentinel&price=price-sentinel", headers)
	second := getOffering(api, "application-1", "customer_id=other-customer&currency=USD", headers)
	if first.Code != http.StatusOK || first.Body.String() != second.Body.String() {
		t.Fatalf("query changed offering response: %d %s vs %d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	assertOfferingAllowlist(t, first.Body.Bytes())
	assertNoOfferingSentinels(t, first)

	poisoned := t.TempDir()
	payload := []byte(`{
		"schema_version": 1,
		"application_id": "application-1",
		"revision": "revision-1",
		"price": "price-sentinel",
		"currency": "USD",
		"customer_id": "customer-sentinel",
		"credential": "credential-sentinel",
		"offerings": [{"id": "default", "packages": [{"id": "monthly", "store_product_id": "premium_monthly"}]}]
	}`)
	if err := os.WriteFile(filepath.Join(poisoned, "application-1.json"), payload, 0o600); err != nil {
		t.Fatalf("write poisoned offering: %v", err)
	}
	poisonedCalls := catalog.calls
	rejected := getOffering(newOfferingAPI(poisoned, catalog), "application-1", "customer_id=customer-sentinel", nil)
	assertOfferingNotFound(t, rejected)
	assertNoOfferingSentinels(t, rejected)
	if catalog.calls != poisonedCalls {
		t.Fatal("poisoned offering reached the catalog")
	}
}

// TestGetApplicationOfferingUnauthenticatedSuccess verifies a bearer is neither required nor echoed.
func TestGetApplicationOfferingUnauthenticatedSuccess(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	writeOfferingHTTPFixture(t, directory, happyOfferingDocument("application-1"))
	api := newOfferingAPI(directory, &offeringMembershipCatalog{products: map[core.ApplicationID]map[core.ProviderProductID]struct{}{
		"application-1": {"premium_monthly": {}},
	}})

	anonymous := getOffering(api, "application-1", "", nil)
	authenticated := getOffering(api, "application-1", "", map[string]string{
		"Authorization": "Bearer durable-application-bearer-sentinel",
	})
	if anonymous.Code != http.StatusOK || authenticated.Code != http.StatusOK {
		t.Fatalf("anonymous = %d, authenticated = %d", anonymous.Code, authenticated.Code)
	}
	if anonymous.Body.String() != authenticated.Body.String() {
		t.Fatalf("bearer changed offering body: %s vs %s", anonymous.Body.String(), authenticated.Body.String())
	}
	if strings.Contains(authenticated.Body.String(), "durable-application-bearer-sentinel") ||
		authenticated.Header().Get("Authorization") != "" {
		t.Fatalf("response echoed a bearer: headers=%v body=%s", authenticated.Header(), authenticated.Body.String())
	}
}

// newOfferingAPI registers the public v1 routes around one offering directory.
func newOfferingAPI(directory string, catalog persistence.OfferingCatalog) *API {
	api := &API{offeringDirectory: directory, offeringCatalog: catalog}
	mux := http.NewServeMux()
	for _, route := range api.v1Routes() {
		mux.HandleFunc(route.Method+" "+serveMuxPath(route.Path), route.Handler)
	}
	mux.HandleFunc("/v1/", api.notFound)
	api.handler = mux
	return api
}

// getOffering performs one offering read. rawQuery is not used to choose a customer.
func getOffering(api *API, applicationID, rawQuery string, headers map[string]string) *httptest.ResponseRecorder {
	target := "/v1/applications/" + applicationID + "/offering"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	api.ServeHTTP(recorder, request)
	return recorder
}

// happyOfferingDocument returns one valid presentation document for the named application.
func happyOfferingDocument(applicationID core.ApplicationID) offering.Document {
	return offering.Document{
		SchemaVersion: 1,
		ApplicationID: applicationID,
		Revision:      "revision-1",
		Offerings: []offering.Offering{{
			ID: "default",
			Packages: []offering.Package{{
				ID: "monthly", StoreProductID: "premium_monthly", DisplayKey: "premium_monthly",
			}},
		}},
	}
}

// writeOfferingHTTPFixture stores one document under its application file name.
func writeOfferingHTTPFixture(t *testing.T, directory string, document offering.Document) {
	t.Helper()

	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, string(document.ApplicationID)+".json"), payload, 0o600); err != nil {
		t.Fatalf("write offering: %v", err)
	}
}

// assertOfferingNotFound checks the stable not-found envelope without a public cache.
func assertOfferingNotFound(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()

	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"not_found"`) {
		t.Fatalf("response = %d %s, want not_found", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", recorder.Header().Get("Cache-Control"))
	}
	if strings.Contains(recorder.Body.String(), `"offerings"`) {
		t.Fatalf("not-found response included an offering document: %s", recorder.Body.String())
	}
}

// assertOfferingAllowlist checks the response contains only presentation fields.
func assertOfferingAllowlist(t *testing.T, payload []byte) {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	assertExactKeys(t, body, "schema_version", "application_id", "revision", "offerings")
	offerings, _ := body["offerings"].([]any)
	if len(offerings) != 1 {
		t.Fatalf("offerings = %#v", body["offerings"])
	}
	group, _ := offerings[0].(map[string]any)
	assertExactKeys(t, group, "id", "packages")
	packages, _ := group["packages"].([]any)
	item, _ := packages[0].(map[string]any)
	assertExactKeys(t, item, "id", "store_product_id", "display_key")
}

// assertExactKeys fails when a JSON object contains anything outside the allowlist.
func assertExactKeys(t *testing.T, object map[string]any, allowed ...string) {
	t.Helper()

	allowedKeys := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedKeys[key] = struct{}{}
	}
	for key := range object {
		if _, ok := allowedKeys[key]; !ok {
			t.Errorf("unexpected field %q", key)
		}
	}
	for _, key := range allowed {
		if _, ok := object[key]; !ok {
			t.Errorf("missing field %q", key)
		}
	}
}

// assertNoOfferingSentinels fails when a response echoes price, customer, or credential probes.
func assertNoOfferingSentinels(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()

	combined := recorder.Body.String()
	for key, values := range recorder.Header() {
		combined += " " + key
		for _, value := range values {
			combined += " " + value
		}
	}
	for _, sentinel := range []string{"price-sentinel", "customer-sentinel", "credential-sentinel", "other-customer"} {
		if strings.Contains(combined, sentinel) {
			t.Errorf("response echoed %s: %s", sentinel, combined)
		}
	}
}
