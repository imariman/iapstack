package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

// fakeOperationsStore exposes one webhook endpoint to delivery tests.
type fakeOperationsStore struct {
	endpoint      persistence.WebhookEndpointRecord
	endpointError error
}

// fakeOperationsTransaction embeds unused operations and overrides webhook lookup.
type fakeOperationsTransaction struct {
	persistence.OperationsTransaction
	endpoint      persistence.WebhookEndpointRecord
	endpointError error
}

// fakeProtection opens one configured webhook signing secret.
type fakeProtection struct {
	secret    []byte
	openError error
}

// TestDeliverClassifiesDependencyFailures verifies only durable absence and invalid ciphertext are permanent.
func TestDeliverClassifiesDependencyFailures(t *testing.T) {
	protected := protection.Value{Ciphertext: []byte("ciphertext"), KeyID: "key-1"}
	protected.Fingerprint = sha256.Sum256([]byte("secret"))
	endpoint := persistence.WebhookEndpointRecord{
		ProjectID: "project-1", ApplicationID: "application-1", URL: "https://hooks.example.com/iapstack",
		Secret: protected, Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	message := persistence.QueueMessage{
		Queue: persistence.QueueOutbox, ID: "event-1", ProjectID: "project-1",
		ApplicationID: "application-1", JSONPayload: []byte(`{"event":"changed"}`),
	}
	tests := []struct {
		name          string
		endpointError error
		openError     error
		wantCode      string
		wantRetry     bool
	}{
		{name: "missing endpoint", endpointError: persistence.ErrNotFound, wantCode: "webhook_not_configured"},
		{name: "unavailable endpoint", endpointError: persistence.ErrUnavailable, wantCode: "webhook_endpoint_unavailable", wantRetry: true},
		{name: "unexpected endpoint load failure", endpointError: errors.New("load failed"), wantCode: "webhook_endpoint_unavailable", wantRetry: true},
		{name: "unavailable secret key", openError: protection.ErrKeyUnavailable, wantCode: "webhook_secret_unavailable", wantRetry: true},
		{name: "invalid secret ciphertext", openError: protection.ErrOpenFailed, wantCode: "webhook_secret_invalid"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			service := &Service{
				store:                &fakeOperationsStore{endpoint: endpoint, endpointError: test.endpointError},
				protection:           fakeProtection{secret: []byte("signing-secret"), openError: test.openError},
				allowPrivateNetworks: false,
			}
			err := service.Deliver(context.Background(), message)
			failure, ok := err.(*DeliveryError)
			if !ok || failure.CodeValue() != test.wantCode || failure.Retryable() != test.wantRetry {
				t.Fatalf("Deliver() error = %#v, want code %q retryable %t", err, test.wantCode, test.wantRetry)
			}
		})
	}
}

// fakeAddressResolver returns deterministic DNS answers to network policy tests.
type fakeAddressResolver struct {
	addresses []netip.Addr
}

// recordingConnectionDialer records whether an address passed policy enforcement.
type recordingConnectionDialer struct {
	calls int
}

// TestSignUsesTimestampDotRawBody verifies the stable v1 webhook signature contract.
func TestSignUsesTimestampDotRawBody(t *testing.T) {
	secret := []byte("application-signing-secret")
	timestamp := time.Unix(1_700_000_000, 0).UTC()
	body := []byte(`{"event":"entitlement.changed"}`)

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp.Unix(), 10) + "."))
	_, _ = mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if got := Sign(secret, timestamp, body); got != want {
		t.Fatalf("Sign() = %q, want %q", got, want)
	}
	if got := Sign(secret, timestamp, append(body, ' ')); got == want {
		t.Fatal("Sign() ignored a raw body change")
	}
}

