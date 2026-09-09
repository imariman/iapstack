//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/webhookreceiver"
	"github.com/jackc/pgx/v5"
	"go.uber.org/goleak"
)

const (
	// processTestSecret is a non-production HMAC fixture with the required entropy length.
	processTestSecret = "0123456789abcdef0123456789abcdef"
	// processTestEventID isolates the durable process fixture from other receiver tests.
	processTestEventID = "receiver-command-process-success-test"
	// processTestApplicationID identifies the synthetic application in persisted assertions.
	processTestApplicationID = "receiver-command-test-app"
	// processTestEventType identifies the synthetic event in persisted assertions.
	processTestEventType = "entitlement.changed"
)

// TestMain rejects goroutines leaked by the real receiver process lifecycle.
func TestMain(main *testing.M) {
	goleak.VerifyTestMain(main)
}

// TestRunServesAndDeduplicatesSignedWebhooksWithPostgreSQL verifies the deployable process success path.
func TestRunServesAndDeduplicatesSignedWebhooksWithPostgreSQL(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("IAPSTACK_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("IAPSTACK_TEST_DATABASE_URL is not configured")
	}
	port := reserveLoopbackPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	environment := map[string]string{
		"PORT":                                   port,
		"IAPSTACK_WEBHOOK_RECEIVER_DATABASE_URL": databaseURL,
		"IAPSTACK_WEBHOOK_RECEIVER_SECRET":       processTestSecret,
	}
	result := make(chan error, 1)
	go func() {
		result <- run(ctx, func(key string) string { return environment[key] }, logger)
	}()

	transport := &http.Transport{DisableKeepAlives: true}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	t.Cleanup(transport.CloseIdleConnections)
	baseURL := "http://127.0.0.1:" + port
	waitForReceiverReady(t, client, baseURL+webhookreceiver.ReadinessPath, result)
	assertReceiverStatus(t, client, http.MethodGet, baseURL+webhookreceiver.HealthPath, nil, nil, http.StatusNoContent)

	database, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect to receiver database: %v", err)
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, cleanupErr := database.Exec(cleanupContext, `DELETE FROM iapstack_webhook_receiver_events WHERE event_id = $1`, processTestEventID); cleanupErr != nil {
			t.Errorf("delete receiver process fixture: %v", cleanupErr)
		}
		database.Close(cleanupContext)
	})
	if _, err := database.Exec(context.Background(), `DELETE FROM iapstack_webhook_receiver_events WHERE event_id = $1`, processTestEventID); err != nil {
		t.Fatalf("reset receiver process fixture: %v", err)
	}

	body := []byte(`{"schema_version":1,"application_id":"` + processTestApplicationID + `","event_type":"` + processTestEventType + `"}`)
	for delivery := 0; delivery < 2; delivery++ {
		headers := signedReceiverHeaders(body, time.Now().UTC())
		assertReceiverStatus(t, client, http.MethodPost, baseURL+webhookreceiver.WebhookPath, body, headers, http.StatusNoContent)
	}
	var rowCount int
	var applicationID string
	var eventType string
	if err := database.QueryRow(context.Background(), `
SELECT count(*), coalesce(min(application_id), ''), coalesce(min(event_type), '')
FROM iapstack_webhook_receiver_events
WHERE event_id = $1`, processTestEventID).Scan(&rowCount, &applicationID, &eventType); err != nil {
		t.Fatalf("query persisted receiver event: %v", err)
	}
	if rowCount != 1 || applicationID != processTestApplicationID || eventType != processTestEventType {
		t.Fatalf("persisted receiver event = (%d, %q, %q), want (1, %q, %q)", rowCount, applicationID, eventType, processTestApplicationID, processTestEventType)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run() shutdown error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not complete graceful shutdown")
	}
	logOutput := logs.String()
	for _, expected := range []string{"webhook receiver is ready", `"delivery":"inserted"`, `"delivery":"duplicate"`} {
		if !strings.Contains(logOutput, expected) {
			t.Errorf("process logs do not contain %q: %s", expected, logOutput)
		}
	}
	for _, sensitive := range []string{databaseURL, processTestSecret} {
		if strings.Contains(logOutput, sensitive) {
			t.Errorf("process logs expose sensitive configuration")
		}
	}
}

// reserveLoopbackPort finds an ephemeral loopback port for the process under test.
func reserveLoopbackPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatalf("release loopback port: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("parse loopback address: %v", err)
	}
	return port
}

// waitForReceiverReady polls readiness until the server accepts requests or exits.
func waitForReceiverReady(t *testing.T, client *http.Client, url string, result <-chan error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-result:
			t.Fatalf("run() exited before readiness: %v", err)
		default:
		}
		response, err := client.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("receiver did not become ready at %s", url)
}

// signedReceiverHeaders returns the canonical authentication headers for one delivery.
func signedReceiverHeaders(body []byte, timestamp time.Time) http.Header {
	timestampText := strconv.FormatInt(timestamp.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(processTestSecret))
	_, _ = io.WriteString(mac, "v2\n")
	_, _ = io.WriteString(mac, processTestEventID)
	_, _ = io.WriteString(mac, "\n")
	_, _ = io.WriteString(mac, timestampText)
	_, _ = io.WriteString(mac, "\n")
	_, _ = mac.Write(body)
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("IAPStack-Event-ID", processTestEventID)
	headers.Set("IAPStack-Timestamp", timestampText)
	headers.Set("IAPStack-Signature", "v2="+hex.EncodeToString(mac.Sum(nil)))
	return headers
}

// assertReceiverStatus sends one request and verifies the exact HTTP status.
func assertReceiverStatus(t *testing.T, client *http.Client, method string, url string, body []byte, headers http.Header, expected int) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create receiver request: %v", err)
	}
	request.Header = headers.Clone()
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send receiver request: %v", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read receiver response: %v", err)
	}
	if response.StatusCode != expected {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, url, response.StatusCode, expected, responseBody)
	}
}
