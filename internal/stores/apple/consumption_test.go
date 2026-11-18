package apple

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/stores"
)

const (
	// fixtureConsumptionTransactionID is the transaction identifier a CONSUMPTION_REQUEST names.
	fixtureConsumptionTransactionID = "2000000923456790"
)

// fakeConsumptionEndpoint records requests to a local Send Consumption Information double.
type fakeConsumptionEndpoint struct {
	mu       sync.Mutex
	status   int
	body     string
	header   http.Header
	requests []recordedConsumptionRequest
}

// recordedConsumptionRequest is one request the fake App Store Server API received.
type recordedConsumptionRequest struct {
	method        string
	path          string
	contentType   string
	authorization string
	body          []byte
}

// TestBuildConsumptionRequestGatesConsentAndDelivery verifies the builder never fills missing facts.
func TestBuildConsumptionRequestGatesConsentAndDelivery(t *testing.T) {
	t.Parallel()

	if _, err := BuildConsumptionRequest(ConsumptionInput{
		ProductType: ConsumptionProductNonConsumable, Delivered: true, RefundPreference: policyRefundNone,
	}); !errors.Is(err, ErrConsumptionConsentRequired) {
		t.Fatalf("BuildConsumptionRequest(no consent) error = %v", err)
	}
	if _, err := BuildConsumptionRequest(ConsumptionInput{
		ProductType: ConsumptionProductNonConsumable, CustomerConsented: true, RefundPreference: policyRefundNone,
	}); !errors.Is(err, ErrConsumptionDeliveryUnknown) {
		t.Fatalf("BuildConsumptionRequest(no delivery) error = %v", err)
	}
	if _, err := BuildConsumptionRequest(ConsumptionInput{
		ProductType: "bundle", CustomerConsented: true, Delivered: true, RefundPreference: policyRefundNone,
	}); err == nil {
		t.Fatal("BuildConsumptionRequest(unknown product type) error = nil")
	}
	if _, err := BuildConsumptionRequest(ConsumptionInput{
		ProductType: ConsumptionProductNonConsumable, CustomerConsented: true, Delivered: true, RefundPreference: "always",
	}); err == nil {
		t.Fatal("BuildConsumptionRequest(unknown policy) error = nil")
	}
}

