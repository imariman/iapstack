package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// roundTripperFunc adapts a function into an HTTP transport for deterministic failures.
type roundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTrip invokes the configured transport function.
func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

// TestNewGateClientValidatesSecretsAndNormalizesOrigins verifies environment configuration boundaries.
func TestNewGateClientValidatesSecretsAndNormalizesOrigins(t *testing.T) {
	setGateEnvironment(t)
	t.Setenv("IAPSTACK_E2E_API_BASE_URL", " https://api.example.test/ ")
	t.Setenv("IAPSTACK_E2E_WORKER_BASE_URL", " ")
	t.Setenv("IAPSTACK_E2E_FIXTURE_BASE_URL", "https://fixture.example.test")

	client, err := newGateClient()
	if err != nil {
		t.Fatalf("newGateClient() error = %v", err)
	}
	if client.apiBaseURL != "https://api.example.test/" || client.workerBaseURL != defaultWorkerBaseURL ||
		client.fixtureBaseURL != "https://fixture.example.test" {
		t.Fatalf("gate origins = (%q, %q, %q)", client.apiBaseURL, client.workerBaseURL, client.fixtureBaseURL)
	}
	redirectRequest := httptest.NewRequest(http.MethodGet, "https://redirect.example.test", nil)
	if err := client.client.CheckRedirect(redirectRequest, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect() error = %v, want %v", err, http.ErrUseLastResponse)
	}

	t.Setenv("IAPSTACK_METRICS_BEARER_TOKEN", "short")
	if _, err := newGateClient(); err == nil {
		t.Fatal("newGateClient() accepted a short metrics bearer")
	}
}

// TestGateRequestEnforcesJSONAuthenticationAndStatus verifies the shared HTTP contract.
func TestGateRequestEnforcesJSONAuthenticationAndStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer gate-key" ||
			request.Header.Get("Content-Type") != "application/json" || request.Header.Get("X-Gate") != "test" {
			http.Error(writer, "unexpected request", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != `{"phase":"bootstrap"}` {
			http.Error(writer, "unexpected body", http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	client := &gateClient{client: server.Client()}

	body, err := client.request(context.Background(), http.MethodPost, server.URL, "gate-key",
		map[string]string{"phase": "bootstrap"}, map[string]string{"X-Gate": "test"}, http.StatusCreated)
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("request() = (%s, %v)", body, err)
	}
	if _, err := client.request(context.Background(), http.MethodPost, server.URL, "gate-key",
		map[string]string{"phase": "bootstrap"}, nil, http.StatusOK); err == nil || !strings.Contains(err.Error(), "returned status") {
		t.Fatalf("request() status error = %v", err)
	}
	if _, err := client.request(context.Background(), http.MethodPost, server.URL, "", make(chan int), nil, http.StatusOK); err == nil {
		t.Fatal("request() accepted an input that JSON cannot encode")
	}
	if _, err := client.request(context.Background(), http.MethodGet, "://invalid", "", nil, nil, http.StatusOK); err == nil {
		t.Fatal("request() accepted an invalid URL")
	}
}

// TestGateRequestReportsTransportFailures verifies network failures retain request context.
func TestGateRequestReportsTransportFailures(t *testing.T) {
	t.Parallel()

	client := &gateClient{client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})}}
	if _, err := client.request(context.Background(), http.MethodGet, "https://api.example.test/healthz", "", nil, nil, http.StatusOK); err == nil ||
		!strings.Contains(err.Error(), "network unavailable") {
		t.Fatalf("request() transport error = %v", err)
	}
}

