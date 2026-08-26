package dashboard

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandlerServesSecureEmbeddedAssets verifies routing, media types, and browser policy headers.
func TestHandlerServesSecureEmbeddedAssets(t *testing.T) {
	t.Parallel()

	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	})
	handler, err := New(fallback)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		path        string
		status      int
		contentType string
		body        string
	}{
		{path: "/dashboard/", status: http.StatusOK, contentType: "text/html; charset=utf-8", body: "IAPStack Operations"},
		{path: "/dashboard/app.css", status: http.StatusOK, contentType: "text/css; charset=utf-8", body: "--accent"},
		{path: "/dashboard/app.js", status: http.StatusOK, contentType: "text/javascript; charset=utf-8", body: "sessionStorage"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if recorder.Code != test.status {
			t.Fatalf("GET %s status = %d, want %d", test.path, recorder.Code, test.status)
		}
		if contentType := recorder.Header().Get("Content-Type"); contentType != test.contentType {
			t.Fatalf("GET %s Content-Type = %q, want %q", test.path, contentType, test.contentType)
		}
		if !strings.Contains(recorder.Body.String(), test.body) {
			t.Fatalf("GET %s body does not contain %q", test.path, test.body)
		}
		if policy := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(policy, "frame-ancestors 'none'") {
			t.Fatalf("GET %s Content-Security-Policy = %q", test.path, policy)
		}
	}
}

// TestDashboardSecretLifecycleAvoidsPersistentStorage verifies browser-managed secrets remain ephemeral.
func TestDashboardSecretLifecycleAvoidsPersistentStorage(t *testing.T) {
	t.Parallel()

	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	if strings.Contains(string(script), "localStorage") {
		t.Fatal("dashboard script must not persist bearer or secret values in localStorage")
	}
	for _, required := range []string{
		"sessionStorage", "credentialForm.reset()", "webhookForm.reset()", `revealedKey.value = ""`,
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard script does not contain secret lifecycle control %q", required)
		}
	}
	if count := strings.Count(string(document), `autocomplete="new-password"`); count != 3 {
		t.Fatalf("dashboard secret fields with new-password autocomplete = %d, want 3", count)
	}
}

// TestDashboardSupportsAppleCatalogCommissioning keeps App Store setup available without weakening secret handling.
func TestDashboardSupportsAppleCatalogCommissioning(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, required := range []string{
		`value="apple_app_store"`, `name="private_key"`, `name="root_certificates"`,
	} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard Apple form does not contain %q", required)
		}
	}
	for _, required := range []string{
		"apple_app_store_server_api", "appleCredentialContentType", "saveAppleCredential",
	} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard Apple workflow does not contain %q", required)
		}
	}
}

// TestDashboardUsesEnglishInterface verifies the embedded interface stays consistently English.
func TestDashboardUsesEnglishInterface(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	if !strings.Contains(string(document), `<html lang="en">`) {
		t.Fatal("dashboard document must declare English as its interface language")
	}
	for _, value := range []string{string(document), string(script)} {
		if strings.ContainsAny(value, "\u00e7\u011f\u0131\u00f6\u015f\u00fc\u00c7\u011e\u0130\u00d6\u015e\u00dc") {
			t.Fatal("dashboard interface contains Turkish characters")
		}
		for _, forbidden := range []string{"Yeni", "Hata", "Tamam", "Eksik", "Bekleyen", "Durum", "Katalog"} {
			if strings.Contains(value, forbidden) {
				t.Fatalf("dashboard interface contains Turkish term %q", forbidden)
			}
		}
	}
}

// TestDashboardProfessionalUIControls verifies the dashboard keeps its dark, accessible interaction baseline.
func TestDashboardProfessionalUIControls(t *testing.T) {
	t.Parallel()

	document, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	styles, err := fs.ReadFile(assets, "app.css")
	if err != nil {
		t.Fatalf("read embedded app.css: %v", err)
	}
	script, err := fs.ReadFile(assets, "app.js")
	if err != nil {
		t.Fatalf("read embedded app.js: %v", err)
	}
	for _, required := range []string{`content="dark"`, `<svg aria-hidden="true"`, `aria-label="Close dialog"`} {
		if !strings.Contains(string(document), required) {
			t.Fatalf("dashboard document does not contain UI control %q", required)
		}
	}
	for _, required := range []string{"min-height: 44px", "prefers-reduced-motion", "outline: 3px solid var(--focus)"} {
		if !strings.Contains(string(styles), required) {
			t.Fatalf("dashboard styles do not contain accessibility rule %q", required)
		}
	}
	for _, required := range []string{`setAttribute("aria-busy", "true")`, "setFormBusy"} {
		if !strings.Contains(string(script), required) {
			t.Fatalf("dashboard script does not contain loading feedback %q", required)
		}
	}
}

// TestHandlerRedirectsAndDelegates verifies the canonical entrypoint and API fallthrough.
func TestHandlerRedirectsAndDelegates(t *testing.T) {
	t.Parallel()

	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	})
	handler, err := New(fallback)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	redirect := httptest.NewRecorder()
	handler.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if redirect.Code != http.StatusPermanentRedirect || redirect.Header().Get("Location") != "/dashboard/" {
		t.Fatalf("dashboard redirect = (%d, %q), want 308 and canonical path", redirect.Code, redirect.Header().Get("Location"))
	}

	delegated := httptest.NewRecorder()
	handler.ServeHTTP(delegated, httptest.NewRequest(http.MethodGet, "/v1/admin/projects", nil))
	if delegated.Code != http.StatusTeapot {
		t.Fatalf("delegated status = %d, want %d", delegated.Code, http.StatusTeapot)
	}
}
