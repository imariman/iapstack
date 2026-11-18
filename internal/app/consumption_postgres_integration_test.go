//go:build integration

package app_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/app"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/protection"
)

// TestWorkerClosesAppleConsumptionRequestThroughRiver verifies the compact process runs the consumption queue.
// The application never saved settings, so the worker records a disabled skip without opening the payload.
func TestWorkerClosesAppleConsumptionRequestThroughRiver(t *testing.T) {
	database := openAppIntegrationDatabase(t)
	apiAddress := reserveAppIntegrationAddress(t)
	workerAddress := reserveAppIntegrationAddress(t)
	environment := appIntegrationEnvironment(database.databaseURL, apiAddress, workerAddress)
	getenv := func(name string) string { return environment[name] }
	var logs bytes.Buffer
	if err := app.Run(database.ctx, []string{"migrate"}, getenv, &logs); err != nil {
		t.Fatalf("Run(migrate) error = %v", err)
	}
	if _, err := database.connection.Exec(database.ctx, `
		INSERT INTO projects (id) VALUES ('consumption-project');
		INSERT INTO applications (id, project_id, provider, environment, provider_application_id)
		VALUES ('consumption-application', 'consumption-project', 'apple_app_store', 'sandbox', 'com.example.iapstack');
	`); err != nil {
		t.Fatalf("seed consumption application: %v", err)
	}
	store, err := postgres.OpenStore(database.ctx, database.databaseURL)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Second)
	payload := []byte("unreadable-without-the-keyring")
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, saveErr := repository.SaveAppleConsumptionRequest(database.ctx, persistence.AppleConsumptionRequest{
			ID: "consumption-app-1", ProjectID: "consumption-project", ApplicationID: "consumption-application",
			ProviderProductID: "iapstack.pro", ProductType: "auto_renewable_subscription",
			Payload:  protection.Value{Ciphertext: payload, Fingerprint: sha256.Sum256(payload), KeyID: "app-integration-key"},
			SignedAt: now.Add(-time.Minute), ReceivedAt: now.Add(-time.Minute), DeadlineAt: now.Add(12 * time.Hour),
			AvailableAt: now.Add(-time.Minute),
		})
		return saveErr
	}); err != nil {
		t.Fatalf("SaveAppleConsumptionRequest() error = %v", err)
	}

	runContext, cancel := context.WithCancel(database.ctx)
	result := make(chan error, 1)
	go func() {
		result <- app.Run(runContext, []string{"worker"}, getenv, &logs)
	}()
	client := &http.Client{Timeout: time.Second}
	waitForAppIntegrationStatus(t, client, result, "http://"+workerAddress+"/readyz", http.StatusOK)
	var status, reason, outcome string
	var processed bool
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		err := database.connection.QueryRow(database.ctx, `
			SELECT request.status, COALESCE(request.status_reason, ''), request.processed_at IS NOT NULL,
				COALESCE((SELECT attempt.outcome FROM apple_consumption_attempts AS attempt
					WHERE attempt.request_id = request.id ORDER BY attempt.attempt DESC LIMIT 1), '')
			FROM apple_consumption_requests AS request WHERE request.id = 'consumption-app-1'
		`).Scan(&status, &reason, &processed, &outcome)
		if err != nil {
			t.Fatalf("load consumption request: %v", err)
		}
		if processed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run(worker) error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run(worker) did not stop after cancellation")
	}
	if !processed || status != persistence.ConsumptionStatusSkipped || reason != "disabled" ||
		outcome != persistence.ConsumptionAttemptSkipped {
		t.Fatalf("consumption request = (%s, %s, processed %t, attempt %s)", status, reason, processed, outcome)
	}
	if bytes.Contains(logs.Bytes(), payload) {
		t.Fatal("worker logs exposed the protected payload")
	}
}
