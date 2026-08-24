package stores

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"strings"
	"unicode"

	"github.com/imariman/iapstack/internal/core"
)

// CredentialKind identifies one provider-owned logical credential package.
type CredentialKind string

// Credential contains an opaque, versioned provider configuration with a private payload.
type Credential struct {
	Kind          CredentialKind
	ContentType   string
	SchemaVersion int
	payload       []byte
}

// CredentialSource resolves application-scoped credentials for concrete provider adapters.
type CredentialSource interface {
	// Credential returns one opaque credential package in the authoritative application scope.
	Credential(context.Context, core.Application, CredentialKind) (Credential, error)
}

var (
	// ErrCredentialNotFound indicates that an application has no credential for the requested kind.
	ErrCredentialNotFound = errors.New("store credential not found")
)

// NewCredential validates metadata and defensively copies one provider-owned credential payload.
func NewCredential(
	kind CredentialKind,
	contentType string,
	schemaVersion int,
	payload []byte,
) (Credential, error) {
	credential := Credential{
		Kind:          kind,
		ContentType:   contentType,
		SchemaVersion: schemaVersion,
		payload:       append([]byte(nil), payload...),
	}
	if err := credential.Validate(); err != nil {
		return Credential{}, err
	}
	return credential, nil
}

// Validate checks that a credential kind is present, trimmed, and control-free.
func (kind CredentialKind) Validate() error {
	return validateCredentialText("credential kind", string(kind))
}

// Validate checks credential metadata and requires a non-empty private payload.
func (credential Credential) Validate() error {
	var contentTypeError error
	if credential.ContentType == "" {
		contentTypeError = errors.New("credential content type is required")
	} else if _, _, err := mime.ParseMediaType(credential.ContentType); err != nil {
		contentTypeError = errors.New("credential content type must be a valid media type")
	} else {
		contentTypeError = validateCredentialText("credential content type", credential.ContentType)
	}
	var schemaVersionError error
	if credential.SchemaVersion <= 0 {
		schemaVersionError = errors.New("credential schema version must be positive")
	}
	var payloadError error
	if len(credential.payload) == 0 {
		payloadError = errors.New("credential payload is required")
	}
	return errors.Join(
		credential.Kind.Validate(),
		contentTypeError,
		schemaVersionError,
		payloadError,
	)
}

// Bytes returns a defensive copy for explicit use inside a provider adapter or protection boundary.
func (credential Credential) Bytes() []byte {
	return append([]byte(nil), credential.payload...)
}

// String returns credential metadata without payload bytes or a reversible digest.
func (credential Credential) String() string {
	if credential.Kind == "" && credential.ContentType == "" && credential.SchemaVersion == 0 && len(credential.payload) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"credential[kind=%s,content_type=%s,schema_version=%d,size=%d]",
		credential.Kind,
		credential.ContentType,
		credential.SchemaVersion,
		len(credential.payload),
	)
}

// GoString returns redacted credential metadata for detailed debug formatting.
func (credential Credential) GoString() string {
	return credential.String()
}

// LogValue returns safe structured metadata without payload bytes or a digest.
func (credential Credential) LogValue() slog.Value {
	if credential.Kind == "" && credential.ContentType == "" && credential.SchemaVersion == 0 && len(credential.payload) == 0 {
		return slog.StringValue("")
	}
	return slog.GroupValue(
		slog.String("kind", string(credential.Kind)),
		slog.String("content_type", credential.ContentType),
		slog.Int("schema_version", credential.SchemaVersion),
		slog.Int("size", len(credential.payload)),
	)
}

// validateCredentialText enforces non-empty, trimmed, control-free credential metadata.
func validateCredentialText(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not have leading or trailing whitespace", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}
