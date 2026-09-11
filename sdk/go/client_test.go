package iapstack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	// testApplicationToken is a non-production application bearer fixture.
	testApplicationToken = "application-token"
	// testCustomerToken is a non-production customer bearer fixture.
	testCustomerToken = "customer-token"
	// testSecretBearer is an invalid bearer used only to prove redaction.
	testSecretBearer = "secret with spaces"
)

type capturedRequest struct {
	Method  string
	Path    string
	URI     string
	Headers http.Header
	Body    []byte
}

// TestCreateCustomerSessionSendsApplicationBearer verifies the host minting contract.
func TestCreateCustomerSessionSendsApplicationBearer(t *testing.T) {
	t.Parallel()

	var captured capturedRequest
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured = captureRequest(t, request)
		writeJSON(writer, http.StatusCreated, map[string]any{
			"token":      "iaps_customer",
			"expires_at": "2026-08-24T20:15:00Z",
		})
	}))

	session, err := client.CreateCustomerSession(
		WithRequestID(context.Background(), "request-client-1"),
		"customer-external",
	)
	if err != nil {
		t.Fatalf("CreateCustomerSession() error = %v", err)
	}
	if session.Token != "iaps_customer" || !session.ExpiresAt.Equal(time.Date(2026, 8, 24, 20, 15, 0, 0, time.UTC)) {
		t.Fatalf("session = %#v", session)
	}
	if captured.Method != http.MethodPost || captured.Path != "/proxy/v1/applications/application-1/customer-sessions" {
		t.Fatalf("request = %s %s", captured.Method, captured.Path)
	}
	if got := captured.Headers.Get("Authorization"); got != "Bearer "+testApplicationToken {
		t.Fatalf("Authorization = %q", got)
	}
	if captured.Headers.Get("X-Request-ID") != "request-client-1" {
		t.Fatalf("X-Request-ID = %q", captured.Headers.Get("X-Request-ID"))
	}
	if !strings.HasPrefix(captured.Headers.Get("X-IAPStack-SDK"), "go-host/") {
		t.Fatalf("X-IAPStack-SDK = %q", captured.Headers.Get("X-IAPStack-SDK"))
	}
	var body map[string]any
	if err := json.Unmarshal(captured.Body, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["external_customer_id"] != "customer-external" {
		t.Fatalf("body = %#v", body)
	}
}

// TestGetEntitlementsUsesCustomerSessionBearer verifies the documented host lookup path.
func TestGetEntitlementsUsesCustomerSessionBearer(t *testing.T) {
	t.Parallel()

	var captured capturedRequest
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured = captureRequest(t, request)
		writeJSON(writer, http.StatusOK, entitlementSnapshotJSON())
	}))

	snapshot, err := client.GetEntitlements(context.Background(), "customer-external", testCustomerToken)
	if err != nil {
		t.Fatalf("GetEntitlements() error = %v", err)
	}
	if snapshot.CustomerID != "customer-internal" || len(snapshot.Entitlements) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if !snapshot.Entitlements[0].GrantsAccessAt(time.Date(2026, 8, 24, 20, 0, 0, 0, time.UTC)) {
		t.Fatal("expected current access")
	}
	if captured.Method != http.MethodGet {
		t.Fatalf("method = %s", captured.Method)
	}
	if captured.Path != "/proxy/v1/applications/application-1/customers/customer-external/entitlements" {
		t.Fatalf("path = %s", captured.Path)
	}
	if got := captured.Headers.Get("Authorization"); got != "Bearer "+testCustomerToken {
		t.Fatalf("Authorization = %q, want customer session bearer", got)
	}
	if strings.Contains(string(captured.Body), testApplicationToken) {
		t.Fatal("application bearer leaked onto the customer-session lookup")
	}
}

// TestGetEntitlementsEscapesCustomerIdentifiersAsOnePathSegment verifies path encoding.
func TestGetEntitlementsEscapesCustomerIdentifiersAsOnePathSegment(t *testing.T) {
	t.Parallel()

	var captured capturedRequest
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured = captureRequest(t, request)
		writeJSON(writer, http.StatusOK, entitlementSnapshotJSON())
	}))

	if _, err := client.GetEntitlements(context.Background(), "customer/with space", testCustomerToken); err != nil {
		t.Fatalf("GetEntitlements() error = %v", err)
	}
	if !strings.Contains(captured.URI, "customer%2Fwith%20space") {
		t.Fatalf("URI = %s, want encoded customer identity", captured.URI)
	}
}

