package iapstack

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	// testWebhookSecret is a non-production HMAC fixture with the required entropy length.
	testWebhookSecret = "0123456789abcdef0123456789abcdef"
	// testWebhookBody is a production-shaped entitlement.changed payload.
	testWebhookBody = `{` +
		`"schema_version":1,` +
		`"project_id":"project-1",` +
		`"application_id":"ios-sandbox",` +
		`"customer_id":"customer-internal",` +
		`"entitlement_id":"entitlement-1",` +
		`"entitlement_key":"premium",` +
		`"access":"allowed",` +
		`"access_reason":"purchase_valid",` +
		`"source_observation_id":"observation-1",` +
		`"source_application_id":"ios-sandbox",` +
		`"source_product_id":"premium_annual",` +
		`"version":1` +
		`}`
)

// TestWebhookVerifierAcceptsAuthenticatedEvent verifies exact v2 signature validation.
func TestWebhookVerifierAcceptsAuthenticatedEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier := newTestVerifier(t, now, defaultWebhookBodyLimit)
	defer verifier.Close()

	event, err := verifier.VerifyRequest(signedWebhookRequest(t, now, []byte(testWebhookBody), "event-1"))
	if err != nil {
		t.Fatalf("VerifyRequest() error = %v", err)
	}
	if event.ID != "event-1" || event.Duplicate || event.Change.EntitlementKey != "premium" {
		t.Fatalf("event = %#v", event)
	}
	if !event.Change.Entitlement().GrantsAccessAt(now) {
		t.Fatal("authenticated entitlement change should grant access")
	}
}

// TestWebhookVerifierRejectsInvalidSignature verifies unauthenticated bodies never reach storage.
func TestWebhookVerifierRejectsInvalidSignature(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	store := NewMemoryEventStore()
	verifier := newTestVerifierWithStore(t, now, defaultWebhookBodyLimit, store)
	defer verifier.Close()

	request := signedWebhookRequest(t, now, []byte(testWebhookBody), "event-1")
	request.Header.Set(headerSignature, signaturePrefix+hex.EncodeToString(make([]byte, sha256.Size)))
	if _, err := verifier.VerifyRequest(request); !webhookCode(err, "invalid_signature") {
		t.Fatalf("error = %v, want invalid_signature", err)
	}
	if duplicate, err := store.Remember(request.Context(), "event-1", sha256.Sum256([]byte(testWebhookBody))); err != nil || duplicate {
		t.Fatalf("store remembered a rejected delivery: duplicate=%t err=%v", duplicate, err)
	}
}

// TestWebhookVerifierRejectsStaleTimestamp verifies replay-window enforcement precedes persistence.
func TestWebhookVerifierRejectsStaleTimestamp(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier := newTestVerifier(t, now, defaultWebhookBodyLimit)
	defer verifier.Close()

	request := signedWebhookRequest(t, now.Add(-6*time.Minute), []byte(testWebhookBody), "event-1")
	if _, err := verifier.VerifyRequest(request); !webhookCode(err, "invalid_timestamp") {
		t.Fatalf("error = %v, want invalid_timestamp", err)
	}
}

// TestWebhookVerifierRejectsFutureTimestampOutsideWindow verifies clock-skew bounds.
func TestWebhookVerifierRejectsFutureTimestampOutsideWindow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier := newTestVerifier(t, now, defaultWebhookBodyLimit)
	defer verifier.Close()

	request := signedWebhookRequest(t, now.Add(6*time.Minute), []byte(testWebhookBody), "event-1")
	if _, err := verifier.VerifyRequest(request); !webhookCode(err, "invalid_timestamp") {
		t.Fatalf("error = %v, want invalid_timestamp", err)
	}
}

// TestWebhookVerifierRejectsEventIDSubstitution verifies the event ID is inside the MAC.
func TestWebhookVerifierRejectsEventIDSubstitution(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier := newTestVerifier(t, now, defaultWebhookBodyLimit)
	defer verifier.Close()

	request := signedWebhookRequest(t, now, []byte(testWebhookBody), "event-1")
	request.Header.Set(headerEventID, "attacker-new-event")
	if _, err := verifier.VerifyRequest(request); !webhookCode(err, "invalid_signature") {
		t.Fatalf("error = %v, want invalid_signature", err)
	}
}

