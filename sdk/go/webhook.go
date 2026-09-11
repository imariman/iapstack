package iapstack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// signatureVersion identifies the authenticated webhook signing contract.
	signatureVersion = "v2"
	// signaturePrefix is the only accepted IAPStack-Signature prefix.
	signaturePrefix = signatureVersion + "="
	// signatureMACSeparator unambiguously delimits versioned MAC fields.
	signatureMACSeparator = "\n"
	// signatureBytes is the HMAC-SHA-256 digest length.
	signatureBytes = sha256.Size
	// minimumWebhookSecretBytes prevents trivially brute-forced HMAC keys.
	minimumWebhookSecretBytes = 32
	// maximumWebhookIdentityLength bounds event-controlled header identities.
	maximumWebhookIdentityLength = 128
	// defaultWebhookBodyLimit bounds one webhook payload to one mebibyte.
	defaultWebhookBodyLimit int64 = 1 << 20
	// defaultWebhookTimestampTolerance is the production five-minute replay window.
	defaultWebhookTimestampTolerance = 5 * time.Minute

	// headerEventID is the authenticated webhook event identity header.
	headerEventID = "IAPStack-Event-ID"
	// headerTimestamp is the unix-seconds webhook timestamp header.
	headerTimestamp = "IAPStack-Timestamp"
	// headerSignature is the versioned HMAC header.
	headerSignature = "IAPStack-Signature"
)

// EventStore atomically deduplicates authenticated webhook event identities.
type EventStore interface {
	// Remember records one authenticated event ID and body fingerprint.
	// It returns duplicate=true when the same identity was stored before.
	// A conflicting fingerprint for an existing ID returns a WebhookError.
	Remember(ctx context.Context, eventID string, fingerprint [sha256.Size]byte) (duplicate bool, err error)
}

// WebhookConfig controls signature verification, replay window, and dedupe storage.
type WebhookConfig struct {
	Secret             []byte
	BodyLimit          int64
	TimestampTolerance time.Duration
	Store              EventStore
	Clock              func() time.Time
}

// WebhookVerifier authenticates v2 IAPStack deliveries and deduplicates event IDs.
type WebhookVerifier struct {
	secret             []byte
	bodyLimit          int64
	timestampTolerance time.Duration
	store              EventStore
	clock              func() time.Time
}

// WebhookEvent is one authenticated, optionally duplicate, entitlement change.
type WebhookEvent struct {
	ID        string
	Timestamp time.Time
	Duplicate bool
	Change    EntitlementChange
}

// MemoryEventStore is a process-local EventStore for tests and single-instance hosts.
type MemoryEventStore struct {
	mu     sync.Mutex
	events map[string][sha256.Size]byte
}

type entitlementChangePayload struct {
	SchemaVersion       int        `json:"schema_version"`
	ProjectID           string     `json:"project_id"`
	ApplicationID       string     `json:"application_id"`
	CustomerID          string     `json:"customer_id"`
	EntitlementID       string     `json:"entitlement_id"`
	EntitlementKey      string     `json:"entitlement_key"`
	Access              string     `json:"access"`
	AccessReason        string     `json:"access_reason"`
	SourceObservationID string     `json:"source_observation_id"`
	SourceApplicationID string     `json:"source_application_id"`
	SourceProductID     string     `json:"source_product_id"`
	EffectiveStartsAt   *time.Time `json:"effective_starts_at"`
	EffectiveEndsAt     *time.Time `json:"effective_ends_at"`
	Version             int64      `json:"version"`
}

// NewMemoryEventStore constructs an in-memory dedupe store.
func NewMemoryEventStore() *MemoryEventStore {
	return &MemoryEventStore{events: make(map[string][sha256.Size]byte)}
}

// NewWebhookVerifier validates dependencies and constructs a fail-closed verifier.
func NewWebhookVerifier(config WebhookConfig) (*WebhookVerifier, error) {
	if len(config.Secret) < minimumWebhookSecretBytes {
		return nil, errors.New("webhook signing secret must contain at least 32 bytes")
	}
	if config.Store == nil {
		return nil, errors.New("webhook event store is required")
	}
	if config.BodyLimit == 0 {
		config.BodyLimit = defaultWebhookBodyLimit
	}
	if config.BodyLimit <= 0 {
		return nil, errors.New("webhook body limit must be positive")
	}
	if config.TimestampTolerance == 0 {
		config.TimestampTolerance = defaultWebhookTimestampTolerance
	}
	if config.TimestampTolerance <= 0 {
		return nil, errors.New("webhook timestamp tolerance must be positive")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &WebhookVerifier{
		secret:             append([]byte(nil), config.Secret...),
		bodyLimit:          config.BodyLimit,
		timestampTolerance: config.TimestampTolerance,
		store:              config.Store,
		clock:              config.Clock,
	}, nil
}

// VerifyRequest authenticates one HTTP delivery, parses the payload, and deduplicates it.
func (verifier *WebhookVerifier) VerifyRequest(request *http.Request) (WebhookEvent, error) {
	if request == nil {
		return WebhookEvent{}, &WebhookError{Code: "invalid_body"}
	}
	if request.Method != http.MethodPost {
		return WebhookEvent{}, &WebhookError{Code: "method_not_allowed"}
	}
	if !isJSONContentType(request.Header.Get("Content-Type")) {
		return WebhookEvent{}, &WebhookError{Code: "content_type_required"}
	}
	eventID := request.Header.Get(headerEventID)
	if !validWebhookIdentity(eventID) {
		return WebhookEvent{}, &WebhookError{Code: "invalid_event_id"}
	}
	timestamp, timestampText, ok := verifier.validTimestamp(request.Header.Get(headerTimestamp))
	if !ok {
		return WebhookEvent{}, &WebhookError{Code: "invalid_timestamp"}
	}
	if request.Body == nil {
		return WebhookEvent{}, &WebhookError{Code: "invalid_body"}
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, request.Body, verifier.bodyLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return WebhookEvent{}, &WebhookError{Code: "body_too_large"}
		}
		return WebhookEvent{}, &WebhookError{Code: "invalid_body"}
	}
	if !verifier.validSignature(eventID, timestampText, body, request.Header.Get(headerSignature)) {
		return WebhookEvent{}, &WebhookError{Code: "invalid_signature"}
	}
	change, err := decodeEntitlementChange(body)
	if err != nil {
		return WebhookEvent{}, &WebhookError{Code: "invalid_event"}
	}
	duplicate, err := verifier.store.Remember(request.Context(), eventID, sha256.Sum256(body))
	if err != nil {
		return WebhookEvent{}, err
	}
	return WebhookEvent{ID: eventID, Timestamp: timestamp, Duplicate: duplicate, Change: change}, nil
}

