// Package main drives the black-box Compose release gate against real IAPStack containers.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// projectID is the stable control-plane project created by the release gate.
	projectID = "project-e2e"
	// applicationID is the stable IAPStack application created by the release gate.
	applicationID = "application-e2e"
	// customerID is the stable internal customer created by the release gate.
	customerID = "customer-e2e"
	// entitlementID is the stable entitlement definition created by the release gate.
	entitlementID = "entitlement-e2e"
	// productID is the stable internal lifetime product created by the release gate.
	productID = "product-e2e"
	// entitlementKey is the public access key asserted by the release gate.
	entitlementKey = "premium"
	// credentialKind is the Huawei credential kind accepted by the production adapter.
	credentialKind = "huawei_server_api" // #nosec G101 -- This is a public credential type discriminator used by the release fixture.
	// credentialContentType is the versioned Huawei credential media type.
	credentialContentType = "application/vnd.iapstack.huawei-credentials+json"
	// defaultAPIBaseURL is the host origin published by the E2E Compose stack.
	defaultAPIBaseURL = "http://127.0.0.1:18080"
	// defaultWorkerBaseURL is the host origin for worker probes and metrics.
	defaultWorkerBaseURL = "http://127.0.0.1:18081"
	// defaultFixtureBaseURL is the host origin for fixture controls.
	defaultFixtureBaseURL = "http://127.0.0.1:18082"
	// requestTimeout bounds one black-box HTTP operation.
	requestTimeout = 10 * time.Second
	// eventualTimeout bounds asynchronous River and webhook assertions.
	eventualTimeout = 30 * time.Second
	// eventualInterval controls release-gate polling without stressing the stack.
	eventualInterval = 250 * time.Millisecond
)

// gateClient contains the three black-box origins and installation secrets used by the gate.
type gateClient struct {
	apiBaseURL     string
	workerBaseURL  string
	fixtureBaseURL string
	bootstrapAdmin string
	webhookSecret  string
	client         *http.Client
}

// fixtureScenario contains safe provider configuration and current signed evidence.
type fixtureScenario struct {
	ProviderApplicationID string          `json:"provider_application_id"`
	ProviderProductID     string          `json:"provider_product_id"`
	ExternalCustomerID    string          `json:"external_customer_id"`
	Credential            json.RawMessage `json:"credential"`
	Evidence              json.RawMessage `json:"evidence"`
	Notification          json.RawMessage `json:"notification"`
}

// keyResponse contains the one-time bearer returned by the administration API.
type keyResponse struct {
	Key string `json:"key"`
}

// customerSessionResponse contains one short-lived customer bearer minted by the trusted gate client.
type customerSessionResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// entitlementSnapshot is the application-facing customer projection response.
type entitlementSnapshot struct {
	CustomerID   string        `json:"customer_id"`
	Entitlements []entitlement `json:"entitlements"`
}

// entitlement contains the stable access fields asserted across lifecycle transitions.
type entitlement struct {
	Key     string `json:"key"`
	Access  string `json:"access"`
	Reason  string `json:"reason"`
	Version int64  `json:"version"`
}

// deliveryReport summarizes webhook attempts observed by the fixture.
type deliveryReport struct {
	Attempts           int  `json:"attempts"`
	UniqueEventIDs     int  `json:"unique_event_ids"`
	AllSignaturesValid bool `json:"all_signatures_valid"`
}

// main validates configuration and runs one explicit release-gate phase.
func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "compose release gate failed: %v\n", err)
		os.Exit(1)
	}
}

// run selects one phase so the shell harness can stop and restart the real worker between assertions.
func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: iapstack-e2e <bootstrap|enqueue-notification|assert-recovery>")
	}
	client, err := newGateClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*eventualTimeout)
	defer cancel()
	switch os.Args[1] {
	case "bootstrap":
		return client.bootstrap(ctx)
	case "enqueue-notification":
		return client.enqueueNotification(ctx)
	case "assert-recovery":
		return client.assertRecovery(ctx)
	default:
		return fmt.Errorf("unsupported release-gate phase %q", os.Args[1])
	}
}

