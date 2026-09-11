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
	// Remember records one authenticated event ID and opaque body fingerprint.
	// The verifier hashes the raw body; hosts persist the fingerprint as-is and
	// must not import crypto/sha256 to recompute it. It returns duplicate=true
	// when the same identity was stored before. A conflicting fingerprint for
	// an existing ID returns a WebhookError.
	Remember(ctx context.Context, eventID string, fingerprint []byte) (duplicate bool, err error)
}

// WebhookConfig controls signature verification, replay window, and dedupe storage.
type WebhookConfig struct {
	// Secret is the webhook signing secret; it must contain at least 32 bytes.
	Secret []byte
	// BodyLimit is the maximum accepted raw webhook body size.
	BodyLimit int64
	// TimestampTolerance is the allowed clock skew around IAPStack-Timestamp.
	TimestampTolerance time.Duration
	// Store atomically deduplicates authenticated event IDs.
	Store EventStore
}

// WebhookVerifier authenticates v2 IAPStack deliveries and deduplicates event IDs.
type WebhookVerifier struct {
	// secret is the copied HMAC key used for constant-time signature comparison.
	secret []byte
	// bodyLimit is the maximum accepted raw webhook body size.
	bodyLimit int64
	// timestampTolerance is the allowed clock skew around IAPStack-Timestamp.
	timestampTolerance time.Duration
	// store atomically deduplicates authenticated event IDs.
	store EventStore
	// clock supplies the current time for replay-window checks.
	clock func() time.Time
}

// WebhookEvent is one authenticated, optionally duplicate, entitlement change.
type WebhookEvent struct {
	// ID is the authenticated IAPStack-Event-ID value.
	ID string
	// Timestamp is the authenticated IAPStack-Timestamp value.
	Timestamp time.Time
	// Duplicate reports whether this event ID was already stored with the same body.
	Duplicate bool
	// Change is the parsed entitlement.changed payload.
	Change EntitlementChange
}

// MemoryEventStore is a process-local EventStore for tests and single-instance hosts.
type MemoryEventStore struct {
	// mu serializes identity inserts and conflict checks.
	mu sync.Mutex
	// events maps authenticated event IDs to the accepted body fingerprint.
	events map[string]string
}

// webhookHandler maps VerifyRequest onto IAPStack's outbound retry policy.
type webhookHandler struct {
	// verifier authenticates and deduplicates each delivery.
	verifier *WebhookVerifier
	// onEvent handles authenticated, non-duplicate entitlement changes.
	onEvent func(context.Context, WebhookEvent) error
}

// NewMemoryEventStore constructs an in-memory dedupe store.
func NewMemoryEventStore() *MemoryEventStore {
	return &MemoryEventStore{events: make(map[string]string)}
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
	return &WebhookVerifier{
		secret:             append([]byte(nil), config.Secret...),
		bodyLimit:          config.BodyLimit,
		timestampTolerance: config.TimestampTolerance,
		store:              config.Store,
		clock:              time.Now,
	}, nil
}

// Handler returns a long-lived HTTP handler for IAPStack webhook deliveries.
//
// Construct the verifier once at process start. The handler maps signature and
// timestamp failures to 401, identity conflict to 409, and store or callback
// errors to 503. IAPStack retries only 408, 429, and 5xx; 401 is a permanent
// webhook_rejected failure.
func (verifier *WebhookVerifier) Handler(onEvent func(context.Context, WebhookEvent) error) http.Handler {
	if onEvent == nil {
		onEvent = func(context.Context, WebhookEvent) error { return nil }
	}
	return &webhookHandler{verifier: verifier, onEvent: onEvent}
}