// TestBuildConsumptionRequestMapsRefundPreference verifies Apple's per-product refundPreference rules.
func TestBuildConsumptionRequestMapsRefundPreference(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		productType string
		policy      string
		sample      bool
		want        string
	}{
		{name: "no preference", productType: ConsumptionProductNonConsumable, policy: policyRefundNone, want: `{"customerConsented":true,"deliveryStatus":"DELIVERED","sampleContentProvided":false}`},
		{name: "decline", productType: ConsumptionProductConsumable, policy: policyRefundDecline, sample: true, want: `{"customerConsented":true,"deliveryStatus":"DELIVERED","refundPreference":"DECLINE","sampleContentProvided":true}`},
		{name: "grant full", productType: ConsumptionProductNonRenewing, policy: policyRefundGrantFull, want: `{"customerConsented":true,"deliveryStatus":"DELIVERED","refundPreference":"GRANT_FULL","sampleContentProvided":false}`},
		{name: "prorated subscription", productType: ConsumptionProductAutoRenewable, policy: policyRefundGrantProrated, want: `{"customerConsented":true,"deliveryStatus":"DELIVERED","refundPreference":"GRANT_PRORATED","sampleContentProvided":false}`},
		{name: "prorated lifetime omitted", productType: ConsumptionProductNonConsumable, policy: policyRefundGrantProrated, want: `{"customerConsented":true,"deliveryStatus":"DELIVERED","sampleContentProvided":false}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			request, err := BuildConsumptionRequest(ConsumptionInput{
				ProductType: test.productType, CustomerConsented: true, Delivered: true,
				RefundPreference: test.policy, SampleContentProvided: test.sample,
			})
			if err != nil {
				t.Fatalf("BuildConsumptionRequest() error = %v", err)
			}
			encoded, err := json.Marshal(request)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(encoded) != test.want {
				t.Fatalf("request body = %s, want %s", encoded, test.want)
			}
			if strings.Contains(string(encoded), "consumptionPercentage") {
				t.Fatal("request body must not contain consumptionPercentage")
			}
		})
	}
}

// TestConsumptionRequestValidateRejectsMissingConsent verifies Apple's customerConsented rule is local too.
func TestConsumptionRequestValidateRejectsMissingConsent(t *testing.T) {
	t.Parallel()

	for _, request := range []ConsumptionRequest{
		{DeliveryStatus: DeliveryStatusDelivered},
		{CustomerConsented: true, DeliveryStatus: "UNKNOWN"},
		{CustomerConsented: true, DeliveryStatus: DeliveryStatusDelivered, RefundPreference: "MAYBE"},
	} {
		if err := request.Validate(); err == nil {
			t.Fatalf("Validate(%#v) error = nil", request)
		}
	}
	if err := (ConsumptionRequest{CustomerConsented: true, DeliveryStatus: "UNDELIVERED_SERVER_OUTAGE"}).Validate(); err != nil {
		t.Fatalf("Validate(undelivered) error = %v", err)
	}
}

// TestSendConsumptionInformationCallsVersionTwoEndpoint verifies method, path, JWT, and exact body.
func TestSendConsumptionInformationCallsVersionTwoEndpoint(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 9, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	endpoint := &fakeConsumptionEndpoint{status: http.StatusAccepted}
	adapter := consumptionAdapterFixture(t, fixture, now, endpoint, core.EnvironmentSandbox)
	request, err := BuildConsumptionRequest(ConsumptionInput{
		ProductType: ConsumptionProductAutoRenewable, CustomerConsented: true, Delivered: true,
		RefundPreference: policyRefundDecline,
	})
	if err != nil {
		t.Fatalf("BuildConsumptionRequest() error = %v", err)
	}

	response, err := adapter.SendConsumptionInformation(
		context.Background(), appleApplication(core.EnvironmentSandbox), fixtureConsumptionTransactionID, request,
	)
	if err != nil || response.HTTPStatus != http.StatusAccepted {
		t.Fatalf("SendConsumptionInformation() = (%#v, %v)", response, err)
	}
	recorded := endpoint.only(t)
	if recorded.method != http.MethodPut || recorded.path != "/inApps/v2/transactions/consumption/"+fixtureConsumptionTransactionID {
		t.Fatalf("request = %s %s", recorded.method, recorded.path)
	}
	if recorded.contentType != "application/json" {
		t.Fatalf("Content-Type = %q", recorded.contentType)
	}
	verifyAuthorizationFixture(t, strings.TrimPrefix(recorded.authorization, "Bearer "), &fixture.apiKey.PublicKey, now)
	if got := string(recorded.body); got != `{"customerConsented":true,"deliveryStatus":"DELIVERED","refundPreference":"DECLINE","sampleContentProvided":false}` {
		t.Fatalf("request body = %s", got)
	}
}

// TestSendConsumptionInformationClassifiesAppleResponses verifies retry classes and Apple error codes.
func TestSendConsumptionInformationClassifiesAppleResponses(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 10, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	request := ConsumptionRequest{CustomerConsented: true, DeliveryStatus: DeliveryStatusDelivered}
	for _, test := range []struct {
		name      string
		status    int
		body      string
		header    http.Header
		kind      stores.FailureKind
		errorCode int64
		retryable bool
		after     time.Duration
	}{
		{name: "invalid", status: http.StatusBadRequest, body: `{"errorCode":4000006,"errorMessage":"Invalid transaction id."}`, kind: stores.FailurePermanent, errorCode: 4000006},
		{name: "unauthorized", status: http.StatusUnauthorized, kind: stores.FailureUnauthorized},
		{name: "not found", status: http.StatusNotFound, body: `{"errorCode":4040010}`, kind: stores.FailureNotFound, errorCode: 4040010},
		{name: "rate limited", status: http.StatusTooManyRequests, header: http.Header{"Retry-After": []string{"30"}}, kind: stores.FailureRateLimited, retryable: true, after: 30 * time.Second},
		{name: "server", status: http.StatusInternalServerError, body: `{"errorCode":5000000}`, kind: stores.FailureTemporary, errorCode: 5000000, retryable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			endpoint := &fakeConsumptionEndpoint{status: test.status, body: test.body, header: test.header}
			adapter := consumptionAdapterFixture(t, fixture, now, endpoint, core.EnvironmentSandbox)
			response, err := adapter.SendConsumptionInformation(
				context.Background(), appleApplication(core.EnvironmentSandbox), fixtureConsumptionTransactionID, request,
			)
			var failure *stores.Failure
			if !errors.As(err, &failure) || failure.Kind != test.kind || failure.Retryable() != test.retryable ||
				failure.RetryAfter != test.after {
				t.Fatalf("SendConsumptionInformation() error = %#v", err)
			}
			if response.HTTPStatus != test.status || response.AppleErrorCode != test.errorCode {
				t.Fatalf("SendConsumptionInformation() response = %#v", response)
			}
			if strings.Contains(err.Error(), "Invalid transaction") {
				t.Fatalf("error leaks the Apple message: %v", err)
			}
		})
	}
}

// TestSendConsumptionInformationRejectsUnsafeInputBeforeCallingApple verifies local guards.
func TestSendConsumptionInformationRejectsUnsafeInputBeforeCallingApple(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 11, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	endpoint := &fakeConsumptionEndpoint{status: http.StatusAccepted}
	adapter := consumptionAdapterFixture(t, fixture, now, endpoint, core.EnvironmentSandbox)
	valid := ConsumptionRequest{CustomerConsented: true, DeliveryStatus: DeliveryStatusDelivered}
	for _, test := range []struct {
		name          string
		application   core.Application
		transactionID string
		request       ConsumptionRequest
	}{
		{name: "no consent", application: appleApplication(core.EnvironmentSandbox), transactionID: fixtureConsumptionTransactionID, request: ConsumptionRequest{DeliveryStatus: DeliveryStatusDelivered}},
		{name: "empty transaction", application: appleApplication(core.EnvironmentSandbox), request: valid},
		{name: "oversized transaction", application: appleApplication(core.EnvironmentSandbox), transactionID: strings.Repeat("1", 65), request: valid},
		{name: "other provider", application: core.Application{
			ID: "application-1", ProjectID: "project-1",
			Store: core.StoreApplication{Provider: core.ProviderGooglePlay, Environment: core.EnvironmentTest, ID: fixtureBundleID},
		}, transactionID: fixtureConsumptionTransactionID, request: valid},
	} {
		if _, err := adapter.SendConsumptionInformation(context.Background(), test.application, test.transactionID, test.request); !isFailureKind(err, stores.FailurePermanent) {
			t.Fatalf("%s: SendConsumptionInformation() error = %v", test.name, err)
		}
	}
	if count := endpoint.count(); count != 0 {
		t.Fatalf("Apple received %d requests, want 0", count)
	}
}

// TestValidateNotificationExtractsConsumptionRequest verifies the consumption signal for every product type.
func TestValidateNotificationExtractsConsumptionRequest(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	adapter, err := New(fakeCredentialSource{credential: credentialFixture(t, fixture, core.EnvironmentSandbox)}, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.clock = func() time.Time { return now }
	for _, test := range []struct {
		name         string
		appleType    string
		ownership    string
		reason       string
		productType  string
		wantOwner    string
		wantReason   string
		verification bool
	}{
		{name: "subscription", appleType: appleSubscriptionType, ownership: applePurchasedOwnership, reason: "UNINTENDED_PURCHASE", productType: ConsumptionProductAutoRenewable, wantOwner: ConsumptionOwnershipPurchased, wantReason: "unintended_purchase", verification: true},
		{name: "consumable", appleType: appleConsumableType, ownership: applePurchasedOwnership, reason: "LEGAL", productType: ConsumptionProductConsumable, wantOwner: ConsumptionOwnershipPurchased, wantReason: "legal"},
		{name: "family shared", appleType: appleNonConsumableType, ownership: appleFamilySharedOwnership, productType: ConsumptionProductNonConsumable, wantOwner: ConsumptionOwnershipFamilyShared, verification: true},
		{name: "unknown reason", appleType: appleNonRenewingType, ownership: "ASSIGNED", reason: "NEW_APPLE_REASON", productType: ConsumptionProductNonRenewing, wantOwner: ConsumptionOwnershipOther},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			transaction := notificationTransaction(now)
			transaction.TransactionID = fixtureConsumptionTransactionID
			transaction.Type = test.appleType
			transaction.OwnershipType = test.ownership
			if test.appleType != appleSubscriptionType {
				transaction.ExpiresDate = 0
			}
			payload := notificationRequestFixture(t, fixture, notificationPayload{
				NotificationType: NotificationTypeConsumptionRequest, NotificationUUID: fixtureNotificationUUID,
				Version: notificationVersion, SignedDate: now.UnixMilli(),
				Data: &notificationData{
					BundleID: fixtureBundleID, Environment: "Sandbox",
					SignedTransactionInfo: signTransactionFixture(t, fixture, transaction), ConsumptionRequestReason: test.reason,
				},
			})
			notification, err := adapter.ValidateNotification(context.Background(), appleApplication(core.EnvironmentSandbox), payload)
			if err != nil {
				t.Fatalf("ValidateNotification() error = %v", err)
			}
			signal := notification.Consumption
			if signal == nil || signal.TransactionID != fixtureConsumptionTransactionID || signal.ProductType != test.productType ||
				signal.ProviderProductID != fixtureProductID || signal.ExternalCustomerID != fixtureAccountToken ||
				signal.Ownership != test.wantOwner || signal.CustomerReason != test.wantReason {
				t.Fatalf("Consumption = %#v", signal)
			}
			if !notification.SignedAt.Equal(now) {
				t.Fatalf("SignedAt = %s, want %s", notification.SignedAt, now)
			}
			if _, process, _ := notification.VerificationEvidence(); process != test.verification {
				t.Fatalf("VerificationEvidence() process = %t, want %t", process, test.verification)
			}
			encoded, err := json.Marshal(notification)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			var decoded NotificationEnvelope
			if err := decodeStrict(encoded, &decoded); err != nil || decoded.Validate() != nil || *decoded.Consumption != *signal {
				t.Fatalf("round trip = (%#v, %v)", decoded.Consumption, err)
			}
		})
	}
}

// TestValidateNotificationKeepsUnanswerableConsumptionRequest verifies an unknown product type is still accepted.
func TestValidateNotificationKeepsUnanswerableConsumptionRequest(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 18, 12, 30, 0, 0, time.UTC)
	fixture := newSigningFixture(t, now)
	adapter, err := New(fakeCredentialSource{credential: credentialFixture(t, fixture, core.EnvironmentSandbox)}, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.clock = func() time.Time { return now }
	transaction := notificationTransaction(now)
	transaction.Type = "Future Product Type"
	payload := notificationRequestFixture(t, fixture, notificationPayload{
		NotificationType: NotificationTypeConsumptionRequest, NotificationUUID: fixtureNotificationUUID,
		Version: notificationVersion, SignedDate: now.UnixMilli(),
		Data: &notificationData{
			BundleID: fixtureBundleID, Environment: "Sandbox", SignedTransactionInfo: signTransactionFixture(t, fixture, transaction),
		},
	})
	notification, err := adapter.ValidateNotification(context.Background(), appleApplication(core.EnvironmentSandbox), payload)
	if err != nil || notification.Consumption != nil {
		t.Fatalf("ValidateNotification() = (%#v, %v), want an accepted notification without consumption data", notification, err)
	}
}

// TestNotificationEnvelopeRejectsMisplacedConsumption verifies consumption data needs its notification type.
func TestNotificationEnvelopeRejectsMisplacedConsumption(t *testing.T) {
	t.Parallel()

	signal := ConsumptionSignal{
		TransactionID: fixtureConsumptionTransactionID, ProductType: ConsumptionProductConsumable,
		ProviderProductID: fixtureProductID, ExternalCustomerID: fixtureAccountToken,
		Ownership: ConsumptionOwnershipPurchased,
	}
	envelope := NotificationEnvelope{
		NotificationUUID: fixtureNotificationUUID, NotificationType: "REFUND",
		SignedAt: time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC), Consumption: &signal,
	}
	if err := envelope.Validate(); err == nil {
		t.Fatal("Validate(REFUND with consumption) error = nil")
	}
	envelope.NotificationType = NotificationTypeConsumptionRequest
	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate(CONSUMPTION_REQUEST) error = %v", err)
	}
	signal.CustomerReason = "angry"
	if err := envelope.Validate(); err == nil {
		t.Fatal("Validate(unknown stored reason) error = nil")
	}
}

// ServeHTTP records one request and replies with the configured Apple response.
func (endpoint *fakeConsumptionEndpoint) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	endpoint.mu.Lock()
	endpoint.requests = append(endpoint.requests, recordedConsumptionRequest{
		method: request.Method, path: request.URL.Path, contentType: request.Header.Get("Content-Type"),
		authorization: request.Header.Get("Authorization"), body: body,
	})
	endpoint.mu.Unlock()
	for key, values := range endpoint.header {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	if endpoint.body != "" {
		writer.Header().Set("Content-Type", "application/json")
	}
	writer.WriteHeader(endpoint.status)
	_, _ = io.WriteString(writer, endpoint.body)
}

// only returns the single recorded request or fails the test.
func (endpoint *fakeConsumptionEndpoint) only(t *testing.T) recordedConsumptionRequest {
	t.Helper()
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	if len(endpoint.requests) != 1 {
		t.Fatalf("Apple received %d requests, want 1", len(endpoint.requests))
	}
	return endpoint.requests[0]
}

// count returns how many requests the fake endpoint received.
func (endpoint *fakeConsumptionEndpoint) count() int {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	return len(endpoint.requests)
}

// consumptionAdapterFixture points an adapter at a local httptest server instead of Apple.
func consumptionAdapterFixture(
	t *testing.T,
	fixture signingFixture,
	now time.Time,
	endpoint *fakeConsumptionEndpoint,
	environment core.Environment,
) *Adapter {
	t.Helper()
	server := httptest.NewServer(endpoint)
	t.Cleanup(server.Close)
	adapter, err := New(fakeCredentialSource{credential: credentialFixture(t, fixture, environment)}, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	adapter.clock = func() time.Time { return now }
	adapter.sandboxURL = server.URL
	adapter.productionURL = server.URL
	return adapter
}
