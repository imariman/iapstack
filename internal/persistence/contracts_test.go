package persistence_test

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
)

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
