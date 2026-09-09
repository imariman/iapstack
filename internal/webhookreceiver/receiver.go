// Package webhookreceiver provides a production-shaped IAPStack webhook test receiver.
package webhookreceiver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// WebhookPath is the public endpoint used for signed IAPStack deliveries.
	WebhookPath = "/webhooks/iapstack"
	// HealthPath is the process-only liveness endpoint.
	HealthPath = "/healthz"
	// ReadinessPath verifies that the durable deduplication store is reachable.
	ReadinessPath = "/readyz"

	// signatureVersion identifies the authenticated IAPStack signing contract.
	signatureVersion = "v2"
	// signaturePrefix identifies the supported IAPStack signing contract.
	signaturePrefix = signatureVersion + "="
	// signatureMACSeparator unambiguously delimits versioned MAC fields.
	signatureMACSeparator = "\n"
	// signatureBytes is the byte length of one HMAC-SHA-256 signature.
	signatureBytes = sha256.Size
	// maximumIdentityLength bounds event-controlled metadata retained by the receiver.
	maximumIdentityLength = 128

	// SaveInserted indicates that the event identity was stored for the first time.
	SaveInserted SaveResult = "inserted"
	// SaveDuplicate indicates an idempotent replay with the same body fingerprint.
	SaveDuplicate SaveResult = "duplicate"
	// SaveConflict indicates reuse of an event identity with a different body.
	SaveConflict SaveResult = "conflict"
)

// SaveResult describes whether an authenticated event was newly stored or replayed.
type SaveResult string

// Store persists authenticated event identities without retaining webhook bodies.
type Store interface {
	// Ping verifies that durable deduplication is available.
	Ping(context.Context) error
	// Save atomically inserts or compares one authenticated event identity.
	Save(context.Context, Event) (SaveResult, error)
	// Close releases durable store resources.
	Close()
}

// Event contains the bounded metadata retained after successful authentication.
type Event struct {
	ID              string
	ApplicationID   string
	EventType       string
	BodyFingerprint [sha256.Size]byte
	ReceivedAt      time.Time
}

// Config controls signature verification, request bounds, and dependencies.
type Config struct {
	Secret             []byte
	BodyLimit          int64
	TimestampTolerance time.Duration
	Store              Store
	Logger             *slog.Logger
	Clock              func() time.Time
}

// Receiver authenticates, validates, and deduplicates IAPStack webhook deliveries.
type Receiver struct {
	secret             []byte
	bodyLimit          int64
	timestampTolerance time.Duration
	store              Store
	logger             *slog.Logger
	clock              func() time.Time
}

// eventEnvelope contains only the non-sensitive webhook fields needed for audit metadata.
type eventEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	ApplicationID string `json:"application_id"`
	EventType     string `json:"event_type"`
}

