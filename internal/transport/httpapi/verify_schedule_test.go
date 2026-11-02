package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/platform/metrics"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/verification"
)

const (
	// verifyScheduleSecret is a stand-in for a scheduling failure that must stay out of logs and responses.
	verifyScheduleSecret = "schedule-secret-do-not-log"
	// verifySchedulePurchaseToken is purchase evidence that must stay out of logs and responses.
	verifySchedulePurchaseToken = "purchase-token-do-not-log"
)

// scriptedPurchaseVerifier returns one fixed verification result without calling a provider.
type scriptedPurchaseVerifier struct {
	result   verification.Result
	err      error
	commands []verification.Command
}

// scheduleProtection records reconciliation protection attempts.
type scheduleProtection struct {
	err      error
	calls    int
	purposes []string
}

// reconciliationScheduleStore records or rejects one reconciliation job write.
type reconciliationScheduleStore struct {
	jobs  []persistence.ReconciliationJob
	err   error
	calls int
}

// reconciliationScheduleTransaction captures the job passed to the fake store.
type reconciliationScheduleTransaction struct {
	persistence.OperationsTransaction
	store *reconciliationScheduleStore
}

// verifyScheduleNow is the fixed clock used by scheduling tests.
var verifyScheduleNow = time.Date(2026, time.November, 1, 15, 4, 5, 0, time.UTC)

// TestVerifyReturnsSnapshotWhenReconciliationSchedulingFails verifies a committed purchase stays successful.
func TestVerifyReturnsSnapshotWhenReconciliationSchedulingFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		protectErr error
		saveErr    error
	}{
		{name: "protection", protectErr: errors.New(verifyScheduleSecret)},
		{name: "persistence", saveErr: errors.New(verifyScheduleSecret)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			api, verifier, protector, operations, logs := newVerifyScheduleAPI(nil, test.protectErr, test.saveErr)
			input := subscriptionVerificationRequest(verifySchedulePurchaseToken)
			principal := verifySchedulePrincipal()

			result, err := api.verify(context.Background(), principal, input)
			if err != nil {
				t.Fatalf("verify() error = %v, want committed snapshot", err)
			}
			if result.Customer.ID != verifier.result.Customer.ID || !result.VerifiedAt.Equal(verifier.result.VerifiedAt) {
				t.Fatalf("verify() result = %#v, want committed snapshot %#v", result, verifier.result)
			}
			again, err := api.verify(context.Background(), principal, input)
			if err != nil || again.Customer.ID != result.Customer.ID {
				t.Fatalf("second verify() = (%#v, %v), want the same snapshot", again, err)
			}
			if protector.calls != 2 || !containsString(protector.purposes, reconciliationProtectionPurpose) {
				t.Fatalf("protection calls = %d purposes %#v, want two reconciliation attempts", protector.calls, protector.purposes)
			}
			if test.saveErr == nil && operations.calls != 0 {
				t.Fatalf("operations calls = %d, want no job write when protection fails", operations.calls)
			}
			if test.saveErr != nil && operations.calls != 2 {
				t.Fatalf("operations calls = %d, want 2", operations.calls)
			}
			assertScheduleFailureLog(t, logs, verifier.result.Customer.ID)
			assertScheduleFailureMetric(t, api.metrics, 2)
		})
	}
}

// TestVerifySchedulesSubscriptionReconciliation verifies a successful verify still enqueues the daily refresh.
func TestVerifySchedulesSubscriptionReconciliation(t *testing.T) {
	t.Parallel()

	api, verifier, protector, operations, logs := newVerifyScheduleAPI(nil, nil, nil)
	result, err := api.verify(context.Background(), verifySchedulePrincipal(), subscriptionVerificationRequest("token-1"))
	if err != nil {
		t.Fatalf("verify() error = %v", err)
	}
	if result.Customer.ID != verifier.result.Customer.ID {
		t.Fatalf("verify() customer = %q, want %q", result.Customer.ID, verifier.result.Customer.ID)
	}
	if protector.calls != 1 || operations.calls != 1 || len(operations.jobs) != 1 {
		t.Fatalf("schedule calls = (protect %d, operate %d, jobs %d), want one job",
			protector.calls, operations.calls, len(operations.jobs))
	}
	job := operations.jobs[0]
	wantAvailable := time.Date(2026, time.November, 2, 0, 0, 0, 0, time.UTC)
	if job.CustomerID != verifier.result.Customer.ID || job.ProjectID != "project-1" || job.ApplicationID != "application-1" ||
		!job.AvailableAt.Equal(wantAvailable) || !strings.HasPrefix(job.ID, "reconcile-") {
		t.Fatalf("reconciliation job = %#v, want customer %q available %s", job, verifier.result.Customer.ID, wantAvailable)
	}
	if strings.Contains(logs.String(), "reconciliation_schedule_failed") {
		t.Fatalf("successful schedule logged a failure: %s", logs.String())
	}
	assertNoScheduleFailureMetric(t, api.metrics)
}

