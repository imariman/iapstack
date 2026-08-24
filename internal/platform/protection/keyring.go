// Package protection implements authenticated, scoped data protection with rotating keys.
package protection

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"unicode"

	protectionport "github.com/imariman/iapstack/internal/protection"
)

const (
	// keySize is the required byte length for every root key.
	keySize = 32
	// envelopeVersion identifies the authenticated ciphertext format.
	envelopeVersion byte = 1
	// encryptionKDFInfo separates encryption subkeys from every other derived key.
	encryptionKDFInfo = "iapstack/protection/encryption/v1/"
	// fingerprintKDFInfo separates the stable fingerprint subkey from encryption keys.
	fingerprintKDFInfo = "iapstack/protection/fingerprint/v1"
	// authenticatedDataDomain separates envelope authentication from fingerprint inputs.
	authenticatedDataDomain = "iapstack/protection/aad/v1"
	// fingerprintDomain separates scoped fingerprints from other HMAC uses.
	fingerprintDomain = "iapstack/protection/fingerprint-input/v1"
)

// Config contains an active encryption keyring and a separate stable fingerprint root key.
type Config struct {
	activeKeyID    string
	encryptionKeys map[string][]byte
	fingerprintKey []byte
}

// Keyring protects new data with one active key and opens data with every retained key.
type Keyring struct {
	activeKeyID    string
	encryptionKeys map[string]cipher.AEAD
	fingerprintKey []byte
}

var (
	// ErrInvalidConfiguration indicates missing or malformed non-secret keyring metadata.
	ErrInvalidConfiguration = errors.New("invalid protection configuration")
	// _ verifies that Keyring implements the complete provider-neutral protection service.
	_ protectionport.Service = (*Keyring)(nil)
)

// New validates root keys, derives purpose-specific subkeys, and builds an immutable keyring.
func New(config Config) (*Keyring, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}

	encryptionKeys := make(map[string]cipher.AEAD, len(config.encryptionKeys))
	for keyID, rootKey := range config.encryptionKeys {
		derivedKey, err := hkdf.Key(sha256.New, rootKey, nil, encryptionKDFInfo+keyID, keySize)
		if err != nil {
			return nil, fmt.Errorf("derive encryption key %q: %w", keyID, err)
		}
		block, err := aes.NewCipher(derivedKey)
		zeroBytes(derivedKey)
		if err != nil {
			return nil, fmt.Errorf("initialize encryption key %q: %w", keyID, err)
		}
		aead, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			return nil, fmt.Errorf("initialize authenticated encryption key %q: %w", keyID, err)
		}
		encryptionKeys[keyID] = aead
	}

	fingerprintKey, err := hkdf.Key(
		sha256.New,
		config.fingerprintKey,
		nil,
		fingerprintKDFInfo,
		keySize,
	)
	if err != nil {
		return nil, fmt.Errorf("derive fingerprint key: %w", err)
	}
	return &Keyring{
		activeKeyID:    config.activeKeyID,
		encryptionKeys: encryptionKeys,
		fingerprintKey: fingerprintKey,
	}, nil
}

// NewConfig validates and defensively copies root keys into a redaction-safe configuration.
func NewConfig(activeKeyID string, encryptionKeys map[string][]byte, fingerprintKey []byte) (Config, error) {
	config := Config{
		activeKeyID:    activeKeyID,
		encryptionKeys: cloneRootKeys(encryptionKeys),
		fingerprintKey: append([]byte(nil), fingerprintKey...),
	}
	if err := validateConfig(config); err != nil {
		zeroRootKeys(config.encryptionKeys)
		zeroBytes(config.fingerprintKey)
		return Config{}, err
	}
	return config, nil
}

