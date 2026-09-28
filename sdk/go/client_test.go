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
	// Method is the HTTP method received by the test origin.
	Method string
	// Path is the decoded URL path received by the test origin.
	Path string
	// URI is the raw request URI, including encoded path segments.
	URI string
	// Headers are the inbound request headers.
	Headers http.Header
	// Body is the exact JSON request body.
	Body []byte
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
	if session.Token != "iaps_customer" || session.ExternalCustomerID != "customer-external" ||
		!session.ExpiresAt.Equal(time.Date(2026, 8, 24, 20, 15, 0, 0, time.UTC)) {
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

	snapshot, err := client.GetEntitlements(context.Background(), CustomerSession{
		Token:              testCustomerToken,
		ExternalCustomerID: "customer-external",
	})
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

	if _, err := client.GetEntitlements(context.Background(), CustomerSession{
		Token:              testCustomerToken,
		ExternalCustomerID: "customer/with space",
	}); err != nil {
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
		config.RetryPolicy = RetryPolicy{MaxAttempts: 2}
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

// TestClientHonorsRetryAfterCooldown verifies Retry-After raises the wait above the jitter delay.
func TestClientHonorsRetryAfterCooldown(t *testing.T) {
	t.Parallel()

	var waited time.Duration
	var attempts atomic.Int32
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if attempts.Add(1) == 1 {
			writer.Header().Set("Retry-After", "3")
			writeJSON(writer, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]any{"code": "provider_unavailable", "message": "cooling down"},
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
	if waited != 3*time.Second {
		t.Fatalf("waited = %s, want 3s", waited)
	}
}

// TestAPIErrorExposesRetryAfter verifies delay-seconds and HTTP-date parsing on the error value.
func TestAPIErrorExposesRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "absent", header: "", want: 0},
		{name: "seconds", header: "17", want: 17 * time.Second},
		{name: "padded seconds", header: " 5 ", want: 5 * time.Second},
		{name: "http date", header: now.Add(90 * time.Second).Format(http.TimeFormat), want: 90 * time.Second},
		{name: "elapsed http date", header: now.Add(-time.Minute).Format(http.TimeFormat), want: 0},
		{name: "negative", header: "-3", want: 0},
		{name: "fraction", header: "1.5", want: 0},
		{name: "garbage", header: "soon", want: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			header := http.Header{}
			if testCase.header != "" {
				header.Set("Retry-After", testCase.header)
			}
			apiErr := apiErrorFromResponse(rawResponse{StatusCode: http.StatusTooManyRequests, Header: header, Body: []byte("{}")}, now)
			if apiErr.RetryAfter != testCase.want {
				t.Fatalf("RetryAfter = %s, want %s", apiErr.RetryAfter, testCase.want)
			}
			if !apiErr.Retryable {
				t.Fatal("429 must stay retryable")
			}
		})
	}
}