// TestEntitlementFailsClosedAtEffectiveEnd verifies exclusive period ends.
func TestEntitlementFailsClosedAtEffectiveEnd(t *testing.T) {
	t.Parallel()

	endsAt := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	startsAt := endsAt.Add(-30 * 24 * time.Hour)
	entitlement := Entitlement{
		Key:               "premium",
		Access:            "allowed",
		Reason:            "canceled_at_period_end",
		Version:           7,
		EffectiveStartsAt: &startsAt,
		EffectiveEndsAt:   &endsAt,
	}
	if !entitlement.GrantsAccessAt(endsAt.Add(-time.Microsecond)) {
		t.Fatal("access should remain granted before the exclusive end")
	}
	if entitlement.GrantsAccessAt(endsAt) {
		t.Fatal("access should be denied at the exclusive end")
	}
	if entitlement.GrantsAccessAt(endsAt.Add(time.Hour)) {
		t.Fatal("access should be denied after the exclusive end")
	}
}

// TestClientRetriesTransientResponses verifies bounded retry of 503 envelopes.
func TestClientRetriesTransientResponses(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if attempts.Add(1) == 1 {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]any{
					"code":       "provider_unavailable",
					"message":    "provider is unavailable",
					"request_id": "request-server-1",
				},
			})
			return
		}
		writeJSON(writer, http.StatusCreated, map[string]any{
			"token":      "iaps_customer",
			"expires_at": "2026-08-24T20:15:00Z",
		})
	}), func(config *Config) {
		config.RetryPolicy = RetryPolicy{MaxAttempts: 2, BaseDelay: 0, MaxDelay: 0}
	})

	session, err := client.CreateCustomerSession(context.Background(), "customer-external")
	if err != nil {
		t.Fatalf("CreateCustomerSession() error = %v", err)
	}
	if attempts.Load() != 2 || session.Token != "iaps_customer" {
		t.Fatalf("attempts=%d session=%#v", attempts.Load(), session)
	}
}

// TestClientWaitsUsingRetryPolicy verifies full-jitter delay is applied between attempts.
func TestClientWaitsUsingRetryPolicy(t *testing.T) {
	t.Parallel()

	var waited time.Duration
	var attempts atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if attempts.Add(1) == 1 {
			writeJSON(writer, http.StatusTooManyRequests, map[string]any{
				"error": map[string]any{"code": "rate_limited", "message": "slow down"},
			})
			return
		}
		writeJSON(writer, http.StatusCreated, map[string]any{
			"token":      "iaps_customer",
			"expires_at": "2026-08-24T20:15:00Z",
		})
	}), func(config *Config) {
		config.RetryPolicy = RetryPolicy{MaxAttempts: 2, BaseDelay: 40 * time.Millisecond, MaxDelay: 40 * time.Millisecond}
	})
	client.jitter = func() float64 { return 1 }
	client.delay = func(_ context.Context, delay time.Duration) error {
		waited = delay
		return nil
	}

	if _, err := client.CreateCustomerSession(context.Background(), "customer-external"); err != nil {
		t.Fatalf("CreateCustomerSession() error = %v", err)
	}
	if waited != 40*time.Millisecond {
		t.Fatalf("waited = %s, want 40ms", waited)
	}
}

// TestClientExposesStableAPIErrorsWithoutPayloads verifies envelope mapping and redaction.
func TestClientExposesStableAPIErrorsWithoutPayloads(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{
				"code":       "unauthorized",
				"message":    "authentication required",
				"request_id": "request-server-2",
			},
			"secret": "must-not-escape",
		})
	}))

	_, err := client.CreateCustomerSession(context.Background(), "customer-external")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized || apiErr.Code != "unauthorized" || apiErr.RequestID != "request-server-2" || apiErr.Retryable {
		t.Fatalf("APIError = %#v", apiErr)
	}
	if strings.Contains(apiErr.Error(), "must-not-escape") {
		t.Fatalf("error leaked payload: %s", apiErr.Error())
	}
}

// TestClientRejectsOversizedAndInvalidJSON verifies fail-closed protocol handling.
func TestClientRejectsOversizedAndInvalidJSON(t *testing.T) {
	t.Parallel()

	oversized := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusCreated, map[string]any{"token": "too-large-token", "expires_at": "2026-08-24T20:15:00Z"})
	}), func(config *Config) {
		config.MaxResponseBytes = 8
	})
	_, err := oversized.CreateCustomerSession(context.Background(), "customer-external")
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("oversized error = %v, want ProtocolError", err)
	}

	invalid := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("<html>proxy error</html>"))
	}))
	_, err = invalid.CreateCustomerSession(context.Background(), "customer-external")
	if !errors.As(err, &protocolErr) {
		t.Fatalf("invalid JSON error = %v, want ProtocolError", err)
	}
}

