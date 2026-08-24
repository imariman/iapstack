package persistence_test

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
)

const (
	// testKeyID identifies the deterministic protector key used by validation tests.
	testKeyID = "test-key"
)

// TestProtectedValueValidation verifies that encrypted persistence inputs are complete.
func TestProtectedValueValidation(t *testing.T) {
	t.Parallel()

	valid := protectedValue([]byte("payload"))
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name  string
		value persistence.ProtectedValue
	}{
		{name: "ciphertext", value: persistence.ProtectedValue{Fingerprint: valid.Fingerprint, KeyID: testKeyID}},
		{name: "fingerprint", value: persistence.ProtectedValue{Ciphertext: []byte("ciphertext"), KeyID: testKeyID}},
		{name: "key ID", value: persistence.ProtectedValue{Ciphertext: []byte("ciphertext"), Fingerprint: valid.Fingerprint}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.value.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want validation error")
			}
		})
	}
}

// TestOutboxEventValidation verifies JSON object and scheduling invariants.
func TestOutboxEventValidation(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	payload := json.RawMessage(`{"entitlement":"pro"}`)
	event := persistence.OutboxEvent{
		ID:                 "event-1",
		ProjectID:          "project-1",
		ApplicationID:      "application-1",
		EventType:          "entitlement.changed",
		AggregateType:      "customer",
		AggregateID:        "customer-1",
		Payload:            payload,
		PayloadFingerprint: sha256.Sum256(payload),
		OccurredAt:         now,
		AvailableAt:        now,
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	event.Payload = json.RawMessage(`["not-an-object"]`)
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want JSON object error")
	}
}

// protectedValue builds deterministic encrypted test metadata without representing production encryption.
func protectedValue(plaintext []byte) persistence.ProtectedValue {
	return persistence.ProtectedValue{
		Ciphertext:  append([]byte("ciphertext:"), plaintext...),
		Fingerprint: sha256.Sum256(plaintext),
		KeyID:       testKeyID,
	}
}