// Close overwrites the in-memory signing secret.
func (verifier *WebhookVerifier) Close() {
	if verifier == nil {
		return
	}
	for index := range verifier.secret {
		verifier.secret[index] = 0
	}
}

// Remember inserts or compares one authenticated event identity.
func (store *MemoryEventStore) Remember(_ context.Context, eventID string, fingerprint [sha256.Size]byte) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.events == nil {
		store.events = make(map[string][sha256.Size]byte)
	}
	existing, ok := store.events[eventID]
	if !ok {
		store.events[eventID] = fingerprint
		return false, nil
	}
	if existing == fingerprint {
		return true, nil
	}
	return false, &WebhookError{Code: "event_identity_conflict"}
}

// validTimestamp parses a canonical Unix timestamp inside the configured replay window.
func (verifier *WebhookVerifier) validTimestamp(value string) (time.Time, string, bool) {
	if value == "" || strings.TrimSpace(value) != value {
		return time.Time{}, "", false
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != value {
		return time.Time{}, "", false
	}
	timestamp := time.Unix(seconds, 0).UTC()
	delta := verifier.clock().UTC().Sub(timestamp)
	if delta < -verifier.timestampTolerance || delta > verifier.timestampTolerance {
		return time.Time{}, "", false
	}
	return timestamp, value, true
}

// validSignature compares the provided HMAC-SHA-256 value in constant time.
func (verifier *WebhookVerifier) validSignature(eventID, timestamp string, body []byte, value string) bool {
	if strings.HasPrefix(value, "v1=") {
		return false
	}
	if !strings.HasPrefix(value, signaturePrefix) || len(value) != len(signaturePrefix)+(signatureBytes*2) {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(value, signaturePrefix))
	if err != nil || len(provided) != signatureBytes {
		return false
	}
	mac := hmac.New(sha256.New, verifier.secret)
	_, _ = io.WriteString(mac, signatureVersion)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = io.WriteString(mac, eventID)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = io.WriteString(mac, timestamp)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

// decodeEntitlementChange validates the production entitlement.changed payload.
func decodeEntitlementChange(body []byte) (EntitlementChange, error) {
	var payload entitlementChangePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return EntitlementChange{}, err
	}
	if payload.SchemaVersion <= 0 || payload.Version < 1 ||
		payload.ProjectID == "" || payload.ApplicationID == "" || payload.CustomerID == "" ||
		payload.EntitlementID == "" || payload.EntitlementKey == "" || payload.Access == "" ||
		payload.AccessReason == "" || payload.SourceObservationID == "" ||
		payload.SourceApplicationID == "" || payload.SourceProductID == "" {
		return EntitlementChange{}, errors.New("webhook event metadata is invalid")
	}
	return EntitlementChange{
		SchemaVersion:       payload.SchemaVersion,
		ProjectID:           payload.ProjectID,
		ApplicationID:       payload.ApplicationID,
		CustomerID:          payload.CustomerID,
		EntitlementID:       payload.EntitlementID,
		EntitlementKey:      payload.EntitlementKey,
		Access:              payload.Access,
		AccessReason:        payload.AccessReason,
		SourceObservationID: payload.SourceObservationID,
		SourceApplicationID: payload.SourceApplicationID,
		SourceProductID:     payload.SourceProductID,
		EffectiveStartsAt:   utcTime(payload.EffectiveStartsAt),
		EffectiveEndsAt:     utcTime(payload.EffectiveEndsAt),
		Version:             payload.Version,
	}, nil
}

// isJSONContentType accepts JSON content types with optional parameters.
func isJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == jsonContentType
}

// validWebhookIdentity accepts bounded printable identifiers without control bytes.
func validWebhookIdentity(value string) bool {
	if value == "" || len(value) > maximumWebhookIdentityLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