// TestClientTurnsAttemptDeadlineIntoTimeout verifies abortable per-attempt timeouts.
func TestClientTurnsAttemptDeadlineIntoTimeout(t *testing.T) {

	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		time.Sleep(40 * time.Millisecond)
		writeJSON(writer, http.StatusCreated, map[string]any{
			"token":      "iaps_customer",
			"expires_at": "2026-08-24T20:15:00Z",
		})
	}), func(config *Config) {
		config.Timeout = 5 * time.Millisecond
	})

	_, err := client.CreateCustomerSession(context.Background(), "customer-external")
	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("error = %v, want TimeoutError", err)
	}
}

// TestClientRetriesAndRedactsTransportErrors verifies cause redaction.
func TestClientRetriesAndRedactsTransportErrors(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		hijack, ok := writer.(http.Hijacker)
		if !ok {
			t.Fatal("writer cannot hijack")
		}
		conn, _, err := hijack.Hijack()
		if err != nil {
			t.Fatalf("Hijack() error = %v", err)
		}
		_ = conn.Close()
	}), func(config *Config) {
		config.RetryPolicy = RetryPolicy{MaxAttempts: 2, BaseDelay: 0, MaxDelay: 0}
	})

	_, err := client.CreateCustomerSession(context.Background(), "customer-external")
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("error = %v, want TransportError", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
	if strings.Contains(transportErr.Error(), "private") {
		t.Fatalf("transport error leaked diagnostics: %s", transportErr.Error())
	}
}

// TestConfigRequiresHTTPSUnlessExplicit verifies the default TLS boundary.
func TestConfigRequiresHTTPSUnlessExplicit(t *testing.T) {
	t.Parallel()

	_, err := NewClient(Config{
		BaseURL:          "http://iap.example",
		ApplicationID:    "application-1",
		ApplicationToken: testApplicationToken,
	})
	if err == nil {
		t.Fatal("NewClient() accepted plain HTTP")
	}
}

// TestConfigRedactsInvalidBearerValues verifies secrets stay out of validation errors.
func TestConfigRedactsInvalidBearerValues(t *testing.T) {
	t.Parallel()

	_, err := NewClient(Config{
		BaseURL:          "https://iap.example",
		ApplicationID:    "application-1",
		ApplicationToken: testSecretBearer,
	})
	if err == nil {
		t.Fatal("NewClient() accepted a whitespace bearer")
	}
	if strings.Contains(err.Error(), testSecretBearer) {
		t.Fatalf("validation error leaked bearer: %s", err.Error())
	}
}

// TestGetEntitlementsRejectsEmptyIdentities verifies host lookup input bounds.
func TestGetEntitlementsRejectsEmptyIdentities(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Fatal("HTTP should not run for invalid inputs")
	}))
	if _, err := client.GetEntitlements(context.Background(), "", testCustomerToken); err == nil {
		t.Fatal("GetEntitlements() accepted an empty customer ID")
	}
	_, err := client.GetEntitlements(context.Background(), "customer-external", testSecretBearer)
	if err == nil {
		t.Fatal("GetEntitlements() accepted a whitespace customer token")
	}
	if strings.Contains(err.Error(), testSecretBearer) {
		t.Fatalf("lookup error leaked bearer: %s", err.Error())
	}
}

// TestCreateCustomerSessionRejectsEmptyCustomerID verifies minting input bounds.
func TestCreateCustomerSessionRejectsEmptyCustomerID(t *testing.T) {
	t.Parallel()

	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Fatal("HTTP should not run for invalid inputs")
	}))
	if _, err := client.CreateCustomerSession(context.Background(), " "); err == nil {
		t.Fatal("CreateCustomerSession() accepted a whitespace customer ID")
	}
}

