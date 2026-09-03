package googleplay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/imariman/iapstack/internal/stores"
)

// TestWithRTDNPreservesServiceAccountMaterial verifies RTDN-only updates do not replace verifier secrets.
func TestWithRTDNPreservesServiceAccountMaterial(t *testing.T) {
	t.Parallel()
	originalPayload := []byte(`{"client_email":"verifier@example.iam.gserviceaccount.com","private_key_id":"key-id","private_key":"private-material"}`)
	original, err := stores.NewCredential(CredentialKind, CredentialContentType, CredentialSchemaVersion, originalPayload)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}

	updated, err := WithRTDN(
		original,
		"projects/example/subscriptions/iapstack-rtdn",
		"push@example.iam.gserviceaccount.com",
		"https://iapstack.example/v1/providers/google-play/projects/project/applications/app/notifications",
	)
	if err != nil {
		t.Fatalf("WithRTDN() error = %v", err)
	}
	var payload struct {
		ClientEmail  string            `json:"client_email"`
		PrivateKeyID string            `json:"private_key_id"`
		PrivateKey   string            `json:"private_key"`
		RTDN         rtdnConfiguration `json:"rtdn"`
	}
	if err := json.Unmarshal(updated.Bytes(), &payload); err != nil {
		t.Fatalf("decode updated credential: %v", err)
	}
	if payload.ClientEmail != "verifier@example.iam.gserviceaccount.com" ||
		payload.PrivateKeyID != "key-id" || payload.PrivateKey != "private-material" {
		t.Fatalf("updated credential did not preserve service-account fields")
	}
	if payload.RTDN.Subscription != "projects/example/subscriptions/iapstack-rtdn" ||
		payload.RTDN.PushServiceAccountEmail != "push@example.iam.gserviceaccount.com" {
		t.Fatalf("updated RTDN configuration = %#v", payload.RTDN)
	}
	if strings.Contains(string(original.Bytes()), `"rtdn"`) {
		t.Fatal("WithRTDN() mutated the original credential")
	}
}

// TestWithRTDNRejectsInvalidScope verifies malformed Pub/Sub identities are rejected before rotation.
func TestWithRTDNRejectsInvalidScope(t *testing.T) {
	t.Parallel()
	credential, err := stores.NewCredential(
		CredentialKind, CredentialContentType, CredentialSchemaVersion,
		[]byte(`{"client_email":"verifier@example.com","private_key_id":"key-id","private_key":"private-material"}`),
	)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	_, err = WithRTDN(credential, "invalid", "push@example.com", "https://iapstack.example/notifications")
	if err == nil || !strings.Contains(err.Error(), "subscription") {
		t.Fatalf("WithRTDN() error = %v, want invalid subscription", err)
	}
}