// TestVerifyLeavesNonSubscriptionAndVerificationErrorsUnscheduled verifies only a successful subscription verify is scheduled.
func TestVerifyLeavesNonSubscriptionAndVerificationErrorsUnscheduled(t *testing.T) {
	t.Parallel()

	api, _, protector, operations, _ := newVerifyScheduleAPI(nil, errors.New(verifyScheduleSecret), nil)
	input := subscriptionVerificationRequest(verifySchedulePurchaseToken)
	input.Evidence = json.RawMessage(`{"product_kind":"non_consumable","purchase_token":"` + verifySchedulePurchaseToken + `"}`)
	if _, err := api.verify(context.Background(), verifySchedulePrincipal(), input); err != nil {
		t.Fatalf("verify() non-subscription error = %v", err)
	}
	if protector.calls != 0 || operations.calls != 0 {
		t.Fatalf("non-subscription schedule calls = (protect %d, operate %d), want none", protector.calls, operations.calls)
	}

	failing, _, protector, operations, logs := newVerifyScheduleAPI(persistence.ErrUnavailable, nil, nil)
	_, err := failing.verify(context.Background(), verifySchedulePrincipal(), subscriptionVerificationRequest(verifySchedulePurchaseToken))
	if !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("verify() error = %v, want ErrUnavailable", err)
	}
	if protector.calls != 0 || operations.calls != 0 {
		t.Fatalf("failed verification schedule calls = (protect %d, operate %d), want none", protector.calls, operations.calls)
	}
	if strings.Contains(logs.String(), "reconciliation_schedule_failed") || strings.Contains(logs.String(), verifySchedulePurchaseToken) {
		t.Fatalf("failed verification log leaked schedule or evidence data: %s", logs.String())
	}
	assertNoScheduleFailureMetric(t, failing.metrics)
}