// TestNewClientAppliesOperationalDefaults verifies timeout and retry defaults.
func TestNewClientAppliesOperationalDefaults(t *testing.T) {
	t.Parallel()

	client, err := NewClient(Config{
		BaseURL:          "https://iap.example",
		ApplicationID:    "application-1",
		ApplicationToken: testApplicationToken,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	if client.timeout != defaultTimeout || client.maxResponseBytes != defaultMaxResponseBytes {
		t.Fatalf("defaults = timeout %s bytes %d", client.timeout, client.maxResponseBytes)
	}
	if client.retryPolicy != DefaultRetryPolicy() {
		t.Fatalf("retry policy = %#v", client.retryPolicy)
	}
}

// TestDeniedEntitlementDoesNotGrantAccess verifies non-allowed projections fail closed.
func TestDeniedEntitlementDoesNotGrantAccess(t *testing.T) {
	t.Parallel()

	entitlement := Entitlement{Key: "premium", Access: "denied", Reason: "refunded", Version: 2}
	if entitlement.GrantsAccess() {
		t.Fatal("denied entitlement granted access")
	}
}

// TestWaitForRetryHonorsTimerAndCancellation covers the default delay implementation.
func TestWaitForRetryHonorsTimerAndCancellation(t *testing.T) {
	t.Parallel()

	if err := waitForRetry(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("waitForRetry() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForRetry(ctx, time.Second); err == nil {
		t.Fatal("waitForRetry() ignored a cancelled context")
	}
}

// TestErrorMethodsKeepMessagesFreeOfSecrets verifies redacted error strings.
func TestErrorMethodsKeepMessagesFreeOfSecrets(t *testing.T) {
	t.Parallel()

	apiErr := &APIError{StatusCode: 401, Code: "unauthorized", Message: "authentication required"}
	if !strings.Contains(apiErr.Error(), "unauthorized") {
		t.Fatalf("APIError.Error() = %q", apiErr.Error())
	}
	timeoutErr := &TimeoutError{Message: "IAPStack request timed out", Cause: errors.New("private TLS diagnostics")}
	if strings.Contains(timeoutErr.Error(), "private TLS diagnostics") {
		t.Fatalf("TimeoutError leaked cause: %s", timeoutErr.Error())
	}
	if timeoutErr.Unwrap() == nil {
		t.Fatal("TimeoutError.Unwrap() = nil")
	}
	protocolErr := &ProtocolError{Message: "IAPStack returned an invalid JSON object", Cause: errors.New("secret")}
	if strings.Contains(protocolErr.Error(), "secret") {
		t.Fatalf("ProtocolError leaked cause: %s", protocolErr.Error())
	}
	if protocolErr.Unwrap() == nil {
		t.Fatal("ProtocolError.Unwrap() = nil")
	}
	if (&WebhookError{}).Error() == "" || (*TransportError)(nil).Error() == "" {
		t.Fatal("nil-safe Error() methods returned empty strings")
	}
}

// TestRetryPolicyValidateRejectsUnsafeBounds verifies retry configuration limits.
func TestRetryPolicyValidateRejectsUnsafeBounds(t *testing.T) {
	t.Parallel()

	if err := (RetryPolicy{MaxAttempts: 9, BaseDelay: time.Millisecond, MaxDelay: time.Second}).Validate(); err == nil {
		t.Fatal("Validate() accepted too many attempts")
	}
	if err := (RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Millisecond}).Validate(); err == nil {
		t.Fatal("Validate() accepted inverted delays")
	}
}

// TestRetryPolicyAppliesFullJitterWithinCap verifies Flutter-aligned backoff math.
func TestRetryPolicyAppliesFullJitterWithinCap(t *testing.T) {
	t.Parallel()

	policy := RetryPolicy{MaxAttempts: 3, BaseDelay: 100 * time.Millisecond, MaxDelay: 250 * time.Millisecond}
	zero, err := policy.DelayAfter(1, 0)
	if err != nil || zero != 0 {
		t.Fatalf("DelayAfter(1,0) = (%v, %v)", zero, err)
	}
	half, err := policy.DelayAfter(1, 0.5)
	if err != nil || half != 50*time.Millisecond {
		t.Fatalf("DelayAfter(1,0.5) = (%v, %v)", half, err)
	}
	capped, err := policy.DelayAfter(4, 1)
	if err != nil || capped != 250*time.Millisecond {
		t.Fatalf("DelayAfter(4,1) = (%v, %v)", capped, err)
	}
	if _, err := policy.DelayAfter(1, 1.1); err == nil {
		t.Fatal("DelayAfter() accepted an out-of-range jitter value")
	}
}

// newTestClient starts an HTTP test origin with Flutter-like proxy prefix defaults.
func newTestClient(t *testing.T, handler http.Handler, options ...func(*Config)) *Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	config := Config{
		BaseURL:           server.URL + "/proxy",
		ApplicationID:     "application-1",
		ApplicationToken:  testApplicationToken,
		Timeout:           time.Second,
		RetryPolicy:       RetryPolicy{MaxAttempts: 1, BaseDelay: 0, MaxDelay: 0},
		AllowInsecureHTTP: true,
	}
	for _, option := range options {
		option(&config)
	}
	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	client.delay = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(client.Close)
	return client
}

// captureRequest records one inbound HTTP request for contract assertions.
func captureRequest(t *testing.T, request *http.Request) capturedRequest {
	t.Helper()

	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return capturedRequest{
		Method:  request.Method,
		Path:    request.URL.Path,
		URI:     request.URL.RequestURI(),
		Headers: request.Header.Clone(),
		Body:    body,
	}
}

// writeJSON writes a JSON test response.
func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// entitlementSnapshotJSON returns a valid v1 entitlement snapshot fixture.
func entitlementSnapshotJSON() map[string]any {
	return map[string]any{
		"customer_id": "customer-internal",
		"entitlements": []map[string]any{
			{
				"key":                 "premium",
				"access":              "allowed",
				"reason":              "purchase_valid",
				"effective_starts_at": "2026-08-24T19:00:00Z",
				"version":             1,
			},
		},
	}
}
