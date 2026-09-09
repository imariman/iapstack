package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

const (
	// testMetricsBearerToken is a sufficiently long isolated metrics credential.
	testMetricsBearerToken = "metrics-test-bearer-token-32-characters"
)

// TestMain fails the package when HTTP lifecycle tests leave goroutines behind.
func TestMain(main *testing.M) {
	goleak.VerifyTestMain(main)
}

// TestMetricsRequiresConfiguredBearer verifies metrics fail closed and accept only the dedicated token.
func TestMetricsRequiresConfiguredBearer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		configured    string
		authorization string
		wantStatus    int
		wantMetrics   bool
	}{
		{name: "disabled", wantStatus: http.StatusNotFound},
		{name: "missing bearer", configured: testMetricsBearerToken, wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", configured: testMetricsBearerToken, authorization: "Basic " + testMetricsBearerToken, wantStatus: http.StatusUnauthorized},
		{name: "wrong token", configured: testMetricsBearerToken, authorization: "Bearer another-metrics-test-token-32-bytes", wantStatus: http.StatusUnauthorized},
		{name: "authorized", configured: testMetricsBearerToken, authorization: "Bearer " + testMetricsBearerToken, wantStatus: http.StatusOK, wantMetrics: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := newTestServerWithMetricsToken(tt.configured)
			request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			request.Header.Set("Authorization", tt.authorization)
			recorder := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if tt.wantMetrics && !strings.Contains(recorder.Body.String(), "iapstack_http_requests_total") {
				t.Fatalf("body = %q, want Prometheus metrics", recorder.Body.String())
			}
			if tt.wantStatus == http.StatusUnauthorized && recorder.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("WWW-Authenticate header is empty")
			}
		})
	}
}

// TestHealthAndReadiness verifies probe status, body, and content type behavior.
func TestHealthAndReadiness(t *testing.T) {
	t.Parallel()

	server := newTestServer()

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "health", path: "/healthz", wantStatus: http.StatusOK, wantBody: "{\"status\":\"ok\"}\n"},
		{name: "not ready", path: "/readyz", wantStatus: http.StatusServiceUnavailable, wantBody: "{\"status\":\"not_ready\"}\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			server.server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if recorder.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if recorder.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", recorder.Body.String(), tt.wantBody)
			}
			if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", contentType)
			}
		})
	}

	server.ready.Store(true)
	recorder := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("ready status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

// TestRunStopsAfterCancellation verifies readiness transitions and graceful cancellation.
func TestRunStopsAfterCancellation(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- server.Run(ctx)
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for !server.ready.Load() {
		select {
		case err := <-result:
			t.Fatalf("Run() returned before ready: %v", err)
		case <-deadline.C:
			t.Fatal("server did not become ready")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop after cancellation")
	}

	if server.ready.Load() {
		t.Fatal("server remained ready after shutdown")
	}
}

// TestServerAppliesConnectionAndBrowserHardening verifies bounded resources and defensive response headers.
func TestServerAppliesConnectionAndBrowserHardening(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	if server.server.ReadTimeout != readTimeout || server.server.WriteTimeout != writeTimeout ||
		server.server.ReadHeaderTimeout != readHeaderTimeout || server.server.MaxHeaderBytes != maximumHeaderBytes {
		t.Fatalf("HTTP limits = (%v, %v, %v, %d), want (%v, %v, %v, %d)",
			server.server.ReadTimeout, server.server.WriteTimeout, server.server.ReadHeaderTimeout,
			server.server.MaxHeaderBytes, readTimeout, writeTimeout, readHeaderTimeout, maximumHeaderBytes)
	}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set(requestIDHeader, "unsafe request id")
	recorder := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(recorder, request)
	if requestID := recorder.Header().Get(requestIDHeader); requestID == "" || requestID == "unsafe request id" {
		t.Fatalf("response request ID = %q, want generated safe value", requestID)
	}
	if recorder.Header().Get("X-Content-Type-Options") != "nosniff" ||
		recorder.Header().Get("X-Frame-Options") != "DENY" ||
		recorder.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("security headers = %#v", recorder.Header())
	}
}

// TestObserveCollapsesUnsupportedHTTPMethods verifies unauthenticated method noise stays one series.
func TestObserveCollapsesUnsupportedHTTPMethods(t *testing.T) {
	t.Parallel()

	server := newTestServer()
	for index := 0; index < 32; index++ {
		request := httptest.NewRequest(fmt.Sprintf("AUDIT%d", index), "/healthz", nil)
		recorder := httptest.NewRecorder()
		server.server.Handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
		}
	}
	longMethod := strings.Repeat("Z", 1024)
	request := httptest.NewRequest(longMethod, "/healthz", nil)
	recorder := httptest.NewRecorder()
	server.server.Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("long method status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}

	var output bytes.Buffer
	if err := server.metrics.WritePrometheus(&output); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	metricsOutput := output.String()
	if !strings.Contains(metricsOutput, `iapstack_http_requests_total{method="OTHER"`) {
		t.Fatalf("metrics output omitted OTHER method series\n%s", metricsOutput)
	}
	if strings.Contains(metricsOutput, `method="AUDIT`) || strings.Contains(metricsOutput, `method="ZZ`) {
		t.Fatalf("metrics output retained an unsupported method label\n%s", metricsOutput)
	}
	series := 0
	for _, line := range strings.Split(metricsOutput, "\n") {
		if strings.HasPrefix(line, "iapstack_http_requests_total{") {
			series++
		}
	}
	if series != 1 {
		t.Fatalf("request series count = %d, want 1\n%s", series, metricsOutput)
	}
}

// newTestServer constructs an isolated loopback server for lifecycle tests.
func newTestServer() *Server {
	return newTestServerWithMetricsToken("")
}

// newTestServerWithMetricsToken constructs an isolated server with optional metrics authentication.
func newTestServerWithMetricsToken(metricsBearerToken string) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewWithOptions(Options{
		Address: "127.0.0.1:0", ShutdownTimeout: time.Second,
		ReadinessTimeout: defaultReadinessTimeout, Logger: logger,
		MetricsBearerToken: metricsBearerToken,
	})
}
