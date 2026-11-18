package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

// recordingCustomerStore records the search a handler passes to the admin read model.
type recordingCustomerStore struct {
	fakeAdminQueryStore
	search persistence.AdminCustomerSearch
	calls  int
}

// AdminSearchCustomers records one search and returns an empty page.
func (store *recordingCustomerStore) AdminSearchCustomers(
	_ context.Context,
	search persistence.AdminCustomerSearch,
) (persistence.AdminCustomerSearchPage, error) {
	store.search = search
	store.calls++
	return persistence.AdminCustomerSearchPage{Results: []persistence.AdminCustomerSearchResult{}}, nil
}

// TestSearchAdminCustomersValidatesUntrustedInput verifies trimming, bounds, and cursor binding before the store runs.
func TestSearchAdminCustomersValidatesUntrustedInput(t *testing.T) {
	t.Parallel()

	authentication, err := auth.NewService(authOperationsStore{}, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	store := &recordingCustomerStore{}
	api := &API{admin: store, authentication: authentication, protection: newIdentityTestKeyring(t)}
	search := func(rawQuery string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/v1/admin/projects/project-1/customers?"+rawQuery, nil)
		request.SetPathValue("project_id", "project-1")
		request.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
		recorder := httptest.NewRecorder()
		api.searchAdminCustomers(recorder, request)
		return recorder
	}

	unauthorized := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodGet, "/v1/admin/projects/project-1/customers", nil)
	unauthorizedRequest.SetPathValue("project_id", "project-1")
	api.searchAdminCustomers(unauthorized, unauthorizedRequest)
	if unauthorized.Code != http.StatusUnauthorized || store.calls != 0 {
		t.Fatalf("unauthenticated search = %d with %d store calls", unauthorized.Code, store.calls)
	}

	if recorder := search("query=" + url.QueryEscape("  user-1  ") + "&limit=10"); recorder.Code != http.StatusOK {
		t.Fatalf("search status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if store.search.Query != "user-1" || store.search.Limit != 10 || store.search.After != nil || store.search.References == nil {
		t.Fatalf("recorded search = %#v, want trimmed text, limit, and a fingerprinter", store.search)
	}
	if recorder := search(""); recorder.Code != http.StatusOK || store.search.Query != "" || store.search.References != nil {
		t.Fatalf("listing = %d, search %#v, want no fingerprinter", recorder.Code, store.search)
	}

	calls := store.calls
	token := encodeSearchPageToken(persistence.AdminCustomerSearchCursor{
		Rank: 1, CreatedAt: time.Date(2026, time.November, 18, 0, 0, 0, 0, time.UTC), CustomerID: "customer-9",
	}, "user-1")
	for name, rawQuery := range map[string]string{
		"control character":  "query=" + url.QueryEscape("user\x00-1"),
		"too long":           "query=" + strings.Repeat("a", persistence.AdminCustomerSearchMaxQueryBytes+1),
		"limit zero":         "limit=0",
		"limit too large":    "limit=101",
		"limit not a number": "limit=ten",
		"foreign token":      "query=other&page_token=" + url.QueryEscape(token),
		"garbage token":      "query=user-1&page_token=not-base64!",
	} {
		recorder := search(rawQuery)
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_request"`) {
			t.Fatalf("%s = %d %s, want invalid_request", name, recorder.Code, recorder.Body.String())
		}
	}
	if store.calls != calls {
		t.Fatalf("invalid searches reached the store %d times", store.calls-calls)
	}

	if recorder := search("query=user-1&page_token=" + url.QueryEscape(token)); recorder.Code != http.StatusOK {
		t.Fatalf("bound token search = %d %s", recorder.Code, recorder.Body.String())
	}
	if store.search.After == nil || store.search.After.CustomerID != "customer-9" || store.search.After.Rank != 1 {
		t.Fatalf("decoded cursor = %#v", store.search.After)
	}
}