// TestWebhookVerifierRejectsLegacyV1Signatures verifies v1 MACs cannot authenticate a delivery.
func TestWebhookVerifierRejectsLegacyV1Signatures(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier := newTestVerifier(t, now, defaultWebhookBodyLimit)
	defer verifier.Close()

	body := []byte(testWebhookBody)
	timestampText := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	_, _ = io.WriteString(mac, timestampText+".")
	_, _ = mac.Write(body)
	request := httptest.NewRequest(http.MethodPost, "/webhooks/iapstack", bytes.NewReader(body))
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set(headerEventID, "attacker-new-event")
	request.Header.Set(headerTimestamp, timestampText)
	request.Header.Set(headerSignature, "v1="+hex.EncodeToString(mac.Sum(nil)))
	if _, err := verifier.VerifyRequest(request); !webhookCode(err, "invalid_signature") {
		t.Fatalf("error = %v, want invalid_signature", err)
	}
}

// TestWebhookVerifierDeduplicatesExactAuthenticatedReplay verifies one logical event ID.
func TestWebhookVerifierDeduplicatesExactAuthenticatedReplay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier := newTestVerifier(t, now, defaultWebhookBodyLimit)
	defer verifier.Close()

	first, err := verifier.VerifyRequest(signedWebhookRequest(t, now, []byte(testWebhookBody), "event-1"))
	if err != nil || first.Duplicate {
		t.Fatalf("first delivery = %#v err=%v", first, err)
	}
	second, err := verifier.VerifyRequest(signedWebhookRequest(t, now, []byte(testWebhookBody), "event-1"))
	if err != nil || !second.Duplicate {
		t.Fatalf("replay = %#v err=%v", second, err)
	}
}

// TestWebhookVerifierRejectsEventIdentityConflict verifies altered replays fail closed.
func TestWebhookVerifierRejectsEventIdentityConflict(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier := newTestVerifier(t, now, defaultWebhookBodyLimit)
	defer verifier.Close()

	if _, err := verifier.VerifyRequest(signedWebhookRequest(t, now, []byte(testWebhookBody), "event-1")); err != nil {
		t.Fatalf("first delivery error = %v", err)
	}
	altered := []byte(strings.Replace(testWebhookBody, `"version":1`, `"version":2`, 1))
	if _, err := verifier.VerifyRequest(signedWebhookRequest(t, now, altered, "event-1")); !webhookCode(err, "event_identity_conflict") {
		t.Fatalf("error = %v, want event_identity_conflict", err)
	}
}

// TestWebhookVerifierBoundsWebhookBody verifies oversized input is rejected before HMAC work.
func TestWebhookVerifierBoundsWebhookBody(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier := newTestVerifier(t, now, 16)
	defer verifier.Close()

	body := bytes.Repeat([]byte("a"), 17)
	if _, err := verifier.VerifyRequest(signedWebhookRequest(t, now, body, "event-1")); !webhookCode(err, "body_too_large") {
		t.Fatalf("error = %v, want body_too_large", err)
	}
}

// TestNewWebhookVerifierRejectsShortSecrets verifies the HMAC key length boundary.
func TestNewWebhookVerifierRejectsShortSecrets(t *testing.T) {
	t.Parallel()

	_, err := NewWebhookVerifier(WebhookConfig{Secret: []byte("short"), Store: NewMemoryEventStore()})
	if err == nil {
		t.Fatal("NewWebhookVerifier() accepted a short secret")
	}
}

// TestWebhookVerifierRejectsMethodAndContentType verifies request envelope checks.
func TestWebhookVerifierRejectsMethodAndContentType(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier := newTestVerifier(t, now, defaultWebhookBodyLimit)
	defer verifier.Close()

	getRequest := httptest.NewRequest(http.MethodGet, "/webhooks/iapstack", bytes.NewReader([]byte(testWebhookBody)))
	if _, err := verifier.VerifyRequest(getRequest); !webhookCode(err, "method_not_allowed") {
		t.Fatalf("GET error = %v, want method_not_allowed", err)
	}
	request := signedWebhookRequest(t, now, []byte(testWebhookBody), "event-1")
	request.Header.Set("Content-Type", "text/plain")
	if _, err := verifier.VerifyRequest(request); !webhookCode(err, "content_type_required") {
		t.Fatalf("content type error = %v, want content_type_required", err)
	}
}