// validateConfig checks key identities, membership, and root-key sizes without deriving secrets.
func validateConfig(config Config) error {
	if err := validateKeyID(config.activeKeyID); err != nil {
		return fmt.Errorf("%w: active key ID: %v", ErrInvalidConfiguration, err)
	}
	if len(config.encryptionKeys) == 0 {
		return fmt.Errorf("%w: at least one encryption key is required", ErrInvalidConfiguration)
	}
	if len(config.fingerprintKey) != keySize {
		return fmt.Errorf("%w: fingerprint key must be %d bytes", ErrInvalidConfiguration, keySize)
	}
	if _, exists := config.encryptionKeys[config.activeKeyID]; !exists {
		return fmt.Errorf("%w: active key is absent from the encryption keyring", ErrInvalidConfiguration)
	}
	for keyID, rootKey := range config.encryptionKeys {
		if err := validateKeyID(keyID); err != nil {
			return fmt.Errorf("%w: encryption key ID: %v", ErrInvalidConfiguration, err)
		}
		if len(rootKey) != keySize {
			return fmt.Errorf("%w: encryption key %q must be %d bytes", ErrInvalidConfiguration, keyID, keySize)
		}
	}
	return nil
}

// Protect encrypts plaintext with a random nonce and computes a stable scoped fingerprint.
func (keyring *Keyring) Protect(ctx context.Context, request protectionport.Request) (protectionport.Value, error) {
	if err := request.Validate(); err != nil {
		return protectionport.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return protectionport.Value{}, fmt.Errorf("protect data: %w", err)
	}

	plaintext := request.Bytes()
	authenticatedData := envelopeAuthenticatedData(request.Scope, keyring.activeKeyID)
	sealed := keyring.encryptionKeys[keyring.activeKeyID].Seal(nil, nil, plaintext, authenticatedData)
	ciphertext := make([]byte, 1, 1+len(sealed))
	ciphertext[0] = envelopeVersion
	ciphertext = append(ciphertext, sealed...)
	fingerprint := keyring.fingerprint(request.Scope, plaintext)
	zeroBytes(plaintext)

	return protectionport.Value{
		Ciphertext:  ciphertext,
		Fingerprint: fingerprint,
		KeyID:       keyring.activeKeyID,
	}, nil
}

// Open authenticates scope, key identity, ciphertext, and fingerprint before returning plaintext.
func (keyring *Keyring) Open(ctx context.Context, request protectionport.OpenRequest) ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open protected data: %w", err)
	}

	aead, exists := keyring.encryptionKeys[request.Value.KeyID]
	if !exists {
		return nil, protectionport.ErrKeyUnavailable
	}
	if len(request.Value.Ciphertext) < 2 || request.Value.Ciphertext[0] != envelopeVersion {
		return nil, protectionport.ErrOpenFailed
	}
	authenticatedData := envelopeAuthenticatedData(request.Scope, request.Value.KeyID)
	plaintext, err := aead.Open(nil, nil, request.Value.Ciphertext[1:], authenticatedData)
	if err != nil {
		return nil, protectionport.ErrOpenFailed
	}
	expectedFingerprint := keyring.fingerprint(request.Scope, plaintext)
	if !hmac.Equal(expectedFingerprint[:], request.Value.Fingerprint[:]) {
		zeroBytes(plaintext)
		return nil, protectionport.ErrOpenFailed
	}
	return plaintext, nil
}

// String returns non-secret keyring metadata suitable for diagnostics.
func (config Config) String() string {
	keyIDs := sortedKeyIDs(config.encryptionKeys)
	return fmt.Sprintf("protection_config[active_key_id=%s,key_ids=%v]", config.activeKeyID, keyIDs)
}

// GoString returns redacted configuration metadata for detailed debug formatting.
func (config Config) GoString() string {
	return config.String()
}

// LogValue returns non-secret configuration metadata without root key bytes.
func (config Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("active_key_id", config.activeKeyID),
		slog.Any("key_ids", sortedKeyIDs(config.encryptionKeys)),
	)
}

// ActiveKeyID returns the non-secret key identity selected for new writes.
func (config Config) ActiveKeyID() string {
	return config.activeKeyID
}

