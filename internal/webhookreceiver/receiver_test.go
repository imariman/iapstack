package webhookreceiver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/webhooks"
)

const (
	// defaultTestBodyLimit is the unit-test request body allowance.
	defaultTestBodyLimit int64 = 1 << 10
	// testSecret is a non-production HMAC fixture with the required entropy length.
	testSecret = "0123456789abcdef0123456789abcdef"
)

// fakeStore records authenticated events and supplies deterministic persistence outcomes.
type fakeStore struct {
	events     []Event
	saveResult SaveResult
	saveError  error
	pingError  error
	closed     bool
}

// recordingStore persists authenticated identities with production duplicate semantics.
type recordingStore struct {
	events   map[string]Event
	inserted int
}

// Ping returns the configured readiness result.
func (store *fakeStore) Ping(context.Context) error {
	return store.pingError
}

// Save records one authenticated event before returning the configured result.
func (store *fakeStore) Save(_ context.Context, event Event) (SaveResult, error) {
	store.events = append(store.events, event)
	return store.saveResult, store.saveError
}

// Close records resource cleanup.
func (store *fakeStore) Close() {
	store.closed = true
}

// Ping reports that the recording store is available.
func (store *recordingStore) Ping(context.Context) error {
	return nil
}

// Save inserts or compares one authenticated event identity.
func (store *recordingStore) Save(_ context.Context, event Event) (SaveResult, error) {
	if existing, ok := store.events[event.ID]; ok {
		if existing.BodyFingerprint == event.BodyFingerprint {
			return SaveDuplicate, nil
		}
		return SaveConflict, nil
	}
	store.events[event.ID] = event
	store.inserted++
	return SaveInserted, nil
}

// Close is unused by the recording store fixture.
func (store *recordingStore) Close() {}

// TestReceiverAcceptsAuthenticatedEvent verifies exact signature validation and safe persistence.
func TestReceiverAcceptsAuthenticatedEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{saveResult: SaveInserted}
	receiver := newTestReceiver(t, store, now, defaultTestBodyLimit)
	body := []byte(`{"schema_version":1,"application_id":"ios-sandbox","event_type":"entitlement.changed","customer_id":"not-retained"}`)
	request := signedRequest(t, now, body, "event-1")
	recorder := httptest.NewRecorder()

	receiver.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	if len(store.events) != 1 {
		t.Fatalf("saved events = %d, want 1", len(store.events))
	}
	event := store.events[0]
	if event.ID != "event-1" || event.ApplicationID != "ios-sandbox" || event.EventType != "entitlement.changed" {
		t.Fatalf("saved event = %#v", event)
	}
	if event.BodyFingerprint != sha256.Sum256(body) || !event.ReceivedAt.Equal(now) {
		t.Fatalf("saved fingerprint or timestamp does not match authenticated request")
	}
}

// TestReceiverRejectsInvalidSignature verifies unauthenticated bodies never reach persistence.
func TestReceiverRejectsInvalidSignature(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{saveResult: SaveInserted}
	receiver := newTestReceiver(t, store, now, defaultTestBodyLimit)
	body := []byte(`{"schema_version":1,"application_id":"ios-sandbox","event_type":"entitlement.changed"}`)
	request := signedRequest(t, now, body, "event-1")
	request.Header.Set("IAPStack-Signature", signaturePrefix+hex.EncodeToString(make([]byte, sha256.Size)))
	recorder := httptest.NewRecorder()

	receiver.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized || len(store.events) != 0 {
		t.Fatalf("status/events = %d/%d, want %d/0", recorder.Code, len(store.events), http.StatusUnauthorized)
	}
}

// TestReceiverRejectsStaleTimestamp verifies replay-window enforcement precedes persistence.
func TestReceiverRejectsStaleTimestamp(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{saveResult: SaveInserted}
	receiver := newTestReceiver(t, store, now, defaultTestBodyLimit)
	body := []byte(`{"schema_version":1,"application_id":"ios-sandbox","event_type":"entitlement.changed"}`)
	request := signedRequest(t, now.Add(-6*time.Minute), body, "event-1")
	recorder := httptest.NewRecorder()

	receiver.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized || len(store.events) != 0 {
		t.Fatalf("status/events = %d/%d, want %d/0", recorder.Code, len(store.events), http.StatusUnauthorized)
	}
}

// TestReceiverBoundsWebhookBody verifies oversized input is rejected before signature work or storage.
func TestReceiverBoundsWebhookBody(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{saveResult: SaveInserted}
	receiver := newTestReceiver(t, store, now, 16)
	body := bytes.Repeat([]byte("a"), 17)
	request := signedRequest(t, now, body, "event-1")
	recorder := httptest.NewRecorder()

	receiver.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge || len(store.events) != 0 {
		t.Fatalf("status/events = %d/%d, want %d/0", recorder.Code, len(store.events), http.StatusRequestEntityTooLarge)
	}
}

