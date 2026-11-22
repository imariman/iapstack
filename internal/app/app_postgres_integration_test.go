//go:build integration

package app_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/app"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/jackc/pgx/v5"
)

const (
	// appIntegrationDatabaseURLKey names the opt-in PostgreSQL connection setting.
	appIntegrationDatabaseURLKey = "IAPSTACK_TEST_DATABASE_URL"
	// appIntegrationDatabaseTimeout bounds database setup, process execution, and cleanup.
	appIntegrationDatabaseTimeout = 45 * time.Second
	// appIntegrationDatabaseAdvisoryLock serializes schema-resetting integration test packages.
	appIntegrationDatabaseAdvisoryLock int64 = 424090117
	// appIntegrationBootstrapAdminKey authenticates the real administration endpoint.
	appIntegrationBootstrapAdminKey = "app-integration-bootstrap-key-32-bytes"
	// appIntegrationMetricsBearer authenticates both operational metrics endpoints.
	appIntegrationMetricsBearer = "app-integration-metrics-key-32-bytes"
)

// appIntegrationDatabase owns one locked connection and migration runner for process tests.
type appIntegrationDatabase struct {
	ctx         context.Context
	connection  *pgx.Conn
	migrator    *postgres.Migrator
	databaseURL string
}

// TestRunMigratesAndServesCompactProcessWithPostgreSQL verifies production composition and graceful shutdown.
func TestRunMigratesAndServesCompactProcessWithPostgreSQL(t *testing.T) {
	database := openAppIntegrationDatabase(t)
	apiAddress := reserveAppIntegrationAddress(t)
	workerAddress := reserveAppIntegrationAddress(t)
	environment := appIntegrationEnvironment(database.databaseURL, apiAddress, workerAddress)
	getenv := func(name string) string { return environment[name] }
	var logs bytes.Buffer

	if err := app.Run(database.ctx, []string{"migrate"}, getenv, &logs); err != nil {
		t.Fatalf("Run(migrate) error = %v", err)
	}
	version, err := database.migrator.Version(database.ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != postgres.LatestVersion {
		t.Fatalf("schema version = %d, want %d", version, postgres.LatestVersion)
	}

	runContext, cancel := context.WithCancel(database.ctx)
	result := make(chan error, 1)
	go func() {
		result <- app.Run(runContext, []string{"server"}, getenv, &logs)
	}()
	transport := &http.Transport{}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	t.Cleanup(transport.CloseIdleConnections)

	apiBaseURL := "http://" + apiAddress
	workerBaseURL := "http://" + workerAddress
	waitForAppIntegrationStatus(t, client, result, apiBaseURL+"/readyz", http.StatusOK)
	waitForAppIntegrationStatus(t, client, result, workerBaseURL+"/readyz", http.StatusOK)
	assertAppIntegrationRequest(t, client, apiBaseURL+"/healthz", "", http.StatusOK, "application/json")
	assertAppIntegrationRequest(t, client, workerBaseURL+"/healthz", "", http.StatusOK, "application/json")
	assertAppIntegrationRequest(t, client, apiBaseURL+"/dashboard/", "", http.StatusOK, "text/html")
	projectsBody := assertAppIntegrationRequest(
		t,
		client,
		apiBaseURL+"/v1/admin/projects",
		appIntegrationBootstrapAdminKey,
		http.StatusOK,
		"application/json",
	)
	if !bytes.Contains(projectsBody, []byte(`"projects":[]`)) {
		t.Fatalf("admin projects body = %s, want empty project collection", projectsBody)
	}
	assertAppIntegrationRequest(
		t,
		client,
		apiBaseURL+"/metrics",
		appIntegrationMetricsBearer,
		http.StatusOK,
		"text/plain",
	)
	assertAppIntegrationRequest(
		t,
		client,
		workerBaseURL+"/metrics",
		appIntegrationMetricsBearer,
		http.StatusOK,
		"text/plain",
	)

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run(server) error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run(server) did not stop after cancellation")
	}
	transport.CloseIdleConnections()

	logged := logs.String()
	for _, expected := range []string{"starting iapstack", "api is ready", "worker is ready", "shutting down api"} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("process logs omitted %q: %s", expected, logged)
		}
	}
	for _, secret := range []string{
		database.databaseURL,
		appIntegrationBootstrapAdminKey,
		appIntegrationMetricsBearer,
		environment["IAPSTACK_PROTECTION_KEY"],
		environment["IAPSTACK_PROTECTION_FINGERPRINT_KEY"],
	} {
		if strings.Contains(logged, secret) {
			t.Fatalf("process logs exposed secret material")
		}
	}
}