// newGateClient loads host origins and the two test-only secrets without printing them.
func newGateClient() (*gateClient, error) {
	bootstrapAdmin := strings.TrimSpace(os.Getenv("IAPSTACK_BOOTSTRAP_ADMIN_KEY"))
	webhookSecret := strings.TrimSpace(os.Getenv("IAPSTACK_E2E_WEBHOOK_SECRET"))
	if bootstrapAdmin == "" || len(webhookSecret) < 16 {
		return nil, errors.New("bootstrap admin and E2E webhook secrets are required")
	}
	return &gateClient{
		apiBaseURL:     valueOrDefault(os.Getenv("IAPSTACK_E2E_API_BASE_URL"), defaultAPIBaseURL),
		workerBaseURL:  valueOrDefault(os.Getenv("IAPSTACK_E2E_WORKER_BASE_URL"), defaultWorkerBaseURL),
		fixtureBaseURL: valueOrDefault(os.Getenv("IAPSTACK_E2E_FIXTURE_BASE_URL"), defaultFixtureBaseURL),
		bootstrapAdmin: bootstrapAdmin,
		webhookSecret:  webhookSecret,
		client: &http.Client{
			Timeout:       requestTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// bootstrap configures a Huawei application, verifies one purchase twice, and observes one signed webhook.
func (client *gateClient) bootstrap(ctx context.Context) error {
	for _, endpoint := range []string{
		client.apiBaseURL + "/healthz",
		client.apiBaseURL + "/readyz",
		client.workerBaseURL + "/healthz",
		client.workerBaseURL + "/readyz",
		client.fixtureBaseURL + "/healthz",
	} {
		if _, err := client.request(ctx, http.MethodGet, endpoint, "", nil, nil, http.StatusOK); err != nil {
			return err
		}
	}
	if _, err := client.request(ctx, http.MethodPut, client.fixtureBaseURL+"/state/active", "", nil, nil, http.StatusOK); err != nil {
		return err
	}
	scenario, err := client.scenario(ctx)
	if err != nil {
		return err
	}
	adminKey, err := client.createKey(ctx, client.bootstrapAdmin, map[string]any{"role": "admin"})
	if err != nil {
		return err
	}
	mutations := []struct {
		path string
		body any
	}{
		{path: "/v1/admin/projects/" + projectID, body: map[string]any{}},
		{path: "/v1/admin/projects/" + projectID + "/applications/" + applicationID, body: map[string]any{
			"provider": "huawei_appgallery", "environment": "sandbox",
			"provider_application_id": scenario.ProviderApplicationID,
		}},
		{path: "/v1/admin/projects/" + projectID + "/customers/" + customerID, body: map[string]any{
			"external_id": scenario.ExternalCustomerID,
		}},
		{path: "/v1/admin/projects/" + projectID + "/entitlements/" + entitlementID, body: map[string]any{
			"key": entitlementKey,
		}},
		{path: "/v1/admin/projects/" + projectID + "/products/" + productID, body: map[string]any{
			"kind": "non_consumable", "entitlement_ids": []string{entitlementID},
		}},
		{path: "/v1/admin/projects/" + projectID + "/applications/" + applicationID +
			"/store-products/" + scenario.ProviderProductID, body: map[string]any{"product_id": productID}},
		{path: "/v1/admin/projects/" + projectID + "/applications/" + applicationID +
			"/credentials/" + credentialKind, body: map[string]any{
			"content_type": credentialContentType, "schema_version": 1,
			"expected_revision": 0, "payload": scenario.Credential,
		}},
		{path: "/v1/admin/projects/" + projectID + "/applications/" + applicationID + "/webhook", body: map[string]any{
			"url": "https://fixture:8443/webhook", "signing_secret": client.webhookSecret, "expected_revision": 0,
		}},
	}
	for _, mutation := range mutations {
		if _, err := client.request(ctx, http.MethodPut, client.apiBaseURL+mutation.path,
			adminKey, mutation.body, nil, http.StatusOK); err != nil {
			return err
		}
	}
	applicationKey, err := client.createApplicationKey(ctx)
	if err != nil {
		return err
	}
	customerSession, err := client.createCustomerSession(ctx, applicationKey, scenario.ExternalCustomerID)
	if err != nil {
		return err
	}
	verification := map[string]any{
		"external_customer_id": scenario.ExternalCustomerID,
		"claimed_products":     []string{scenario.ProviderProductID},
		"evidence":             scenario.Evidence,
	}
	for attempt := 0; attempt < 2; attempt++ {
		body, err := client.request(ctx, http.MethodPost,
			client.apiBaseURL+"/v1/applications/"+applicationID+"/purchases:verify",
			customerSession, verification, nil, http.StatusOK)
		if err != nil {
			return err
		}
		if err := assertVerification(body, "allowed", "purchase_valid", 1); err != nil {
			return err
		}
	}
	if err := client.waitForDeliveries(ctx, 1, 1); err != nil {
		return err
	}
	if err := client.assertEntitlement(ctx, customerSession, scenario.ExternalCustomerID,
		"allowed", "purchase_valid", 1); err != nil {
		return err
	}
	metrics, err := client.request(ctx, http.MethodGet, client.apiBaseURL+"/metrics", "", nil, nil, http.StatusOK)
	if err != nil {
		return err
	}
	if !bytes.Contains(metrics, []byte("iapstack_provider_calls_total")) ||
		!bytes.Contains(metrics, []byte("iapstack_verifications_total")) {
		return errors.New("API metrics omitted provider or verification counters")
	}
	_, _ = fmt.Fprintln(os.Stdout, "bootstrap phase passed")
	return nil
}

// enqueueNotification changes provider state and proves duplicate notifications commit while the worker is stopped.
func (client *gateClient) enqueueNotification(ctx context.Context) error {
	if _, err := client.request(ctx, http.MethodPut, client.fixtureBaseURL+"/state/refunded", "", nil, nil, http.StatusOK); err != nil {
		return err
	}
	scenario, err := client.scenario(ctx)
	if err != nil {
		return err
	}
	headers := map[string]string{"X-IAPStack-Project-ID": projectID}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := client.request(ctx, http.MethodPost,
			client.apiBaseURL+"/v1/providers/huawei/applications/"+applicationID+"/notifications",
			"", scenario.Notification, headers, http.StatusOK); err != nil {
			return err
		}
	}
	applicationKey, err := client.createApplicationKey(ctx)
	if err != nil {
		return err
	}
	customerSession, err := client.createCustomerSession(ctx, applicationKey, scenario.ExternalCustomerID)
	if err != nil {
		return err
	}
	if err := client.assertEntitlement(ctx, customerSession, scenario.ExternalCustomerID,
		"allowed", "purchase_valid", 1); err != nil {
		return fmt.Errorf("worker stopped projection changed unexpectedly: %w", err)
	}
	if err := client.assertDeliveries(ctx, 1, 1); err != nil {
		return fmt.Errorf("worker stopped webhook state changed unexpectedly: %w", err)
	}
	_, _ = fmt.Fprintln(os.Stdout, "notification enqueue phase passed")
	return nil
}

// assertRecovery waits for River processing after restart and verifies idempotent refund delivery.
func (client *gateClient) assertRecovery(ctx context.Context) error {
	scenario, err := client.scenario(ctx)
	if err != nil {
		return err
	}
	applicationKey, err := client.createApplicationKey(ctx)
	if err != nil {
		return err
	}
	customerSession, err := client.createCustomerSession(ctx, applicationKey, scenario.ExternalCustomerID)
	if err != nil {
		return err
	}
	if err := waitFor(ctx, func() (bool, error) {
		err := client.assertEntitlement(ctx, customerSession, scenario.ExternalCustomerID,
			"denied", "refunded", 2)
		return err == nil, nil
	}); err != nil {
		return fmt.Errorf("wait for refunded entitlement: %w", err)
	}
	if err := client.waitForDeliveries(ctx, 2, 2); err != nil {
		return err
	}
	headers := map[string]string{"X-IAPStack-Project-ID": projectID}
	if _, err := client.request(ctx, http.MethodPost,
		client.apiBaseURL+"/v1/providers/huawei/applications/"+applicationID+"/notifications",
		"", scenario.Notification, headers, http.StatusOK); err != nil {
		return err
	}
	timer := time.NewTimer(2 * time.Second)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
	}
	if err := client.assertDeliveries(ctx, 2, 2); err != nil {
		return fmt.Errorf("duplicate notification created another delivery: %w", err)
	}
	metrics, err := client.request(ctx, http.MethodGet, client.workerBaseURL+"/metrics", "", nil, nil, http.StatusOK)
	if err != nil {
		return err
	}
	for _, required := range [][]byte{
		[]byte("iapstack_queue_depth"), []byte("iapstack_queue_attempts_total"), []byte("iapstack_webhook_attempts_total"),
	} {
		if !bytes.Contains(metrics, required) {
			return fmt.Errorf("worker metrics omitted %s", required)
		}
	}
	_, _ = fmt.Fprintln(os.Stdout, "worker recovery phase passed")
	return nil
}

// scenario loads the fixture's current signed purchase and provider configuration.
func (client *gateClient) scenario(ctx context.Context) (fixtureScenario, error) {
	body, err := client.request(ctx, http.MethodGet, client.fixtureBaseURL+"/scenario", "", nil, nil, http.StatusOK)
	if err != nil {
		return fixtureScenario{}, err
	}
	var result fixtureScenario
	if err := json.Unmarshal(body, &result); err != nil {
		return fixtureScenario{}, fmt.Errorf("decode fixture scenario: %w", err)
	}
	if result.ProviderApplicationID == "" || result.ProviderProductID == "" || result.ExternalCustomerID == "" ||
		len(result.Credential) == 0 || len(result.Evidence) == 0 || len(result.Notification) == 0 {
		return fixtureScenario{}, errors.New("fixture scenario is incomplete")
	}
	return result, nil
}

// createApplicationKey creates one fresh application bearer without persisting it between gate phases.
func (client *gateClient) createApplicationKey(ctx context.Context) (string, error) {
	return client.createKey(ctx, client.bootstrapAdmin, map[string]any{
		"role": "application", "project_id": projectID, "application_id": applicationID,
	})
}

// createCustomerSession exchanges one trusted application key for a customer-bound bearer.
func (client *gateClient) createCustomerSession(
	ctx context.Context,
	applicationKey string,
	externalID string,
) (string, error) {
	body, err := client.request(
		ctx,
		http.MethodPost,
		client.apiBaseURL+"/v1/applications/"+applicationID+"/customer-sessions",
		applicationKey,
		map[string]any{"external_customer_id": externalID},
		nil,
		http.StatusCreated,
	)
	if err != nil {
		return "", err
	}
	var response customerSessionResponse
	if err := json.Unmarshal(body, &response); err != nil || response.Token == "" ||
		!response.ExpiresAt.After(time.Now().UTC()) {
		return "", errors.New("customer session response was invalid")
	}
	return response.Token, nil
}

// createKey calls the one-time key creation contract and returns the bearer only in memory.
func (client *gateClient) createKey(ctx context.Context, adminBearer string, input any) (string, error) {
	body, err := client.request(ctx, http.MethodPost, client.apiBaseURL+"/v1/admin/api-keys",
		adminBearer, input, nil, http.StatusCreated)
	if err != nil {
		return "", err
	}
	var response keyResponse
	if err := json.Unmarshal(body, &response); err != nil || response.Key == "" {
		return "", errors.New("API key response was invalid")
	}
	return response.Key, nil
}

// assertEntitlement loads and validates one exact customer entitlement snapshot.
func (client *gateClient) assertEntitlement(
	ctx context.Context,
	customerSession string,
	externalID string,
	access string,
	reason string,
	version int64,
) error {
	body, err := client.request(ctx, http.MethodGet,
		client.apiBaseURL+"/v1/applications/"+applicationID+"/customers/"+externalID+"/entitlements",
		customerSession, nil, nil, http.StatusOK)
	if err != nil {
		return err
	}
	var snapshot entitlementSnapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return fmt.Errorf("decode entitlement snapshot: %w", err)
	}
	if snapshot.CustomerID != customerID || len(snapshot.Entitlements) != 1 {
		return fmt.Errorf("entitlement snapshot identity or cardinality mismatch")
	}
	value := snapshot.Entitlements[0]
	if value.Key != entitlementKey || value.Access != access || value.Reason != reason || value.Version != version {
		return fmt.Errorf("entitlement = %#v, want %s/%s version %d", value, access, reason, version)
	}
	return nil
}

