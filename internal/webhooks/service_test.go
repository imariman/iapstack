package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

// fakeOperationsStore exposes one webhook endpoint to delivery tests.
type fakeOperationsStore struct {
	endpoint persistence.WebhookEndpointRecord
}

// fakeOperationsTransaction embeds unused operations and overrides webhook lookup.
type fakeOperationsTransaction struct {
	persistence.OperationsTransaction
	endpoint persistence.WebhookEndpointRecord
}

// fakeProtection opens one configured webhook signing secret.
type fakeProtection struct {
	secret []byte
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

// Operate executes one fake atomic operations callback.
func (store *fakeOperationsStore) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	return operation(&fakeOperationsTransaction{endpoint: store.endpoint})
}

// WebhookEndpoint returns the configured application endpoint.
func (transaction *fakeOperationsTransaction) WebhookEndpoint(
	_ context.Context,
	_ core.ProjectID,
	_ core.ApplicationID,
) (persistence.WebhookEndpointRecord, error) {
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
	return append([]byte(nil), service.secret...), nil
}