// TestReceiverAcceptsWebhooksSignContract verifies sender and receiver share the v2 MAC.
func TestReceiverAcceptsWebhooksSignContract(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{saveResult: SaveInserted}
	receiver := newTestReceiver(t, store, now, defaultTestBodyLimit)
	body := []byte(`{"schema_version":1,"application_id":"ios-sandbox","event_type":"entitlement.changed"}`)
	eventID := "event-1"
	request := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("IAPStack-Event-ID", eventID)
	request.Header.Set("IAPStack-Timestamp", strconv.FormatInt(now.Unix(), 10))
	request.Header.Set("IAPStack-Signature", "v2="+webhooks.Sign([]byte(testSecret), eventID, now, body))
	recorder := httptest.NewRecorder()

	receiver.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || len(store.events) != 1 {
		t.Fatalf("status/events = %d/%d, want %d/1", recorder.Code, len(store.events), http.StatusNoContent)
	}
}

// TestReceiverRejectsEventIDSubstitution verifies the event ID is inside the MAC.
func TestReceiverRejectsEventIDSubstitution(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{saveResult: SaveInserted}
	receiver := newTestReceiver(t, store, now, defaultTestBodyLimit)
	body := []byte(`{"schema_version":1,"application_id":"ios-sandbox","event_type":"entitlement.changed"}`)
	request := signedRequest(t, now, body, "event-1")
	request.Header.Set("IAPStack-Event-ID", "attacker-new-event")
	recorder := httptest.NewRecorder()

	receiver.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized || len(store.events) != 0 {
		t.Fatalf("status/events = %d/%d, want %d/0", recorder.Code, len(store.events), http.StatusUnauthorized)
	}
}

// TestReceiverRejectsLegacyUnsignedEventIDSignature verifies v1 signatures cannot authenticate a new ID.
func TestReceiverRejectsLegacyUnsignedEventIDSignature(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{saveResult: SaveInserted}
	receiver := newTestReceiver(t, store, now, defaultTestBodyLimit)
	body := []byte(`{"schema_version":1,"application_id":"ios-sandbox","event_type":"entitlement.changed"}`)
	timestampText := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(testSecret))
	_, _ = mac.Write([]byte(timestampText + "."))
	_, _ = mac.Write(body)
	request := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("IAPStack-Event-ID", "attacker-new-event")
	request.Header.Set("IAPStack-Timestamp", timestampText)
	request.Header.Set("IAPStack-Signature", "v1="+hex.EncodeToString(mac.Sum(nil)))
	recorder := httptest.NewRecorder()

	receiver.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized || len(store.events) != 0 {
		t.Fatalf("status/events = %d/%d, want %d/0", recorder.Code, len(store.events), http.StatusUnauthorized)
	}
}

// TestReceiverDeduplicatesExactAuthenticatedReplay verifies an exact replay stays one event.
func TestReceiverDeduplicatesExactAuthenticatedReplay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := &recordingStore{events: make(map[string]Event)}
	receiver := newTestReceiver(t, store, now, defaultTestBodyLimit)
	body := []byte(`{"schema_version":1,"application_id":"ios-sandbox","event_type":"entitlement.changed"}`)
	for range 2 {
		request := signedRequest(t, now, body, "event-1")
		recorder := httptest.NewRecorder()
		receiver.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
		}
	}
	if store.inserted != 1 || len(store.events) != 1 {
		t.Fatalf("store state = inserted %d events %d, want 1/1", store.inserted, len(store.events))
	}
}

// TestReceiverReportsIdentityConflict verifies altered replays are visible and rejected.
func TestReceiverReportsIdentityConflict(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{saveResult: SaveConflict}
	receiver := newTestReceiver(t, store, now, defaultTestBodyLimit)
	body := []byte(`{"schema_version":1,"application_id":"ios-sandbox","event_type":"entitlement.changed"}`)
	request := signedRequest(t, now, body, "event-1")
	recorder := httptest.NewRecorder()

	receiver.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
}

// TestReceiverReadinessTracksStore verifies health and readiness have distinct dependency semantics.
func TestReceiverReadinessTracksStore(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{saveResult: SaveInserted, pingError: errors.New("database unavailable")}
	receiver := newTestReceiver(t, store, now, defaultTestBodyLimit)

	for _, test := range []struct {
		path string
		want int
	}{
		{path: HealthPath, want: http.StatusNoContent},
		{path: ReadinessPath, want: http.StatusServiceUnavailable},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		recorder := httptest.NewRecorder()
		receiver.ServeHTTP(recorder, request)
		if recorder.Code != test.want {
			t.Fatalf("%s status = %d, want %d", test.path, recorder.Code, test.want)
		}
	}
}

// newTestReceiver constructs a receiver with deterministic time and discarded logs.
func newTestReceiver(t *testing.T, store Store, now time.Time, bodyLimit int64) *Receiver {
	t.Helper()
	receiver, err := New(Config{
		Secret: []byte(testSecret), BodyLimit: bodyLimit,
		TimestampTolerance: 5 * time.Minute,
		Store:              store,
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return receiver
}

// signedRequest creates one correctly signed IAPStack delivery fixture.
func signedRequest(t *testing.T, timestamp time.Time, body []byte, eventID string) *http.Request {
	t.Helper()
	timestampText := strconv.FormatInt(timestamp.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(testSecret))
	_, _ = io.WriteString(mac, signatureVersion)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = io.WriteString(mac, eventID)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = io.WriteString(mac, timestampText)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = mac.Write(body)
	request := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("IAPStack-Event-ID", eventID)
	request.Header.Set("IAPStack-Timestamp", timestampText)
	request.Header.Set("IAPStack-Signature", signaturePrefix+hex.EncodeToString(mac.Sum(nil)))
	return request
}