// waitForDeliveries waits until the fixture observes the exact expected webhook result.
func (client *gateClient) waitForDeliveries(ctx context.Context, attempts, unique int) error {
	return waitFor(ctx, func() (bool, error) {
		err := client.assertDeliveries(ctx, attempts, unique)
		return err == nil, nil
	})
}

// assertDeliveries validates exact attempt count, unique event count, and every HMAC signature.
func (client *gateClient) assertDeliveries(ctx context.Context, attempts, unique int) error {
	body, err := client.request(ctx, http.MethodGet, client.fixtureBaseURL+"/deliveries", "", nil, nil, http.StatusOK)
	if err != nil {
		return err
	}
	var report deliveryReport
	if err := json.Unmarshal(body, &report); err != nil {
		return fmt.Errorf("decode delivery report: %w", err)
	}
	if report.Attempts != attempts || report.UniqueEventIDs != unique || !report.AllSignaturesValid {
		return fmt.Errorf("deliveries = %#v, want %d attempts and %d valid unique events", report, attempts, unique)
	}
	return nil
}

// request performs one bounded JSON or probe request and accepts only the exact expected status.
func (client *gateClient) request(
	ctx context.Context,
	method string,
	url string,
	bearer string,
	input any,
	headers map[string]string,
	expectedStatus int,
) ([]byte, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("encode request for %s: %w", url, err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("construct request for %s: %w", url, err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute %s %s: %w", method, url, err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s %s response: %w", method, url, err)
	}
	if response.StatusCode != expectedStatus {
		return nil, fmt.Errorf("%s %s returned status %d, want %d", method, url, response.StatusCode, expectedStatus)
	}
	return responseBody, nil
}

// assertVerification validates one verification response without relying on internal identifiers.
func assertVerification(body []byte, access, reason string, version int64) error {
	var response struct {
		CustomerID   string        `json:"customer_id"`
		Entitlements []entitlement `json:"entitlements"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("decode verification response: %w", err)
	}
	if response.CustomerID != customerID || len(response.Entitlements) != 1 {
		return errors.New("verification response identity or cardinality mismatch")
	}
	value := response.Entitlements[0]
	if value.Key != entitlementKey || value.Access != access || value.Reason != reason || value.Version != version {
		return fmt.Errorf("verification entitlement = %#v, want %s/%s version %d", value, access, reason, version)
	}
	return nil
}

// waitFor polls one asynchronous assertion until success, failure, or its bounded deadline.
func waitFor(ctx context.Context, assertion func() (bool, error)) error {
	deadline := time.NewTimer(eventualTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(eventualInterval)
	defer ticker.Stop()
	for {
		ready, err := assertion()
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("eventual assertion timed out")
		case <-ticker.C:
		}
	}
}

// valueOrDefault returns one trimmed environment override or its stable local default.
func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
