package webhookreceiver

import (
	"context"
	"crypto/sha256"
	"os"
	"testing"
	"time"
)

// TestPostgresStoreDeduplicatesAuthenticatedIdentity verifies durable replay and conflict semantics.
func TestPostgresStoreDeduplicatesAuthenticatedIdentity(t *testing.T) {
	databaseURL := os.Getenv("IAPSTACK_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("IAPSTACK_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	store, err := OpenPostgresStore(ctx, databaseURL)
	if err != nil {
		t.Fatalf("OpenPostgresStore() error = %v", err)
	}
	eventID := "receiver-postgres-deduplication-test"
	if _, err := store.pool.Exec(ctx, `DELETE FROM iapstack_webhook_receiver_events WHERE event_id = $1`, eventID); err != nil {
		t.Fatalf("delete fixture before test: %v", err)
	}
	t.Cleanup(func() {
		defer store.Close()
		if _, err := store.pool.Exec(ctx, `DELETE FROM iapstack_webhook_receiver_events WHERE event_id = $1`, eventID); err != nil {
			t.Errorf("delete fixture after test: %v", err)
		}
	})
	event := Event{
		ID: eventID, ApplicationID: "ios-sandbox", EventType: "entitlement.changed",
		BodyFingerprint: sha256.Sum256([]byte("first body")), ReceivedAt: time.Now().UTC(),
	}

	inserted, err := store.Save(ctx, event)
	if err != nil || inserted != SaveInserted {
		t.Fatalf("first Save() = (%q, %v), want (%q, nil)", inserted, err, SaveInserted)
	}
	duplicate, err := store.Save(ctx, event)
	if err != nil || duplicate != SaveDuplicate {
		t.Fatalf("duplicate Save() = (%q, %v), want (%q, nil)", duplicate, err, SaveDuplicate)
	}
	event.BodyFingerprint = sha256.Sum256([]byte("altered body"))
	conflict, err := store.Save(ctx, event)
	if err != nil || conflict != SaveConflict {
		t.Fatalf("conflicting Save() = (%q, %v), want (%q, nil)", conflict, err, SaveConflict)
	}
}
