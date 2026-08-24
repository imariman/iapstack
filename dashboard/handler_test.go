package dashboard

import (
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
		{path: "/dashboard/app.css", status: http.StatusOK, contentType: "text/css; charset=utf-8", body: "--signal"},
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
