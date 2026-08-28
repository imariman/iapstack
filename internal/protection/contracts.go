// Package protection defines provider-neutral sensitive-data protection ports.
package protection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"github.com/imariman/iapstack/internal/core"
)

// Protector encrypts sensitive bytes and produces a stable scoped fingerprint.
type Protector interface {
	// Protect encrypts one validated plaintext request without retaining it after the call.
	Protect(context.Context, Request) (Value, error)
}

// Opener authenticates and decrypts protected values in their original scope.
type Opener interface {
	// Open authenticates and decrypts one protected value.
	Open(context.Context, OpenRequest) ([]byte, error)
}

// Service combines write and read protection operations for a rotating keyring.
type Service interface {
	Protector
	Opener
}

// Scope binds protected data to one project, application, and semantic purpose.
type Scope struct {
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
	Purpose       string
}

// Value contains authenticated ciphertext, a scoped fingerprint, and its encryption key ID.
type Value struct {
	Ciphertext  []byte
	Fingerprint [32]byte
	KeyID       string
}

// Request carries plaintext only across the explicit protection boundary.
type Request struct {
	Scope     Scope
	plaintext []byte
}

// OpenRequest binds a protected value to the scope required for authentication.
type OpenRequest struct {
	Scope Scope
	Value Value
}

var (
	// ErrOpenFailed indicates failed authentication without revealing whether data, scope, or key was wrong.
	ErrOpenFailed = errors.New("protected value authentication failed")
	// ErrKeyUnavailable indicates that a protected value references a key absent from the keyring.
	ErrKeyUnavailable = errors.New("protection key is unavailable")
)

// NewRequest validates scope and defensively copies plaintext into a protection request.
func NewRequest(scope Scope, plaintext []byte) (Request, error) {
	request := Request{Scope: scope, plaintext: append([]byte(nil), plaintext...)}
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	return request, nil
}

// NewOpenRequest validates scope and defensively copies a protected value.
func NewOpenRequest(scope Scope, value Value) (OpenRequest, error) {
	request := OpenRequest{Scope: scope, Value: value.Clone()}
	if err := request.Validate(); err != nil {
		return OpenRequest{}, err
	}
	return request, nil
}

// Protect creates, uses, and destroys one short-lived defensive plaintext copy.
func Protect(ctx context.Context, protector Protector, scope Scope, plaintext []byte) (Value, error) {
	if protector == nil {
		return Value{}, errors.New("data protector is required")
	}
	request, err := NewRequest(scope, plaintext)
	if err != nil {
		return Value{}, err
	}
	defer request.Destroy()
	return protector.Protect(ctx, request)
}

// Validate checks every scope field used for authentication and fingerprinting.
func (scope Scope) Validate() error {
	return errors.Join(
		scope.ProjectID.Validate(),
		scope.ApplicationID.Validate(),
		validateText("protection purpose", scope.Purpose),
	)
}

// Validate checks ciphertext, fingerprint, and encryption key identity.
func (value Value) Validate() error {
	if len(value.Ciphertext) == 0 {
		return errors.New("protected ciphertext is required")
	}
	if value.Fingerprint == ([32]byte{}) {
		return errors.New("protected fingerprint is required")
	}
	return validateText("encryption key ID", value.KeyID)
}

// Clone returns a defensive copy of a protected value.
func (value Value) Clone() Value {
	value.Ciphertext = append([]byte(nil), value.Ciphertext...)
	return value
}

// String returns a redacted representation that cannot expose ciphertext or fingerprint bytes.
func (value Value) String() string {
	if len(value.Ciphertext) == 0 && value.Fingerprint == ([32]byte{}) && value.KeyID == "" {
		return ""
	}
	return fmt.Sprintf("protected[key_id=%s,size=%d]", value.KeyID, len(value.Ciphertext))
}

// GoString returns redacted protected metadata for detailed debug formatting.
func (value Value) GoString() string {
	return value.String()
}

// LogValue returns safe structured metadata for a protected value.
func (value Value) LogValue() slog.Value {
	if len(value.Ciphertext) == 0 && value.Fingerprint == ([32]byte{}) && value.KeyID == "" {
		return slog.StringValue("")
	}
	return slog.GroupValue(
		slog.String("key_id", value.KeyID),
		slog.Int("ciphertext_size", len(value.Ciphertext)),
	)
}

// Validate checks request scope and requires non-empty plaintext.
func (request Request) Validate() error {
	if len(request.plaintext) == 0 {
		return errors.New("protection plaintext is required")
	}
	return request.Scope.Validate()
}

// Bytes returns a defensive plaintext copy for an explicit protection implementation.
func (request Request) Bytes() []byte {
	return append([]byte(nil), request.plaintext...)
}

// Destroy overwrites and releases the request-owned plaintext copy.
func (request *Request) Destroy() {
	if request == nil {
		return
	}
	for index := range request.plaintext {
		request.plaintext[index] = 0
	}
	request.plaintext = nil
}

// String returns a redacted request representation without plaintext or a reversible digest.
func (request Request) String() string {
	return fmt.Sprintf(
		"protection_request[project=%s,application=%s,purpose=%s,size=%d]",
		request.Scope.ProjectID,
		request.Scope.ApplicationID,
		request.Scope.Purpose,
		len(request.plaintext),
	)
}

// GoString returns a redacted protection request for detailed debug formatting.
func (request Request) GoString() string {
	return request.String()
}

// LogValue returns safe structured scope and plaintext-size metadata.
func (request Request) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("project_id", string(request.Scope.ProjectID)),
		slog.String("application_id", string(request.Scope.ApplicationID)),
		slog.String("purpose", request.Scope.Purpose),
		slog.Int("plaintext_size", len(request.plaintext)),
	)
}

// Validate checks the opening scope and protected value metadata.
func (request OpenRequest) Validate() error {
	return errors.Join(request.Scope.Validate(), request.Value.Validate())
}

// String returns a redacted opening request representation.
func (request OpenRequest) String() string {
	return fmt.Sprintf(
		"open_request[project=%s,application=%s,purpose=%s,value=%s]",
		request.Scope.ProjectID,
		request.Scope.ApplicationID,
		request.Scope.Purpose,
		request.Value,
	)
}

// GoString returns a redacted opening request for detailed debug formatting.
func (request OpenRequest) GoString() string {
	return request.String()
}

// LogValue returns safe structured scope and protected-value metadata.
func (request OpenRequest) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("project_id", string(request.Scope.ProjectID)),
		slog.String("application_id", string(request.Scope.ApplicationID)),
		slog.String("purpose", request.Scope.Purpose),
		slog.Any("value", request.Value),
	)
}

// validateText enforces non-empty, trimmed, control-free metadata.
func validateText(name, value string) error {
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
