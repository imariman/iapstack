package protection_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	platformprotection "github.com/imariman/iapstack/internal/platform/protection"
	protectionport "github.com/imariman/iapstack/internal/protection"
)

const (
	// protectionProjectID identifies the project bound into test authentication data.
	protectionProjectID = "project-1"
	// protectionApplicationID identifies the application bound into test authentication data.
	protectionApplicationID = "application-1"
	// alternateApplicationID provides a distinct application scope for isolation tests.
	alternateApplicationID = "application-2"
	// protectionPurpose identifies the semantic use bound into test authentication data.
	protectionPurpose = "purchase_evidence"
	// alternatePurpose provides a distinct semantic scope for isolation tests.
	alternatePurpose = "provider_reference"
	// oldEncryptionKeyID identifies a retained key used before rotation.
	oldEncryptionKeyID = "encryption-2026-01"
	// activeEncryptionKeyID identifies the active key used after rotation.
	activeEncryptionKeyID = "encryption-2026-08"
	// secretPlaintext is the sensitive marker that errors and logs must never expose.
	secretPlaintext = "receipt-token-never-log"
	// concurrentOperationCount exercises immutable keyring use across goroutines.
	concurrentOperationCount = 32
)

// keyMaterial contains deterministic root keys used only by unit tests.
type keyMaterial struct {
	old         []byte
	active      []byte
	fingerprint []byte
}

// TestKeyringProtectOpen verifies random nonces, deterministic fingerprints, and defensive copies.
func TestKeyringProtectOpen(t *testing.T) {
	t.Parallel()

	keyring := newTestKeyring(t, testConfig(t, activeEncryptionKeyID))
	original := []byte(secretPlaintext)
	request := newProtectionRequest(t, protectionScope(), original)
	original[0] = 'X'

	requestBytes := request.Bytes()
	requestBytes[0] = 'Y'
	first, err := keyring.Protect(context.Background(), request)
	if err != nil {
		t.Fatalf("first Protect() error = %v", err)
	}
	second, err := keyring.Protect(context.Background(), request)
	if err != nil {
		t.Fatalf("second Protect() error = %v", err)
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("Protect() ciphertexts are equal, want independently generated nonces")
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("Protect() fingerprints differ for identical scoped plaintext")
	}
	if first.KeyID != activeEncryptionKeyID || second.KeyID != activeEncryptionKeyID {
		t.Fatalf("Protect() key IDs = %q and %q, want %q", first.KeyID, second.KeyID, activeEncryptionKeyID)
	}

	openRequest := newOpenRequest(t, protectionScope(), first)
	first.Ciphertext[1] ^= 0xff
	plaintext, err := keyring.Open(context.Background(), openRequest)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if string(plaintext) != secretPlaintext {
		t.Fatalf("Open() plaintext = %q, want original protected value", plaintext)
	}
}

// TestNewConfigDefensivelyCopiesSecrets verifies caller mutations cannot change derived keys.
func TestNewConfigDefensivelyCopiesSecrets(t *testing.T) {
	t.Parallel()

	material := testKeyMaterial()
	callerKeys := map[string][]byte{activeEncryptionKeyID: material.active}
	callerFingerprintKey := material.fingerprint
	config := newTestConfig(t, activeEncryptionKeyID, callerKeys, callerFingerprintKey)
	for index := range callerKeys[activeEncryptionKeyID] {
		callerKeys[activeEncryptionKeyID][index] = 0
		callerFingerprintKey[index] = 0
	}
	keyIDs := config.KeyIDs()
	keyIDs[0] = "mutated-key-id"
	if config.KeyIDs()[0] != activeEncryptionKeyID {
		t.Fatal("KeyIDs() returned mutable configuration metadata")
	}

	keyring := newTestKeyring(t, config)
	expectedMaterial := testKeyMaterial()
	expectedKeyring := newTestKeyring(t, newTestConfig(
		t,
		activeEncryptionKeyID,
		map[string][]byte{activeEncryptionKeyID: expectedMaterial.active},
		expectedMaterial.fingerprint,
	))
	value := protectText(t, expectedKeyring, protectionScope(), secretPlaintext)
	plaintext, err := keyring.Open(context.Background(), newOpenRequest(t, protectionScope(), value))
	if err != nil {
		t.Fatalf("Open() after caller mutation error = %v", err)
	}
	if string(plaintext) != secretPlaintext {
		t.Fatalf("Open() after caller mutation plaintext = %q", plaintext)
	}
}