// TestSearchReferenceFingerprintsMatchVerificationScopes verifies search derives the fingerprints verification stores.
func TestSearchReferenceFingerprintsMatchVerificationScopes(t *testing.T) {
	t.Parallel()

	keyring := newIdentityTestKeyring(t)
	fingerprints, err := searchReferenceFingerprinter{protector: keyring, text: "2000000123456789"}.ReferenceFingerprints(
		context.Background(), "project-1", []core.ApplicationID{"application-1", "application-2"},
	)
	if err != nil {
		t.Fatalf("ReferenceFingerprints() error = %v", err)
	}
	if len(fingerprints) != 2*len(searchableReferences) {
		t.Fatalf("fingerprints = %d, want one per application and searchable reference", len(fingerprints))
	}
	for _, fingerprint := range fingerprints {
		stored, err := protection.Protect(context.Background(), keyring, protection.Scope{
			ProjectID: "project-1", ApplicationID: fingerprint.ApplicationID,
			Purpose: "provider_reference:" + string(fingerprint.Role) + ":" + fingerprint.Kind,
		}, []byte("2000000123456789"))
		if err != nil {
			t.Fatalf("Protect() error = %v", err)
		}
		if stored.Fingerprint != fingerprint.Fingerprint {
			t.Fatalf("fingerprint for %s %s/%s does not match the verification scope", fingerprint.ApplicationID, fingerprint.Role, fingerprint.Kind)
		}
		if fingerprint.Role != core.ReferenceTransaction && fingerprint.Role != core.ReferenceLineage {
			t.Fatalf("search fingerprinted role %q", fingerprint.Role)
		}
		if fingerprint.Kind == "purchase_token" {
			t.Fatal("search must not fingerprint purchase tokens")
		}
	}
}

// TestManualGrantHTTPRequiresStoredAdminAndValidPeriod verifies actor, reason, and period checks before any write.
func TestManualGrantHTTPRequiresStoredAdminAndValidPeriod(t *testing.T) {
	t.Parallel()

	keys := &keyLifecycleStore{
		records: make(map[string]persistence.APIKeyRecord), revokedAt: make(map[string]time.Time),
	}
	authentication, err := auth.NewService(keys, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adminKey, err := authentication.Create(context.Background(), auth.Principal{Role: persistence.APIKeyRoleAdmin})
	if err != nil {
		t.Fatalf("Create() admin key error = %v", err)
	}
	applicationKey, err := authentication.Create(context.Background(), auth.Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-1", ApplicationID: "application-1",
	})
	if err != nil {
		t.Fatalf("Create() application key error = %v", err)
	}
	api := &API{
		store: &identityAPIStore{}, authentication: authentication, bodyLimit: 1 << 20,
		clock: func() time.Time { return time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC) },
	}
	grant := func(bearer, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := identityRequest(t, http.MethodPost, "/v1/admin/projects/project-1/customers/customer-1/manual-grants", bearer, body)
		request.SetPathValue("project_id", "project-1")
		request.SetPathValue("customer_id", "customer-1")
		recorder := httptest.NewRecorder()
		api.createAdminManualGrant(recorder, request)
		return recorder
	}
	valid := `{"idempotency_key":"grant-1","entitlement_id":"entitlement-1","product_id":"product-1",` +
		`"application_id":"application-1","reason":"Support ticket 42","duration_days":30}`

	if recorder := grant(testBootstrapAdminKey, valid); recorder.Code != http.StatusForbidden ||
		!strings.Contains(recorder.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("bootstrap grant = %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := grant(applicationKey, valid); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("application grant = %d %s", recorder.Code, recorder.Body.String())
	}
	for name, body := range map[string]string{
		"both periods": strings.Replace(valid, `"duration_days":30`, `"duration_days":30,"ends_at":"2026-12-01T00:00:00Z"`, 1),
		"no period":    strings.Replace(valid, `,"duration_days":30`, ``, 1),
		"zero days":    strings.Replace(valid, `"duration_days":30`, `"duration_days":0`, 1),
		"past end":     strings.Replace(valid, `"duration_days":30`, `"ends_at":"2026-11-01T00:00:00Z"`, 1),
		"far end":      strings.Replace(valid, `"duration_days":30`, `"ends_at":"2040-01-01T00:00:00Z"`, 1),
		"no reason":    strings.Replace(valid, `"reason":"Support ticket 42"`, `"reason":""`, 1),
		"multi-line":   strings.Replace(valid, `"reason":"Support ticket 42"`, `"reason":"line one\nline two"`, 1),
		"long reason":  strings.Replace(valid, `"reason":"Support ticket 42"`, `"reason":"`+strings.Repeat("x", 501)+`"`, 1),
		"no key":       strings.Replace(valid, `"idempotency_key":"grant-1",`, ``, 1),
	} {
		recorder := grant(adminKey, body)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s grant = %d %s, want 400", name, recorder.Code, recorder.Body.String())
		}
	}
	if recorder := grant(adminKey, strings.Replace(valid, `"reason"`, `"source_observation_id":"observation-1","reason"`, 1)); recorder.Code != http.StatusBadRequest ||
		!strings.Contains(recorder.Body.String(), `"code":"invalid_json"`) {
		t.Fatalf("grant with an observation id = %d %s, want invalid_json", recorder.Code, recorder.Body.String())
	}

	revoke := func(grantID string) *httptest.ResponseRecorder {
		t.Helper()
		request := identityRequest(t, http.MethodPost,
			"/v1/admin/projects/project-1/customers/customer-1/manual-grants/"+grantID, adminKey, `{"reason":"Wrong account"}`)
		request.SetPathValue("project_id", "project-1")
		request.SetPathValue("customer_id", "customer-1")
		request.SetPathValue("grant_id", grantID)
		recorder := httptest.NewRecorder()
		api.revokeAdminManualGrant(recorder, request)
		return recorder
	}
	for _, grantID := range []string{"mgr-0123456789abcdef0123456789abcdef", ":revoke", "a:b:revoke"} {
		if recorder := revoke(grantID); recorder.Code != http.StatusNotFound {
			t.Fatalf("revoke %q = %d %s, want 404", grantID, recorder.Code, recorder.Body.String())
		}
	}
	if recorder := revoke("not-a-grant:revoke"); recorder.Code != http.StatusBadRequest {
		t.Fatalf("revoke malformed id = %d %s, want 400", recorder.Code, recorder.Body.String())
	}
}