// TestRetryPolicyDelayAfterResponseBoundsRetryAfter verifies max(jitter, Retry-After) capped at MaxRetryAfter.
func TestRetryPolicyDelayAfterResponseBoundsRetryAfter(t *testing.T) {
	t.Parallel()

	policy := RetryPolicy{MaxAttempts: 3, BaseDelay: 100 * time.Millisecond, MaxDelay: 250 * time.Millisecond}
	for _, testCase := range []struct {
		name       string
		random     float64
		retryAfter time.Duration
		want       time.Duration
	}{
		{name: "no header keeps jitter", random: 0.5, retryAfter: 0, want: 50 * time.Millisecond},
		{name: "jitter wins when larger", random: 1, retryAfter: 20 * time.Millisecond, want: 100 * time.Millisecond},
		{name: "retry after wins when larger", random: 0, retryAfter: 4 * time.Second, want: 4 * time.Second},
		{name: "capped", random: 1, retryAfter: time.Hour, want: MaxRetryAfter},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got, err := policy.DelayAfterResponse(1, testCase.random, testCase.retryAfter)
			if err != nil {
				t.Fatalf("DelayAfterResponse() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("DelayAfterResponse() = %s, want %s", got, testCase.want)
			}
		})
	}
	if _, err := policy.DelayAfterResponse(1, 2, time.Second); err == nil {
		t.Fatal("DelayAfterResponse() accepted an out-of-range random value")
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
		config.RetryPolicy = RetryPolicy{MaxAttempts: 2}
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
	if _, err := client.GetEntitlements(context.Background(), CustomerSession{Token: testCustomerToken}); err == nil {
		t.Fatal("GetEntitlements() accepted an empty customer ID")
	}
	_, err := client.GetEntitlements(context.Background(), CustomerSession{
		Token:              testSecretBearer,
		ExternalCustomerID: "customer-external",
	})
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
	webhookErr := &WebhookError{Code: "receiver_unavailable", StatusCode: http.StatusServiceUnavailable, Cause: errors.New("disk full")}
	if webhookErr.Error() != "receiver_unavailable" || !errors.Is(webhookErr, webhookErr.Cause) {
		t.Fatalf("WebhookError = %v", webhookErr)
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
		RetryPolicy:       RetryPolicy{MaxAttempts: 1},
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

// TestPartialRetryPolicyFillsEveryZeroField verifies omitted retry fields take defaults.
func TestPartialRetryPolicyFillsEveryZeroField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input RetryPolicy
		want  RetryPolicy
	}{
		{
			name:  "attempts only keeps default backoff",
			input: RetryPolicy{MaxAttempts: 5},
			want:  RetryPolicy{MaxAttempts: 5, BaseDelay: defaultBaseDelay, MaxDelay: defaultMaxDelay},
		},
		{
			name:  "max delay only",
			input: RetryPolicy{MaxDelay: time.Second},
			want:  RetryPolicy{MaxAttempts: defaultMaxAttempts, BaseDelay: defaultBaseDelay, MaxDelay: time.Second},
		},
		{
			name:  "base delay only",
			input: RetryPolicy{BaseDelay: 100 * time.Millisecond},
			want:  RetryPolicy{MaxAttempts: defaultMaxAttempts, BaseDelay: 100 * time.Millisecond, MaxDelay: defaultMaxDelay},
		},
		{
			name:  "delays without attempts",
			input: RetryPolicy{BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second},
			want:  RetryPolicy{MaxAttempts: defaultMaxAttempts, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second},
		},
		{
			name:  "zero value",
			input: RetryPolicy{},
			want:  DefaultRetryPolicy(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			config := Config{
				BaseURL:          "https://iap.example",
				ApplicationID:    "application-1",
				ApplicationToken: testApplicationToken,
				RetryPolicy:      test.input,
			}.applyDefaults()
			if config.RetryPolicy != test.want {
				t.Fatalf("retry policy = %+v, want %+v", config.RetryPolicy, test.want)
			}
			if err := config.Validate(); err != nil {
				t.Fatalf("Validate() = %v", err)
			}
		})
	}
}

// TestPartialRetryPolicyStillValidatesOrder verifies a filled default cannot hide unordered delays.
func TestPartialRetryPolicyStillValidatesOrder(t *testing.T) {
	t.Parallel()

	config := Config{
		BaseURL:          "https://iap.example",
		ApplicationID:    "application-1",
		ApplicationToken: testApplicationToken,
		RetryPolicy:      RetryPolicy{BaseDelay: 5 * time.Second},
	}.applyDefaults()
	if err := config.Validate(); err == nil {
		t.Fatalf("Validate() accepted BaseDelay above the default MaxDelay: %+v", config.RetryPolicy)
	}
}

// TestClientBacksOffWithAttemptsOnlyPolicy verifies RetryPolicy{MaxAttempts: n} does not retry back-to-back.
func TestClientBacksOffWithAttemptsOnlyPolicy(t *testing.T) {
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
		config.RetryPolicy = RetryPolicy{MaxAttempts: 2}
	})
	client.jitter = func() float64 { return 1 }
	client.delay = func(_ context.Context, delay time.Duration) error {
		waited = delay
		return nil
	}

	if _, err := client.CreateCustomerSession(context.Background(), "customer-external"); err != nil {
		t.Fatalf("CreateCustomerSession() error = %v", err)
	}
	if attempts.Load() != 2 || waited != defaultBaseDelay {
		t.Fatalf("attempts = %d waited = %s, want 2 attempts and %s", attempts.Load(), waited, defaultBaseDelay)
	}
}