// TestScenarioValidatesFixturePayload verifies complete, malformed, and incomplete fixture responses.
func TestScenarioValidatesFixturePayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "complete",
			body: `{"provider_application_id":"provider-app","provider_product_id":"provider-product","external_customer_id":"external-customer","credential":{},"evidence":{},"notification":{}}`,
		},
		{name: "malformed", body: `{`, wantErr: "decode fixture scenario"},
		{name: "incomplete", body: `{"provider_application_id":"provider-app"}`, wantErr: "fixture scenario is incomplete"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/scenario" {
					http.NotFound(writer, request)
					return
				}
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			client := &gateClient{fixtureBaseURL: server.URL, client: server.Client()}

			result, err := client.scenario(context.Background())
			if test.wantErr == "" {
				if err != nil || result.ProviderApplicationID != "provider-app" {
					t.Fatalf("scenario() = (%#v, %v)", result, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("scenario() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

// TestGateIdentityResponsesValidateRequiredFields verifies key and customer-session parsing.
func TestGateIdentityResponsesValidateRequiredFields(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/v1/admin/api-keys" && request.Header.Get("Authorization") == "Bearer bootstrap-key":
			writer.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(writer, `{"key":"application-key"}`)
		case request.URL.Path == "/v1/admin/api-keys":
			writer.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(writer, `{"key":""}`)
		case strings.HasSuffix(request.URL.Path, "/customer-sessions"):
			body, _ := io.ReadAll(request.Body)
			writer.WriteHeader(http.StatusCreated)
			if strings.Contains(string(body), "external-expired") {
				_, _ = io.WriteString(writer, `{"token":"customer-token","expires_at":"2020-01-01T00:00:00Z"}`)
				return
			}
			_, _ = io.WriteString(writer, `{"token":"customer-token","expires_at":"`+time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)+`"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := &gateClient{apiBaseURL: server.URL, client: server.Client(), bootstrapAdmin: "bootstrap-key"}

	key, err := client.createApplicationKey(context.Background())
	if err != nil || key != "application-key" {
		t.Fatalf("createApplicationKey() = (%q, %v)", key, err)
	}
	if _, err := client.createKey(context.Background(), "admin", map[string]any{}); err == nil {
		t.Fatal("createKey() accepted an empty key")
	}
	token, err := client.createCustomerSession(context.Background(), "application-key", "external-customer")
	if err != nil || token != "customer-token" {
		t.Fatalf("createCustomerSession() = (%q, %v)", token, err)
	}
	if _, err := client.createCustomerSession(context.Background(), "application-key", "external-expired"); err == nil {
		t.Fatal("createCustomerSession() accepted an expired token")
	}
}

// TestGateAssertionsValidateExactBusinessState verifies verification, entitlement, and delivery contracts.
func TestGateAssertionsValidateExactBusinessState(t *testing.T) {
	t.Parallel()

	verification := `{"customer_id":"customer-e2e","entitlements":[{"key":"premium","access":"allowed","reason":"purchase_valid","version":1}]}`
	if err := assertVerification([]byte(verification), "allowed", "purchase_valid", 1); err != nil {
		t.Fatalf("assertVerification() error = %v", err)
	}
	for _, invalid := range []string{
		`{`,
		`{"customer_id":"wrong","entitlements":[]}`,
		`{"customer_id":"customer-e2e","entitlements":[{"key":"premium","access":"denied","reason":"refunded","version":2}]}`,
	} {
		if err := assertVerification([]byte(invalid), "allowed", "purchase_valid", 1); err == nil {
			t.Fatalf("assertVerification() accepted %s", invalid)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/applications/application-e2e/customers/external-customer/entitlements":
			_, _ = io.WriteString(writer, verification)
		case "/deliveries":
			_, _ = io.WriteString(writer, `{"attempts":2,"unique_event_ids":1,"all_signatures_valid":true}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := &gateClient{apiBaseURL: server.URL, fixtureBaseURL: server.URL, client: server.Client()}
	if err := client.assertEntitlement(context.Background(), "customer-token", "external-customer", "allowed", "purchase_valid", 1); err != nil {
		t.Fatalf("assertEntitlement() error = %v", err)
	}
	if err := client.assertDeliveries(context.Background(), 2, 1); err != nil {
		t.Fatalf("assertDeliveries() error = %v", err)
	}
	if err := client.assertDeliveries(context.Background(), 1, 1); err == nil {
		t.Fatal("assertDeliveries() accepted an unexpected attempt count")
	}
}

// TestWaitForHandlesSuccessFailureAndCancellation verifies bounded polling outcomes.
func TestWaitForHandlesSuccessFailureAndCancellation(t *testing.T) {
	t.Parallel()

	if err := waitFor(context.Background(), func() (bool, error) { return true, nil }); err != nil {
		t.Fatalf("waitFor() immediate success error = %v", err)
	}
	expected := errors.New("assertion failed")
	if err := waitFor(context.Background(), func() (bool, error) { return false, expected }); !errors.Is(err, expected) {
		t.Fatalf("waitFor() assertion error = %v, want %v", err, expected)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitFor(ctx, func() (bool, error) { return false, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitFor() cancellation error = %v, want %v", err, context.Canceled)
	}
}

// TestValueOrDefaultTrimsOverrides verifies stable default selection.
func TestValueOrDefaultTrimsOverrides(t *testing.T) {
	t.Parallel()

	if value := valueOrDefault("  custom  ", "fallback"); value != "custom" {
		t.Fatalf("valueOrDefault() = %q, want custom", value)
	}
	if value := valueOrDefault("  ", "fallback"); value != "fallback" {
		t.Fatalf("valueOrDefault() = %q, want fallback", value)
	}
}

// setGateEnvironment installs valid non-production release-gate secrets.
func setGateEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("IAPSTACK_BOOTSTRAP_ADMIN_KEY", " bootstrap-admin ")
	t.Setenv("IAPSTACK_METRICS_BEARER_TOKEN", strings.Repeat("m", 32))
	t.Setenv("IAPSTACK_E2E_WEBHOOK_SECRET", strings.Repeat("w", 16))
}