// TestAdminCustomerDetailEvaluatesAccessAtNow verifies the detail fails closed after an end and labels grant status.
func TestAdminCustomerDetailEvaluatesAccessAtNow(t *testing.T) {
	t.Parallel()

	detail := fakeAdminCustomerDetail("project-1")
	afterEnd := detail.ManualGrants[0].EndsAt.Add(time.Minute)
	response := adminCustomerDetailFromRecord(detail, afterEnd)
	if response.Entitlements[0].Access != core.AccessDenied || response.Entitlements[0].Reason != core.AccessReasonExpired ||
		response.Entitlements[0].Source != persistence.SourceKindManual {
		t.Fatalf("entitlement after end = %#v, want expired manual source", response.Entitlements[0])
	}
	if response.ManualGrants[0].Status != manualGrantStatusExpired {
		t.Fatalf("grant status after end = %q", response.ManualGrants[0].Status)
	}
	active := adminCustomerDetailFromRecord(detail, detail.ManualGrants[0].StartsAt.Add(time.Hour))
	if active.ManualGrants[0].Status != manualGrantStatusActive || active.Entitlements[0].Access != core.AccessAllowed {
		t.Fatalf("active detail = %#v", active)
	}
	revokedAt := detail.ManualGrants[0].StartsAt.Add(time.Minute)
	detail.ManualGrants[0].RevokedAt = &revokedAt
	detail.ManualGrants[0].RevokedByKeyID = "0123456789abcdef01234567"
	detail.ManualGrants[0].RevocationReason = "Wrong account"
	if revoked := adminCustomerDetailFromRecord(detail, revokedAt.Add(time.Hour)); revoked.ManualGrants[0].Status != manualGrantStatusRevoked {
		t.Fatalf("revoked grant status = %q", revoked.ManualGrants[0].Status)
	}

	authentication, err := auth.NewService(authOperationsStore{}, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	api := &API{admin: fakeAdminQueryStore{}, authentication: authentication}
	for customerID, status := range map[string]int{"customer-1": http.StatusOK, "customer-404": http.StatusNotFound} {
		request := httptest.NewRequest(http.MethodGet, "/v1/admin/projects/project-1/customers/"+customerID, nil)
		request.SetPathValue("project_id", "project-1")
		request.SetPathValue("customer_id", customerID)
		request.Header.Set("Authorization", "Bearer "+testBootstrapAdminKey)
		recorder := httptest.NewRecorder()
		api.getAdminCustomer(recorder, request)
		if recorder.Code != status {
			t.Fatalf("detail %s = %d %s, want %d", customerID, recorder.Code, recorder.Body.String(), status)
		}
		if status == http.StatusOK {
			var decoded adminCustomerDetailResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("decode detail: %v", err)
			}
			for _, forbidden := range []string{"ciphertext", "fingerprint", "purchase_token", "idempotency_key"} {
				if bytes.Contains(recorder.Body.Bytes(), []byte(forbidden)) {
					t.Fatalf("detail response contains %q: %s", forbidden, recorder.Body.String())
				}
			}
		}
	}
}
