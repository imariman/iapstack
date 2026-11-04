package webhooks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/verification"
	"github.com/imariman/iapstack/internal/webhookreceiver"
)

// receiverContractStore records what the shipped receiver persists for each delivery.
type receiverContractStore struct {
	mutex  sync.Mutex
	events []webhookreceiver.Event
}

// TestDeliverEntitlementEventReachesShippedReceiver verifies a real outbox payload passes the shipped receiver.
func TestDeliverEntitlementEventReachesShippedReceiver(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	endsAt := now.Add(30 * 24 * time.Hour)
	storeEntitlement := persistence.CustomerEntitlement{
		Projection: persistence.EntitlementProjection{
			ProjectID: "project-1", CustomerID: "customer-1", EntitlementID: "entitlement-1",
			SourceKind: persistence.SourceKindStoreObservation, SourceObservationID: "observation-1",
			SourceProductID: "product-1", Access: core.AccessAllowed, AccessReason: core.AccessReasonPurchaseValid,
			EffectivePeriod: core.EffectivePeriod{StartsAt: now.Add(-time.Hour), EndsAt: &endsAt},
		},
		SourceApplicationID: "ios-sandbox",
		Key:                 "premium",
		Version:             3,
	}
	importEntitlement := storeEntitlement
	importEntitlement.Projection.SourceKind = persistence.SourceKindImport
	importEntitlement.Projection.SourceObservationID = ""
	importEntitlement.Projection.SourceImportID = "imp-55555555555555555555555555555555"
	// Core application ids allow inner spaces and non-ASCII text, with no 128-byte cap.
	wideApplicationID := core.ApplicationID("Café app " + strings.Repeat("é", 70))
	tests := []struct {
		name          string
		applicationID core.ApplicationID
		entitlement   persistence.CustomerEntitlement
	}{
		{name: "store observation", applicationID: "ios-sandbox", entitlement: storeEntitlement},
		{name: "import grant", applicationID: "ios-sandbox", entitlement: importEntitlement},
		{name: "core application id beyond printable ASCII", applicationID: wideApplicationID, entitlement: storeEntitlement},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if err := test.applicationID.Validate(); err != nil {
				t.Fatalf("application ID %q is not a core identifier: %v", test.applicationID, err)
			}
			event, err := verification.EntitlementChangedEvent(test.applicationID, test.entitlement, now)
			if err != nil {
				t.Fatalf("EntitlementChangedEvent() error = %v", err)
			}
			secret := bytes.Repeat([]byte("s"), testSigningSecretBytes)
			receiverStore := &receiverContractStore{}
			receiver, err := webhookreceiver.New(webhookreceiver.Config{
				Secret: secret, BodyLimit: 1 << 20, TimestampTolerance: 5 * time.Minute,
				Store:  receiverStore,
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				Clock:  func() time.Time { return now },
			})
			if err != nil {
				t.Fatalf("webhookreceiver.New() error = %v", err)
			}
			server := httptest.NewTLSServer(receiver)
			t.Cleanup(server.Close)

			protected := protection.Value{Ciphertext: []byte("ciphertext"), KeyID: "key-1"}
			protected.Fingerprint = sha256.Sum256(secret)
			service := &Service{
				store: &fakeOperationsStore{endpoint: persistence.WebhookEndpointRecord{
					ProjectID: event.ProjectID, ApplicationID: event.ApplicationID,
					URL: server.URL + webhookreceiver.WebhookPath, Secret: protected, Revision: 1,
					CreatedAt: now, UpdatedAt: now,
				}},
				protection: fakeProtection{secret: secret},
				client:     server.Client(), clock: func() time.Time { return now },
				allowPrivateNetworks: true,
			}
			message := persistence.QueueMessage{
				Queue: persistence.QueueOutbox, ID: event.ID, ProjectID: event.ProjectID,
				ApplicationID: event.ApplicationID, Kind: event.EventType,
				JSONPayload: event.Payload, OccurredAt: event.OccurredAt,
			}

			if err := service.Deliver(context.Background(), message); err != nil {
				t.Fatalf("Deliver() error = %v, want the shipped receiver to accept the real payload %s", err, event.Payload)
			}
			saved := receiverStore.saved()
			if len(saved) != 1 {
				t.Fatalf("receiver saved %d events, want 1", len(saved))
			}
			if saved[0].ID != event.ID || saved[0].ApplicationID != string(test.applicationID) ||
				saved[0].EventType != event.EventType || saved[0].BodyFingerprint != sha256.Sum256(event.Payload) {
				t.Fatalf("receiver saved %#v, want id %q application %q type %q", saved[0], event.ID, test.applicationID, event.EventType)
			}
		})
	}
}

// Ping reports that the in-memory receiver store is available.
func (store *receiverContractStore) Ping(context.Context) error {
	return nil
}

// Save records one authenticated event identity as newly inserted.
func (store *receiverContractStore) Save(_ context.Context, event webhookreceiver.Event) (webhookreceiver.SaveResult, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.events = append(store.events, event)
	return webhookreceiver.SaveInserted, nil
}

// Close is unused by the in-memory receiver store.
func (store *receiverContractStore) Close() {}

// saved returns a copy of the recorded events.
func (store *receiverContractStore) saved() []webhookreceiver.Event {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	return append([]webhookreceiver.Event(nil), store.events...)
}