// TestPurchaseEndpointsReturnCommittedSnapshotsWhenSchedulingFails verifies verify and restore stay HTTP 200.
func TestPurchaseEndpointsReturnCommittedSnapshotsWhenSchedulingFails(t *testing.T) {
	t.Parallel()

	token, sessions := newVerifyScheduleCustomerSession(t)
	now := verifyScheduleNow

	t.Run("verify", func(t *testing.T) {
		t.Parallel()

		api, verifier, _, operations, logs := newVerifyScheduleAPI(nil, nil, errors.New(verifyScheduleSecret))
		api.customerSessions = sessions
		api.clock = func() time.Time { return now }
		recorder := httptest.NewRecorder()
		request := verifyScheduleRequest(
			http.MethodPost,
			"/v1/applications/application-1/purchases:verify",
			token,
			subscriptionPurchaseJSON(verifySchedulePurchaseToken, "subscription"),
		)
		api.verifyPurchase(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("verifyPurchase() status = %d, want 200: %s", recorder.Code, recorder.Body.String())
		}
		var response verificationResponse
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode verification response: %v", err)
		}
		if response.CustomerID != verifier.result.Customer.ID || len(response.Entitlements) != 1 ||
			response.Entitlements[0].Key != "pro" || response.Entitlements[0].Access != core.AccessAllowed {
			t.Fatalf("verification response = %#v", response)
		}
		if operations.calls != 1 {
			t.Fatalf("operations calls = %d, want 1", operations.calls)
		}
		assertNoPurchaseSecrets(t, recorder.Body.String())
		assertScheduleFailureLog(t, logs, verifier.result.Customer.ID)
		assertScheduleFailureMetric(t, api.metrics, 1)
	})

	t.Run("restore", func(t *testing.T) {
		t.Parallel()

		api, verifier, _, operations, _ := newVerifyScheduleAPI(nil, nil, errors.New(verifyScheduleSecret))
		api.customerSessions = sessions
		api.clock = func() time.Time { return now }
		body := `{"purchases":[` + subscriptionPurchaseJSON("token-a", "subscription") + `,` +
			subscriptionPurchaseJSON("token-b", "subscription") + `]}`
		recorder := httptest.NewRecorder()
		request := verifyScheduleRequest(
			http.MethodPost,
			"/v1/applications/application-1/purchases:restore",
			token,
			body,
		)
		api.restorePurchases(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("restorePurchases() status = %d, want 200: %s", recorder.Code, recorder.Body.String())
		}
		var response struct {
			Results []verificationResponse `json:"results"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode restore response: %v", err)
		}
		if len(response.Results) != 2 || len(verifier.commands) != 2 || operations.calls != 2 {
			t.Fatalf("restore results=%d verifies=%d schedules=%d, want 2",
				len(response.Results), len(verifier.commands), operations.calls)
		}
		for _, result := range response.Results {
			if result.CustomerID != verifier.result.Customer.ID {
				t.Fatalf("restore result = %#v, want customer %q", result, verifier.result.Customer.ID)
			}
		}
		assertNoPurchaseSecrets(t, recorder.Body.String())
		assertScheduleFailureMetric(t, api.metrics, 2)
	})

	t.Run("verification failure", func(t *testing.T) {
		t.Parallel()

		api, _, protector, operations, _ := newVerifyScheduleAPI(persistence.ErrUnavailable, nil, errors.New(verifyScheduleSecret))
		api.customerSessions = sessions
		recorder := httptest.NewRecorder()
		request := verifyScheduleRequest(
			http.MethodPost,
			"/v1/applications/application-1/purchases:verify",
			token,
			subscriptionPurchaseJSON(verifySchedulePurchaseToken, "subscription"),
		)
		api.verifyPurchase(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), `"code":"storage_unavailable"`) {
			t.Fatalf("verifyPurchase() status = %d body %s, want storage_unavailable", recorder.Code, recorder.Body.String())
		}
		if protector.calls != 0 || operations.calls != 0 {
			t.Fatalf("failed verification schedule calls = (protect %d, operate %d), want none", protector.calls, operations.calls)
		}
		assertNoPurchaseSecrets(t, recorder.Body.String())
		assertNoScheduleFailureMetric(t, api.metrics)
	})
}

// Verify records one scripted purchase verification.
func (verifier *scriptedPurchaseVerifier) Verify(
	_ context.Context,
	command verification.Command,
) (verification.Result, error) {
	verifier.commands = append(verifier.commands, command)
	if verifier.err != nil {
		return verification.Result{}, verifier.err
	}
	return verifier.result, nil
}

// Protect records the reconciliation scope and optionally fails before ciphertext is returned.
func (service *scheduleProtection) Protect(_ context.Context, request protection.Request) (protection.Value, error) {
	service.calls++
	service.purposes = append(service.purposes, request.Scope.Purpose)
	if service.err != nil {
		return protection.Value{}, service.err
	}
	value := protection.Value{Ciphertext: []byte("ciphertext"), KeyID: "key-1"}
	value.Fingerprint = sha256.Sum256([]byte("reconciliation-test"))
	return value, nil
}

// Open rejects unused reads in scheduling tests.
func (*scheduleProtection) Open(context.Context, protection.OpenRequest) ([]byte, error) {
	return nil, errors.New("unexpected open")
}

// Operate applies one reconciliation write or returns the configured storage failure.
func (store *reconciliationScheduleStore) Operate(_ context.Context, operation persistence.OperationsFunc) error {
	store.calls++
	if store.err != nil {
		return store.err
	}
	return operation(&reconciliationScheduleTransaction{store: store})
}

// SaveReconciliationJob records one validated reconciliation job.
func (transaction *reconciliationScheduleTransaction) SaveReconciliationJob(
	_ context.Context,
	job persistence.ReconciliationJob,
) (string, error) {
	if err := job.Validate(); err != nil {
		return "", err
	}
	transaction.store.jobs = append(transaction.store.jobs, job)
	return job.ID, nil
}

// newVerifyScheduleAPI builds one API whose verifier succeeds unless verifyErr is set.
func newVerifyScheduleAPI(verifyErr, protectErr, saveErr error) (
	*API,
	*scriptedPurchaseVerifier,
	*scheduleProtection,
	*reconciliationScheduleStore,
	*bytes.Buffer,
) {
	logs := &bytes.Buffer{}
	verifier := &scriptedPurchaseVerifier{result: verifyScheduleResult(), err: verifyErr}
	protector := &scheduleProtection{err: protectErr}
	operations := &reconciliationScheduleStore{err: saveErr}
	api := &API{
		store:        &webhookAPIStore{application: webhookAPIApplication()},
		operations:   operations,
		verification: verifier,
		protection:   protector,
		metrics:      metrics.New(),
		bodyLimit:    1 << 20,
		clock:        func() time.Time { return verifyScheduleNow },
		logger:       slog.New(slog.NewJSONHandler(logs, nil)),
	}
	return api, verifier, protector, operations, logs
}

// newVerifyScheduleCustomerSession mints one application-scoped customer bearer.
func newVerifyScheduleCustomerSession(t *testing.T) (string, *auth.CustomerSessions) {
	t.Helper()

	store := &keyLifecycleStore{
		records: make(map[string]persistence.APIKeyRecord), revokedAt: make(map[string]time.Time),
	}
	authentication, err := auth.NewService(store, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	applicationKey, err := authentication.Create(context.Background(), auth.Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-1", ApplicationID: "application-1",
	})
	if err != nil {
		t.Fatalf("Create() application key error = %v", err)
	}
	issuer, err := authentication.Authenticate(context.Background(), applicationKey)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	sessions, err := auth.NewCustomerSessions(store, newCustomerSessionTestKeyring(t))
	if err != nil {
		t.Fatalf("NewCustomerSessions() error = %v", err)
	}
	session, err := sessions.Mint(context.Background(), issuer, "customer-external")
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	return session.Token, sessions
}

// verifyScheduleResult is the snapshot a committed verification returns to the client.
func verifyScheduleResult() verification.Result {
	return verification.Result{
		VerifiedAt: verifyScheduleNow,
		Customer: core.Customer{
			ID: "customer-1", ProjectID: "project-1", ExternalID: "customer-external",
		},
		Entitlements: []persistence.CustomerEntitlement{{
			Key: "pro", Version: 3,
			Projection: persistence.EntitlementProjection{
				Access: core.AccessAllowed, AccessReason: core.AccessReasonPurchaseValid,
			},
		}},
	}
}

// verifySchedulePrincipal is the customer session scope used by direct verify calls.
func verifySchedulePrincipal() auth.Principal {
	return auth.Principal{
		Role: persistence.APIKeyRoleApplication, ProjectID: "project-1", ApplicationID: "application-1",
	}
}

// subscriptionVerificationRequest builds one subscription purchase body.
func subscriptionVerificationRequest(token string) verificationRequest {
	return verificationRequest{
		ExternalCustomerID: "customer-external",
		ClaimedProducts:    []core.ProviderProductID{"pro_monthly"},
		Evidence: json.RawMessage(fmt.Sprintf(
			`{"product_kind":"subscription","purchase_token":%q}`, token,
		)),
	}
}

// subscriptionPurchaseJSON builds one public purchase object.
func subscriptionPurchaseJSON(token, kind string) string {
	return fmt.Sprintf(
		`{"external_customer_id":"customer-external","claimed_products":["pro_monthly"],"evidence":{"product_kind":%q,"purchase_token":%q}}`,
		kind, token,
	)
}

// verifyScheduleRequest builds one authenticated JSON purchase request.
func verifyScheduleRequest(method, path, token, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.SetPathValue("application_id", "application-1")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	return request
}

// assertScheduleFailureLog checks the safe scheduling record and rejects evidence or backend secrets.
func assertScheduleFailureLog(t *testing.T, logs *bytes.Buffer, customerID core.CustomerID) {
	t.Helper()

	text := logs.String()
	if !strings.Contains(text, `"error_code":"reconciliation_schedule_failed"`) ||
		!strings.Contains(text, `"customer_id":"`+string(customerID)+`"`) ||
		!strings.Contains(text, `"project_id":"project-1"`) {
		t.Fatalf("schedule log = %s, want safe reconciliation_schedule_failed record", text)
	}
	assertNoPurchaseSecrets(t, text)
}

// assertScheduleFailureMetric checks the bounded reconciliation schedule counter.
func assertScheduleFailureMetric(t *testing.T, registry *metrics.Registry, count int) {
	t.Helper()

	text := scheduleMetricText(t, registry)
	want := fmt.Sprintf(
		`iapstack_verifications_total{provider="google_play",outcome="reconciliation_schedule_failed"} %d`,
		count,
	)
	if !strings.Contains(text, want) {
		t.Fatalf("metrics = %s, want %s", text, want)
	}
}

// assertNoScheduleFailureMetric verifies a successful or rejected verify did not count a missed schedule.
func assertNoScheduleFailureMetric(t *testing.T, registry *metrics.Registry) {
	t.Helper()

	text := scheduleMetricText(t, registry)
	if strings.Contains(text, `outcome="reconciliation_schedule_failed"`) {
		t.Fatalf("metrics counted a schedule failure\n%s", text)
	}
}

// scheduleMetricText renders one registry snapshot for schedule-failure assertions.
func scheduleMetricText(t *testing.T, registry *metrics.Registry) string {
	t.Helper()

	var output bytes.Buffer
	if err := registry.WritePrometheus(&output); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	return output.String()
}

// assertNoPurchaseSecrets rejects evidence and scheduling secrets in one observable string.
func assertNoPurchaseSecrets(t *testing.T, text string) {
	t.Helper()

	for _, secret := range []string{verifyScheduleSecret, verifySchedulePurchaseToken} {
		if strings.Contains(text, secret) {
			t.Fatalf("output contains %q", secret)
		}
	}
}