// TestKeyringScopesFingerprintAndAuthentication verifies project, application, and purpose isolation.
func TestKeyringScopesFingerprintAndAuthentication(t *testing.T) {
	t.Parallel()

	keyring := newTestKeyring(t, testConfig(t, activeEncryptionKeyID))
	baseScope := protectionScope()
	applicationScope := baseScope
	applicationScope.ApplicationID = alternateApplicationID
	purposeScope := baseScope
	purposeScope.Purpose = alternatePurpose

	base := protectText(t, keyring, baseScope, secretPlaintext)
	application := protectText(t, keyring, applicationScope, secretPlaintext)
	purpose := protectText(t, keyring, purposeScope, secretPlaintext)
	if base.Fingerprint == application.Fingerprint {
		t.Fatal("application-scoped fingerprints are equal, want isolation")
	}
	if base.Fingerprint == purpose.Fingerprint {
		t.Fatal("purpose-scoped fingerprints are equal, want isolation")
	}

	_, err := keyring.Open(context.Background(), newOpenRequest(t, applicationScope, base))
	if !errors.Is(err, protectionport.ErrOpenFailed) {
		t.Fatalf("Open() wrong scope error = %v, want ErrOpenFailed", err)
	}
}

// TestKeyringRotation verifies active writes, retained-key reads, and stable fingerprints.
func TestKeyringRotation(t *testing.T) {
	t.Parallel()

	oldKeyring := newTestKeyring(t, testConfig(t, oldEncryptionKeyID))
	rotatedKeyring := newTestKeyring(t, testConfig(t, activeEncryptionKeyID))
	scope := protectionScope()
	oldValue := protectText(t, oldKeyring, scope, secretPlaintext)
	newValue := protectText(t, rotatedKeyring, scope, secretPlaintext)

	if oldValue.KeyID != oldEncryptionKeyID || newValue.KeyID != activeEncryptionKeyID {
		t.Fatalf("rotation key IDs = %q and %q", oldValue.KeyID, newValue.KeyID)
	}
	if oldValue.Fingerprint != newValue.Fingerprint {
		t.Fatal("fingerprint changed during encryption-key rotation")
	}
	for _, value := range []protectionport.Value{oldValue, newValue} {
		plaintext, err := rotatedKeyring.Open(context.Background(), newOpenRequest(t, scope, value))
		if err != nil {
			t.Fatalf("rotated Open(%q) error = %v", value.KeyID, err)
		}
		if string(plaintext) != secretPlaintext {
			t.Fatalf("rotated Open(%q) plaintext = %q", value.KeyID, plaintext)
		}
	}

	material := testKeyMaterial()
	activeOnlyConfig := newTestConfig(t, activeEncryptionKeyID, map[string][]byte{
		activeEncryptionKeyID: material.active,
	}, material.fingerprint)
	activeOnlyKeyring := newTestKeyring(t, activeOnlyConfig)
	_, err := activeOnlyKeyring.Open(context.Background(), newOpenRequest(t, scope, oldValue))
	if !errors.Is(err, protectionport.ErrKeyUnavailable) {
		t.Fatalf("Open() removed key error = %v, want ErrKeyUnavailable", err)
	}
}

// TestKeyringRejectsTamperingAndWrongKey verifies generic authentication failures.
func TestKeyringRejectsTamperingAndWrongKey(t *testing.T) {
	t.Parallel()

	config := testConfig(t, activeEncryptionKeyID)
	keyring := newTestKeyring(t, config)
	scope := protectionScope()
	valid := protectText(t, keyring, scope, secretPlaintext)

	tests := []struct {
		name    string
		keyring *platformprotection.Keyring
		mutate  func(protectionport.Value) protectionport.Value
	}{
		{
			name:    "ciphertext",
			keyring: keyring,
			mutate: func(value protectionport.Value) protectionport.Value {
				value.Ciphertext[len(value.Ciphertext)-1] ^= 0xff
				return value
			},
		},
		{
			name:    "fingerprint",
			keyring: keyring,
			mutate: func(value protectionport.Value) protectionport.Value {
				value.Fingerprint[0] ^= 0xff
				return value
			},
		},
		{
			name:    "known key ID",
			keyring: keyring,
			mutate: func(value protectionport.Value) protectionport.Value {
				value.KeyID = oldEncryptionKeyID
				return value
			},
		},
		{
			name: "wrong key material",
			keyring: newTestKeyring(t, newTestConfig(
				t,
				activeEncryptionKeyID,
				map[string][]byte{activeEncryptionKeyID: bytes.Repeat([]byte{0x7f}, 32)},
				testKeyMaterial().fingerprint,
			)),
			mutate: func(value protectionport.Value) protectionport.Value { return value },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			value := test.mutate(valid.Clone())
			_, err := test.keyring.Open(context.Background(), newOpenRequest(t, scope, value))
			if !errors.Is(err, protectionport.ErrOpenFailed) {
				t.Fatalf("Open() error = %v, want ErrOpenFailed", err)
			}
			if strings.Contains(err.Error(), secretPlaintext) {
				t.Fatalf("Open() error exposed plaintext: %v", err)
			}
		})
	}
}