// New validates dependencies and constructs a bounded webhook receiver.
func New(config Config) (*Receiver, error) {
	if len(config.Secret) < 32 {
		return nil, errors.New("webhook receiver secret must be at least 32 bytes")
	}
	if config.BodyLimit <= 0 {
		return nil, errors.New("webhook receiver body limit must be positive")
	}
	if config.TimestampTolerance <= 0 {
		return nil, errors.New("webhook receiver timestamp tolerance must be positive")
	}
	if config.Store == nil {
		return nil, errors.New("webhook receiver store is required")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Receiver{
		secret:             append([]byte(nil), config.Secret...),
		bodyLimit:          config.BodyLimit,
		timestampTolerance: config.TimestampTolerance,
		store:              config.Store,
		logger:             config.Logger,
		clock:              config.Clock,
	}, nil
}

// ServeHTTP routes liveness, readiness, and signed delivery requests.
func (receiver *Receiver) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(writer)
	switch request.URL.Path {
	case HealthPath:
		receiver.serveHealth(writer, request)
	case ReadinessPath:
		receiver.serveReadiness(writer, request)
	case WebhookPath:
		receiver.serveWebhook(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

// Close overwrites the in-memory signing secret and closes durable resources.
func (receiver *Receiver) Close() {
	for index := range receiver.secret {
		receiver.secret[index] = 0
	}
	receiver.store.Close()
}

// serveHealth reports process liveness without touching dependencies.
func (receiver *Receiver) serveHealth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// serveReadiness reports whether durable event deduplication is available.
func (receiver *Receiver) serveReadiness(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	if err := receiver.store.Ping(request.Context()); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "receiver_unavailable")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// serveWebhook authenticates one exact raw body before parsing or persisting metadata.
func (receiver *Receiver) serveWebhook(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	if !isJSON(request.Header.Get("Content-Type")) {
		writeError(writer, http.StatusUnsupportedMediaType, "content_type_required")
		return
	}
	eventID := request.Header.Get("IAPStack-Event-ID")
	if !validIdentity(eventID) {
		writeError(writer, http.StatusBadRequest, "invalid_event_id")
		return
	}
	timestamp, timestampText, ok := receiver.validTimestamp(request.Header.Get("IAPStack-Timestamp"))
	if !ok {
		writeError(writer, http.StatusUnauthorized, "invalid_timestamp")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, receiver.bodyLimit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(writer, http.StatusRequestEntityTooLarge, "body_too_large")
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_body")
		return
	}
	if !receiver.validSignature(eventID, timestampText, body, request.Header.Get("IAPStack-Signature")) {
		writeError(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	envelope, err := decodeEnvelope(body)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_event")
		return
	}
	result, err := receiver.store.Save(request.Context(), Event{
		ID: eventID, ApplicationID: envelope.ApplicationID, EventType: envelope.EventType,
		BodyFingerprint: sha256.Sum256(body), ReceivedAt: receiver.clock().UTC(),
	})
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "receiver_unavailable")
		return
	}
	if result == SaveConflict {
		writeError(writer, http.StatusConflict, "event_identity_conflict")
		return
	}
	receiver.logger.Info("iapstack webhook accepted",
		"event_id", eventID,
		"application_id", envelope.ApplicationID,
		"event_type", envelope.EventType,
		"delivery", string(result),
		"signed_at", timestamp.UTC().Format(time.RFC3339),
	)
	writer.WriteHeader(http.StatusNoContent)
}

// validTimestamp parses a canonical Unix timestamp inside the configured replay window.
func (receiver *Receiver) validTimestamp(value string) (time.Time, string, bool) {
	if value == "" || strings.TrimSpace(value) != value {
		return time.Time{}, "", false
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != value {
		return time.Time{}, "", false
	}
	timestamp := time.Unix(seconds, 0).UTC()
	delta := receiver.clock().UTC().Sub(timestamp)
	if delta < -receiver.timestampTolerance || delta > receiver.timestampTolerance {
		return time.Time{}, "", false
	}
	return timestamp, value, true
}

// validSignature compares the provided HMAC-SHA-256 value in constant time.
func (receiver *Receiver) validSignature(eventID, timestamp string, body []byte, value string) bool {
	if !strings.HasPrefix(value, signaturePrefix) || len(value) != len(signaturePrefix)+(signatureBytes*2) {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(value, signaturePrefix))
	if err != nil || len(provided) != signatureBytes {
		return false
	}
	mac := hmac.New(sha256.New, receiver.secret)
	_, _ = io.WriteString(mac, signatureVersion)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = io.WriteString(mac, eventID)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = io.WriteString(mac, timestamp)
	_, _ = io.WriteString(mac, signatureMACSeparator)
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

// decodeEnvelope validates the stable event metadata used by the receiver audit row.
func decodeEnvelope(body []byte) (eventEnvelope, error) {
	var envelope eventEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return eventEnvelope{}, err
	}
	if envelope.SchemaVersion <= 0 || !validIdentity(envelope.ApplicationID) || !validIdentity(envelope.EventType) {
		return eventEnvelope{}, errors.New("webhook event metadata is invalid")
	}
	return envelope, nil
}

// isJSON accepts JSON content types with optional parameters.
func isJSON(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "application/json"
}

// validIdentity accepts bounded printable identifiers without log-breaking control bytes.
func validIdentity(value string) bool {
	if value == "" || len(value) > maximumIdentityLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

// setSecurityHeaders applies browser-safe defaults even though the receiver has no HTML UI.
func setSecurityHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("X-Frame-Options", "DENY")
}

// methodNotAllowed returns a bounded response and advertises the only accepted method.
func methodNotAllowed(writer http.ResponseWriter, method string) {
	writer.Header().Set("Allow", method)
	writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
}

// writeError returns a stable JSON error without internal details.
func writeError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"code": code})
}