// TestWebhookVerifierRejectsInvalidEventIDAndPayload verifies identity and body contracts.
func TestWebhookVerifierRejectsInvalidEventIDAndPayload(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier := newTestVerifier(t, now, defaultWebhookBodyLimit)
	defer verifier.Close()

	invalidID := signedWebhookRequest(t, now, []byte(testWebhookBody), "event 1")
	if _, err := verifier.VerifyRequest(invalidID); !webhookCode(err, "invalid_event_id") {
		t.Fatalf("event ID error = %v, want invalid_event_id", err)
	}
	invalidJSON := signedWebhookRequest(t, now, []byte(`{"schema_version":1}`), "event-1")
	if _, err := verifier.VerifyRequest(invalidJSON); !webhookCode(err, "invalid_event") {
		t.Fatalf("payload error = %v, want invalid_event", err)
	}
}

// TestWebhookVerifierAppliesDefaultBounds verifies zero config values become production defaults.
func TestWebhookVerifierAppliesDefaultBounds(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 31, 10, 0, 0, 0, time.UTC)
	verifier, err := NewWebhookVerifier(WebhookConfig{
		Secret: []byte(testWebhookSecret),
		Store:  NewMemoryEventStore(),
		Clock:  func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewWebhookVerifier() error = %v", err)
	}
	defer verifier.Close()
	if _, err := verifier.VerifyRequest(signedWebhookRequest(t, now, []byte(testWebhookBody), "event-1")); err != nil {
		t.Fatalf("VerifyRequest() error = %v", err)
	}
}

// TestWebhookVerifierRejectsNilRequest verifies a missing HTTP request fails closed.
func TestWebhookVerifierRejectsNilRequest(t *testing.T) {
	t.Parallel()

	verifier := newTestVerifier(t, time.Now().UTC(), defaultWebhookBodyLimit)
	defer verifier.Close()
	if _, err := verifier.VerifyRequest(nil); !webhookCode(err, "invalid_body") {
		t.Fatalf("error = %v, want invalid_body", err)
	}
}

// newTestVerifier constructs a verifier with an isolated memory store.
func newTestVerifier(t *testing.T, now time.Time, bodyLimit int64) *WebhookVerifier {
	t.Helper()
	return newTestVerifierWithStore(t, now, bodyLimit, NewMemoryEventStore())
}

// newTestVerifierWithStore constructs a verifier around one injected store.
func newTestVerifierWithStore(t *testing.T, now time.Time, bodyLimit int64, store EventStore) *WebhookVerifier {
	t.Helper()

	verifier, err := NewWebhookVerifier(WebhookConfig{
		Secret:             []byte(testWebhookSecret),
		BodyLimit:          bodyLimit,
		TimestampTolerance: 5 * time.Minute,
		Store:              store,
		Clock:              func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewWebhookVerifier() error = %v", err)
	}
	return verifier
}

// signedWebhookRequest builds one production-compatible v2 delivery.
func signedWebhookRequest(t *testing.T, timestamp time.Time, body []byte, eventID string) *http.Request {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/webhooks/iapstack", bytes.NewReader(body))
	request.Header.Set("Content-Type", jsonContentType)
	request.Header.Set(headerEventID, eventID)
	request.Header.Set(headerTimestamp, strconv.FormatInt(timestamp.Unix(), 10))
	request.Header.Set(headerSignature, "v2="+signWebhook(eventID, timestamp, body))
	return request
}

// signWebhook computes HMAC-SHA-256 over the documented newline-delimited v2 input.
func signWebhook(eventID string, timestamp time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	_, _ = io.WriteString(mac, signatureVersion)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = io.WriteString(mac, eventID)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = io.WriteString(mac, strconv.FormatInt(timestamp.Unix(), 10))
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// webhookCode reports whether err is a WebhookError with the expected code.
func webhookCode(err error, code string) bool {
	var webhookErr *WebhookError
	return errors.As(err, &webhookErr) && webhookErr.Code == code
}