// TestKeyringRedactsSecrets verifies safe string and structured logging behavior.
func TestKeyringRedactsSecrets(t *testing.T) {
	t.Parallel()

	config := testConfig(t, activeEncryptionKeyID)
	keyring := newTestKeyring(t, config)
	request := newProtectionRequest(t, protectionScope(), []byte(secretPlaintext))
	value, err := keyring.Protect(context.Background(), request)
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	openRequest := newOpenRequest(t, protectionScope(), value)

	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logger.Info("redaction", "config", config, "keyring", keyring, "request", request, "value", value, "open", openRequest)
	combined := strings.Join([]string{
		fmt.Sprint(config),
		fmt.Sprintf("%#v", config),
		fmt.Sprint(keyring),
		fmt.Sprintf("%#v", keyring),
		fmt.Sprint(request),
		fmt.Sprintf("%#v", request),
		fmt.Sprint(value),
		fmt.Sprintf("%#v", value),
		fmt.Sprint(openRequest),
		fmt.Sprintf("%#v", openRequest),
		output.String(),
	}, "\n")
	material := testKeyMaterial()
	for _, forbidden := range []string{
		secretPlaintext,
		base64.StdEncoding.EncodeToString(material.fingerprint),
		base64.StdEncoding.EncodeToString(material.active),
		fmt.Sprint(value.Ciphertext),
		fmt.Sprintf("%x", value.Fingerprint),
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("diagnostics exposed secret marker %q in %s", forbidden, combined)
		}
	}
}

// TestKeyringSupportsConcurrentUse verifies immutable keyring operations under the race detector.
func TestKeyringSupportsConcurrentUse(t *testing.T) {
	t.Parallel()

	keyring := newTestKeyring(t, testConfig(t, activeEncryptionKeyID))
	request := newProtectionRequest(t, protectionScope(), []byte(secretPlaintext))
	errorsChannel := make(chan error, concurrentOperationCount)
	var workers sync.WaitGroup
	for index := 0; index < concurrentOperationCount; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			value, err := keyring.Protect(context.Background(), request)
			if err != nil {
				errorsChannel <- err
				return
			}
			openRequest, err := protectionport.NewOpenRequest(protectionScope(), value)
			if err != nil {
				errorsChannel <- err
				return
			}
			plaintext, err := keyring.Open(context.Background(), openRequest)
			if err != nil {
				errorsChannel <- err
				return
			}
			if string(plaintext) != secretPlaintext {
				errorsChannel <- fmt.Errorf("unexpected plaintext size %d", len(plaintext))
			}
		}()
	}
	workers.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("concurrent protection error = %v", err)
	}
}

// testConfig returns a two-key configuration with a stable fingerprint root.
func testConfig(t *testing.T, activeKeyID string) platformprotection.Config {
	t.Helper()
	material := testKeyMaterial()
	return newTestConfig(
		t,
		activeKeyID,
		map[string][]byte{
			oldEncryptionKeyID:    material.old,
			activeEncryptionKeyID: material.active,
		},
		material.fingerprint,
	)
}

// testKeyMaterial returns deterministic and distinct 256-bit test root keys.
func testKeyMaterial() keyMaterial {
	return keyMaterial{
		old:         bytes.Repeat([]byte{0x11}, 32),
		active:      bytes.Repeat([]byte{0x22}, 32),
		fingerprint: bytes.Repeat([]byte{0x33}, 32),
	}
}

// protectionScope returns the canonical test scope.
func protectionScope() protectionport.Scope {
	return protectionport.Scope{
		ProjectID:     protectionProjectID,
		ApplicationID: protectionApplicationID,
		Purpose:       protectionPurpose,
	}
}

// newTestKeyring constructs a keyring or fails the current test.
func newTestKeyring(t *testing.T, config platformprotection.Config) *platformprotection.Keyring {
	t.Helper()
	keyring, err := platformprotection.New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return keyring
}

// newTestConfig constructs redaction-safe configuration or fails the current test.
func newTestConfig(
	t *testing.T,
	activeKeyID string,
	encryptionKeys map[string][]byte,
	fingerprintKey []byte,
) platformprotection.Config {
	t.Helper()
	config, err := platformprotection.NewConfig(activeKeyID, encryptionKeys, fingerprintKey)
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	return config
}

// newProtectionRequest constructs a validated request or fails the current test.
func newProtectionRequest(t *testing.T, scope protectionport.Scope, plaintext []byte) protectionport.Request {
	t.Helper()
	request, err := protectionport.NewRequest(scope, plaintext)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	return request
}

// newOpenRequest constructs a validated opening request or fails the current test.
func newOpenRequest(t *testing.T, scope protectionport.Scope, value protectionport.Value) protectionport.OpenRequest {
	t.Helper()
	request, err := protectionport.NewOpenRequest(scope, value)
	if err != nil {
		t.Fatalf("NewOpenRequest() error = %v", err)
	}
	return request
}

// protectText protects one string or fails the current test.
func protectText(
	t *testing.T,
	keyring *platformprotection.Keyring,
	scope protectionport.Scope,
	plaintext string,
) protectionport.Value {
	t.Helper()
	value, err := keyring.Protect(
		context.Background(),
		newProtectionRequest(t, scope, []byte(plaintext)),
	)
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	return value
}