// KeyIDs returns a sorted defensive copy of non-secret encryption key identities.
func (config Config) KeyIDs() []string {
	return sortedKeyIDs(config.encryptionKeys)
}

// String returns non-secret immutable keyring metadata suitable for diagnostics.
func (keyring *Keyring) String() string {
	if keyring == nil {
		return ""
	}
	return fmt.Sprintf(
		"protection_keyring[active_key_id=%s,key_ids=%v]",
		keyring.activeKeyID,
		sortedAEADKeyIDs(keyring.encryptionKeys),
	)
}

// GoString returns redacted immutable keyring metadata for detailed debug formatting.
func (keyring *Keyring) GoString() string {
	return keyring.String()
}

// LogValue returns non-secret immutable keyring metadata without derived key bytes.
func (keyring *Keyring) LogValue() slog.Value {
	if keyring == nil {
		return slog.StringValue("")
	}
	return slog.GroupValue(
		slog.String("active_key_id", keyring.activeKeyID),
		slog.Any("key_ids", sortedAEADKeyIDs(keyring.encryptionKeys)),
	)
}

// fingerprint computes a deterministic HMAC over unambiguous scope fields and plaintext.
func (keyring *Keyring) fingerprint(scope protectionport.Scope, plaintext []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, keyring.fingerprintKey)
	_, _ = mac.Write(scopedInput(fingerprintDomain, scope))
	_, _ = mac.Write(plaintext)
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], mac.Sum(nil))
	return fingerprint
}

// envelopeAuthenticatedData binds ciphertext to its version, scope, purpose, and key identity.
func envelopeAuthenticatedData(scope protectionport.Scope, keyID string) []byte {
	data := scopedInput(authenticatedDataDomain, scope)
	data = appendLengthPrefixed(data, keyID)
	return data
}

// scopedInput encodes one domain and scope without delimiter ambiguity.
func scopedInput(domain string, scope protectionport.Scope) []byte {
	data := make([]byte, 1, 128)
	data[0] = envelopeVersion
	data = appendLengthPrefixed(data, domain)
	data = appendLengthPrefixed(data, string(scope.ProjectID))
	data = appendLengthPrefixed(data, string(scope.ApplicationID))
	data = appendLengthPrefixed(data, scope.Purpose)
	return data
}

// appendLengthPrefixed appends one string with an unambiguous byte length prefix.
func appendLengthPrefixed(destination []byte, value string) []byte {
	destination = binary.AppendUvarint(destination, uint64(len(value)))
	return append(destination, value...)
}

// validateKeyID enforces non-empty, trimmed, control-free encryption key identifiers.
func validateKeyID(keyID string) error {
	if keyID == "" {
		return errors.New("is required")
	}
	if strings.TrimSpace(keyID) != keyID {
		return errors.New("must not have leading or trailing whitespace")
	}
	for _, character := range keyID {
		if unicode.IsControl(character) {
			return errors.New("must not contain control characters")
		}
	}
	return nil
}

// sortedKeyIDs returns deterministic non-secret key metadata from root-key configuration.
func sortedKeyIDs(keys map[string][]byte) []string {
	keyIDs := make([]string, 0, len(keys))
	for keyID := range keys {
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	return keyIDs
}

// sortedAEADKeyIDs returns deterministic non-secret key metadata from an immutable keyring.
func sortedAEADKeyIDs(keys map[string]cipher.AEAD) []string {
	keyIDs := make([]string, 0, len(keys))
	for keyID := range keys {
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	return keyIDs
}

// cloneRootKeys returns a deep copy of caller-owned root key material.
func cloneRootKeys(keys map[string][]byte) map[string][]byte {
	clone := make(map[string][]byte, len(keys))
	for keyID, key := range keys {
		clone[keyID] = append([]byte(nil), key...)
	}
	return clone
}

// zeroRootKeys overwrites every root key in a temporary map.
func zeroRootKeys(keys map[string][]byte) {
	for _, key := range keys {
		zeroBytes(key)
	}
}

// zeroBytes overwrites a temporary secret byte slice after use.
func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