// ServeHTTP authenticates one delivery and invokes onEvent for new events.
func (handler *webhookHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	event, err := handler.verifier.VerifyRequest(request)
	if err != nil {
		writeWebhookError(writer, err)
		return
	}
	if event.Duplicate {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if err := handler.onEvent(request.Context(), event); err != nil {
		writeWebhookError(writer, &WebhookError{
			Code:       "receiver_unavailable",
			StatusCode: http.StatusServiceUnavailable,
			Cause:      err,
		})
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// VerifyRequest authenticates one HTTP delivery, parses the payload, and deduplicates it.
//
// Prefer Handler so hosts do not reconstruct HTTP status mapping. Returned
// WebhookError values include the status IAPStack's outbound delivery expects.
func (verifier *WebhookVerifier) VerifyRequest(request *http.Request) (WebhookEvent, error) {
	if request == nil {
		return WebhookEvent{}, webhookFailure("invalid_body")
	}
	if request.Method != http.MethodPost {
		return WebhookEvent{}, webhookFailure("method_not_allowed")
	}
	if !isJSONContentType(request.Header.Get("Content-Type")) {
		return WebhookEvent{}, webhookFailure("content_type_required")
	}
	eventID := request.Header.Get(headerEventID)
	if !validWebhookIdentity(eventID) {
		return WebhookEvent{}, webhookFailure("invalid_event_id")
	}
	timestamp, timestampText, ok := verifier.validTimestamp(request.Header.Get(headerTimestamp))
	if !ok {
		return WebhookEvent{}, webhookFailure("invalid_timestamp")
	}
	if request.Body == nil {
		return WebhookEvent{}, webhookFailure("invalid_body")
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, request.Body, verifier.bodyLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return WebhookEvent{}, webhookFailure("body_too_large")
		}
		return WebhookEvent{}, webhookFailure("invalid_body")
	}
	if !verifier.validSignature(eventID, timestampText, body, request.Header.Get(headerSignature)) {
		return WebhookEvent{}, webhookFailure("invalid_signature")
	}
	change, err := decodeEntitlementChange(body)
	if err != nil {
		return WebhookEvent{}, webhookFailure("invalid_event")
	}
	fingerprint := sha256.Sum256(body)
	duplicate, err := verifier.store.Remember(request.Context(), eventID, fingerprint[:])
	if err != nil {
		return WebhookEvent{}, wrapStoreError(err)
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
func (store *MemoryEventStore) Remember(_ context.Context, eventID string, fingerprint []byte) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.events == nil {
		store.events = make(map[string]string)
	}
	copied := string(fingerprint)
	existing, ok := store.events[eventID]
	if !ok {
		store.events[eventID] = copied
		return false, nil
	}
	if existing == copied {
		return true, nil
	}
	return false, webhookFailure("event_identity_conflict")
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
	var change EntitlementChange
	if err := json.Unmarshal(body, &change); err != nil {
		return EntitlementChange{}, err
	}
	if err := change.validate(); err != nil {
		return EntitlementChange{}, err
	}
	change.normalize()
	return change, nil
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

// webhookFailure constructs a WebhookError with the IAPStack retry-policy status.
func webhookFailure(code string) *WebhookError {
	return &WebhookError{Code: code, StatusCode: webhookStatus(code)}
}

// webhookStatus maps verifier failure codes onto IAPStack outbound delivery statuses.
func webhookStatus(code string) int {
	switch code {
	case "invalid_timestamp", "invalid_signature":
		return http.StatusUnauthorized
	case "method_not_allowed":
		return http.StatusMethodNotAllowed
	case "content_type_required":
		return http.StatusUnsupportedMediaType
	case "body_too_large":
		return http.StatusRequestEntityTooLarge
	case "event_identity_conflict":
		return http.StatusConflict
	case "receiver_unavailable":
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadRequest
	}
}

// wrapStoreError preserves WebhookError values and maps other store failures to 503.
func wrapStoreError(err error) error {
	if err == nil {
		return nil
	}
	var webhookErr *WebhookError
	if errors.As(err, &webhookErr) {
		if webhookErr.StatusCode != 0 {
			return webhookErr
		}
		return &WebhookError{Code: webhookErr.Code, StatusCode: webhookStatus(webhookErr.Code), Cause: webhookErr.Cause}
	}
	return &WebhookError{Code: "receiver_unavailable", StatusCode: http.StatusServiceUnavailable, Cause: err}
}

// writeWebhookError writes the JSON error envelope hosts must return to IAPStack.
func writeWebhookError(writer http.ResponseWriter, err error) {
	webhookErr, ok := wrapStoreError(err).(*WebhookError)
	if !ok {
		webhookErr = webhookFailure("receiver_unavailable")
	}
	if webhookErr.Code == "method_not_allowed" {
		writer.Header().Set("Allow", http.MethodPost)
	}
	writer.Header().Set("Content-Type", jsonContentType)
	writer.WriteHeader(webhookErr.StatusCode)
	_ = json.NewEncoder(writer).Encode(map[string]string{"code": webhookErr.Code})
}