// appIntegrationEnvironment returns the complete production configuration used by the compact process.
func appIntegrationEnvironment(databaseURL, apiAddress, workerAddress string) map[string]string {
	return map[string]string{
		"IAPSTACK_DATABASE_URL":                    databaseURL,
		"IAPSTACK_HTTP_ADDRESS":                    apiAddress,
		"IAPSTACK_WORKER_HTTP_ADDRESS":             workerAddress,
		"IAPSTACK_SHUTDOWN_TIMEOUT":                "2s",
		"IAPSTACK_READINESS_TIMEOUT":               "500ms",
		"IAPSTACK_BOOTSTRAP_ADMIN_KEY":             appIntegrationBootstrapAdminKey,
		"IAPSTACK_METRICS_BEARER_TOKEN":            appIntegrationMetricsBearer,
		"IAPSTACK_WORKER_ID":                       "app-integration-worker",
		"IAPSTACK_WORKER_POLL_INTERVAL":            "100ms",
		"IAPSTACK_WORKER_JOB_TIMEOUT":              "2s",
		"IAPSTACK_WORKER_CONCURRENCY":              "1",
		"IAPSTACK_WORKER_MAX_ATTEMPTS":             "3",
		"IAPSTACK_QUEUE_RETENTION":                 "24h",
		"IAPSTACK_PROVIDER_TIMEOUT":                "1s",
		"IAPSTACK_WEBHOOK_TIMEOUT":                 "1s",
		"IAPSTACK_PROTECTION_ACTIVE_KEY_ID":        "app-integration-key",
		"IAPSTACK_PROTECTION_KEY":                  base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{61}, 32)),
		"IAPSTACK_PROTECTION_FINGERPRINT_KEY":      base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{79}, 32)),
		"IAPSTACK_AUTH_MAX_CONCURRENT_DERIVATIONS": "2",
	}
}

// reserveAppIntegrationAddress obtains one currently available loopback address for a process listener.
func reserveAppIntegrationAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve process address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release process address: %v", err)
	}
	return address
}

// waitForAppIntegrationStatus waits for one process endpoint while failing fast on process exit.
func waitForAppIntegrationStatus(
	t *testing.T,
	client *http.Client,
	result <-chan error,
	url string,
	expectedStatus int,
) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			t.Fatalf("compact process stopped before %s became ready: %v", url, err)
		case <-deadline.C:
			t.Fatalf("%s did not return status %d", url, expectedStatus)
		case <-ticker.C:
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
			if err != nil {
				t.Fatalf("create readiness request: %v", err)
			}
			response, err := client.Do(request)
			if err != nil {
				continue
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == expectedStatus {
				return
			}
		}
	}
}

// assertAppIntegrationRequest verifies one compact-process HTTP success contract.
func assertAppIntegrationRequest(
	t *testing.T,
	client *http.Client,
	url string,
	bearer string,
	expectedStatus int,
	expectedContentType string,
) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("create request for %s: %v", url, err)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("request %s: %v", url, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatalf("read response from %s: %v", url, err)
	}
	if response.StatusCode != expectedStatus {
		t.Fatalf("%s status = %d, want %d: %s", url, response.StatusCode, expectedStatus, payload)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), expectedContentType) {
		t.Fatalf("%s content type = %q, want prefix %q", url, response.Header.Get("Content-Type"), expectedContentType)
	}
	return payload
}

// openAppIntegrationDatabase resets and locks one PostgreSQL database for process execution.
func openAppIntegrationDatabase(t *testing.T) *appIntegrationDatabase {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv(appIntegrationDatabaseURLKey))
	if databaseURL == "" {
		t.Skipf("%s is not configured", appIntegrationDatabaseURLKey)
	}
	ctx, cancel := context.WithTimeout(context.Background(), appIntegrationDatabaseTimeout)
	t.Cleanup(cancel)
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to app integration PostgreSQL: %v", err)
	}
	if _, err := connection.Exec(ctx, `SELECT pg_advisory_lock($1)`, appIntegrationDatabaseAdvisoryLock); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("lock app integration PostgreSQL: %v", err)
	}
	migrator, err := postgres.NewMigrator(ctx, connection)
	if err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("NewMigrator() error = %v", err)
	}
	if err := postgres.RemoveRiver(ctx, databaseURL); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset app integration River schema: %v", err)
	}
	if err := migrator.MigrateTo(ctx, 0); err != nil {
		_ = connection.Close(ctx)
		t.Fatalf("reset app integration PostgreSQL: %v", err)
	}
	database := &appIntegrationDatabase{
		ctx: ctx, connection: connection, migrator: migrator, databaseURL: databaseURL,
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), appIntegrationDatabaseTimeout)
		defer cleanupCancel()
		if err := postgres.RemoveRiver(cleanupContext, databaseURL); err != nil {
			t.Errorf("reset app integration River schema during cleanup: %v", err)
		}
		if err := migrator.MigrateTo(cleanupContext, 0); err != nil {
			t.Errorf("reset app integration PostgreSQL during cleanup: %v", err)
		}
		if _, err := connection.Exec(cleanupContext, `SELECT pg_advisory_unlock($1)`, appIntegrationDatabaseAdvisoryLock); err != nil {
			t.Errorf("unlock app integration PostgreSQL: %v", err)
		}
		if err := connection.Close(cleanupContext); err != nil {
			t.Errorf("close app integration PostgreSQL: %v", err)
		}
	})
	return database
}