// TestDeliverSignsAndClassifiesResponses verifies headers, success, retryable, and permanent status handling.
func TestDeliverSignsAndClassifiesResponses(t *testing.T) {
	status := http.StatusNoContent
	var receivedSignature, receivedEventID string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		receivedSignature = request.Header.Get("IAPStack-Signature")
		receivedEventID = request.Header.Get("IAPStack-Event-ID")
		writer.WriteHeader(status)
	}))
	defer server.Close()

	protected := protection.Value{Ciphertext: []byte("ciphertext"), KeyID: "key-1"}
	protected.Fingerprint = sha256.Sum256([]byte("secret"))
	store := &fakeOperationsStore{endpoint: persistence.WebhookEndpointRecord{
		ProjectID: "project-1", ApplicationID: "application-1", URL: server.URL,
		Secret: protected, Revision: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	service := &Service{
		store: store, protection: fakeProtection{secret: []byte("signing-secret")},
		client: server.Client(), clock: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		allowPrivateNetworks: true,
	}
	message := persistence.QueueMessage{
		Queue: persistence.QueueOutbox, ID: "event-1", ProjectID: "project-1",
		ApplicationID: "application-1", JSONPayload: []byte(`{"event":"changed"}`),
	}
	if err := service.Deliver(context.Background(), message); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if receivedEventID != message.ID || receivedSignature == "" {
		t.Fatalf("delivery headers = (%q, %q)", receivedEventID, receivedSignature)
	}
	status = http.StatusServiceUnavailable
	err := service.Deliver(context.Background(), message)
	failure, ok := err.(*DeliveryError)
	if !ok || !failure.Retryable() {
		t.Fatalf("503 Deliver() error = %#v, want retryable DeliveryError", err)
	}
	status = http.StatusBadRequest
	err = service.Deliver(context.Background(), message)
	failure, ok = err.(*DeliveryError)
	if !ok || failure.Retryable() {
		t.Fatalf("400 Deliver() error = %#v, want permanent DeliveryError", err)
	}
}

// TestWebhookAddressPolicyRejectsNonPublicRanges verifies the Internet-only destination boundary.
func TestWebhookAddressPolicyRejectsNonPublicRanges(t *testing.T) {
	tests := []struct {
		address string
		allowed bool
	}{
		{address: "8.8.8.8", allowed: true},
		{address: "2606:4700:4700::1111", allowed: true},
		{address: "127.0.0.1", allowed: false},
		{address: "::1", allowed: false},
		{address: "10.0.0.1", allowed: false},
		{address: "169.254.169.254", allowed: false},
		{address: "100.64.0.1", allowed: false},
		{address: "198.18.0.1", allowed: false},
		{address: "::ffff:127.0.0.1", allowed: false},
		{address: "ff02::1", allowed: false},
	}
	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			address := netip.MustParseAddr(tt.address)
			if got := isPublicWebhookAddress(address); got != tt.allowed {
				t.Fatalf("isPublicWebhookAddress(%q) = %t, want %t", tt.address, got, tt.allowed)
			}
		})
	}
}

// TestRestrictedDialerRejectsMixedDNSAnswers verifies one private answer blocks DNS-rebinding delivery.
func TestRestrictedDialerRejectsMixedDNSAnswers(t *testing.T) {
	connectionDialer := &recordingConnectionDialer{}
	dialer := &restrictedDialer{
		resolver: fakeAddressResolver{addresses: []netip.Addr{
			netip.MustParseAddr("8.8.8.8"),
			netip.MustParseAddr("127.0.0.1"),
		}},
		dialer: connectionDialer,
	}
	if _, err := dialer.DialContext(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatal("DialContext() mixed DNS error = nil, want policy rejection")
	}
	if connectionDialer.calls != 0 {
		t.Fatalf("DialContext() connection calls = %d, want 0", connectionDialer.calls)
	}
}

// TestWebhookDestinationPrivateNetworkOptIn verifies private destinations require an explicit policy override.
func TestWebhookDestinationPrivateNetworkOptIn(t *testing.T) {
	if err := validateWebhookDestination("https://127.0.0.1/hooks", false); err == nil {
		t.Fatal("validateWebhookDestination() default error = nil, want private-address rejection")
	}
	if err := validateWebhookDestination("https://127.0.0.1/hooks", true); err != nil {
		t.Fatalf("validateWebhookDestination() opt-in error = %v", err)
	}
}

// Operate executes one fake atomic operations callback.
func (store *fakeOperationsStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	return operation(&fakeOperationsTransaction{endpoint: store.endpoint, endpointError: store.endpointError})
}

// WebhookEndpoint returns the configured application endpoint.
func (transaction *fakeOperationsTransaction) WebhookEndpoint(
	_ context.Context,
	_ core.ProjectID,
	_ core.ApplicationID,
) (persistence.WebhookEndpointRecord, error) {
	if transaction.endpointError != nil {
		return persistence.WebhookEndpointRecord{}, transaction.endpointError
	}
	return transaction.endpoint, nil
}

// Protect is unused by delivery tests and returns one deterministic placeholder.
func (service fakeProtection) Protect(_ context.Context, _ protection.Request) (protection.Value, error) {
	value := protection.Value{Ciphertext: []byte("ciphertext"), KeyID: "key-1"}
	value.Fingerprint = sha256.Sum256(service.secret)
	return value, nil
}

// Open returns one defensive copy of the configured signing secret.
func (service fakeProtection) Open(_ context.Context, _ protection.OpenRequest) ([]byte, error) {
	if service.openError != nil {
		return nil, service.openError
	}
	return append([]byte(nil), service.secret...), nil
}

// LookupNetIP returns deterministic addresses without consulting external DNS.
func (resolver fakeAddressResolver) LookupNetIP(
	_ context.Context,
	_, _ string,
) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), resolver.addresses...), nil
}

// DialContext records a permitted dial attempt and returns a deterministic transport failure.
func (dialer *recordingConnectionDialer) DialContext(
	_ context.Context,
	_, _ string,
) (net.Conn, error) {
	dialer.calls++
	return nil, errors.New("test dial failure")
}
