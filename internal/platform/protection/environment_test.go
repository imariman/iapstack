package protection_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	platformprotection "github.com/imariman/iapstack/internal/platform/protection"
)

const (
	// activeKeyIDEnvironmentName is the public environment contract for the write key.
	activeKeyIDEnvironmentName = "IAPSTACK_PROTECTION_ACTIVE_KEY_ID"
	// encryptionKeysEnvironmentName is the public environment contract for encryption keys.
	encryptionKeysEnvironmentName = "IAPSTACK_PROTECTION_KEYS"
	// encryptionKeyEnvironmentName is the public environment contract for one encryption key.
	encryptionKeyEnvironmentName = "IAPSTACK_PROTECTION_KEY"
	// fingerprintKeyEnvironmentName is the public environment contract for the fingerprint key.
	fingerprintKeyEnvironmentName = "IAPSTACK_PROTECTION_FINGERPRINT_KEY"
	// environmentSecretMarker identifies malformed secret text that errors must redact.
	environmentSecretMarker = "environment-secret-never-expose"
)

// TestLoadConfigFromEnvironment verifies strict parsing and successful keyring construction.
func TestLoadConfigFromEnvironment(t *testing.T) {
	t.Parallel()

	environment := validProtectionEnvironment()
	config, err := platformprotection.LoadConfigFromEnvironment(environmentGetter(environment))
	if err != nil {
		t.Fatalf("LoadConfigFromEnvironment() error = %v", err)
	}
	if config.ActiveKeyID() != activeEncryptionKeyID {
		t.Fatalf("ActiveKeyID() = %q, want %q", config.ActiveKeyID(), activeEncryptionKeyID)
	}
	if len(config.KeyIDs()) != 2 {
		t.Fatalf("KeyIDs() count = %d, want 2", len(config.KeyIDs()))
	}

	keyring, err := platformprotection.OpenFromEnvironment(environmentGetter(environment))
	if err != nil {
		t.Fatalf("OpenFromEnvironment() error = %v", err)
	}
	value := protectText(t, keyring, protectionScope(), secretPlaintext)
	plaintext, err := keyring.Open(context.Background(), newOpenRequest(t, protectionScope(), value))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if string(plaintext) != secretPlaintext {
		t.Fatalf("Open() plaintext = %q", plaintext)
	}
}

// TestLoadConfigFromEnvironmentHexKeys verifies 32-byte hex platform generators.
func TestLoadConfigFromEnvironmentHexKeys(t *testing.T) {
	t.Parallel()

	material := testKeyMaterial()
	environment := map[string]string{
		activeKeyIDEnvironmentName:    activeEncryptionKeyID,
		encryptionKeyEnvironmentName:  hex.EncodeToString(material.active),
		fingerprintKeyEnvironmentName: hex.EncodeToString(material.fingerprint),
	}
	keyring, err := platformprotection.OpenFromEnvironment(environmentGetter(environment))
	if err != nil {
		t.Fatalf("OpenFromEnvironment() error = %v", err)
	}
	value := protectText(t, keyring, protectionScope(), secretPlaintext)
	plaintext, err := keyring.Open(context.Background(), newOpenRequest(t, protectionScope(), value))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if string(plaintext) != secretPlaintext {
		t.Fatalf("Open() plaintext = %q", plaintext)
	}
}

// TestLoadConfigFromEnvironmentSingleKey verifies the managed-platform convenience form.
func TestLoadConfigFromEnvironmentSingleKey(t *testing.T) {
	t.Parallel()

	material := testKeyMaterial()
	environment := map[string]string{
		activeKeyIDEnvironmentName:    activeEncryptionKeyID,
		encryptionKeyEnvironmentName:  base64.StdEncoding.EncodeToString(material.active),
		fingerprintKeyEnvironmentName: base64.StdEncoding.EncodeToString(material.fingerprint),
	}
	config, err := platformprotection.LoadConfigFromEnvironment(environmentGetter(environment))
	if err != nil {
		t.Fatalf("LoadConfigFromEnvironment() error = %v", err)
	}
	if config.ActiveKeyID() != activeEncryptionKeyID {
		t.Fatalf("ActiveKeyID() = %q, want %q", config.ActiveKeyID(), activeEncryptionKeyID)
	}
	if keyIDs := config.KeyIDs(); len(keyIDs) != 1 || keyIDs[0] != activeEncryptionKeyID {
		t.Fatalf("KeyIDs() = %v, want [%s]", keyIDs, activeEncryptionKeyID)
	}
}

// TestLoadConfigFromEnvironmentRejectsInvalidSecrets verifies fail-fast redacted validation.
func TestLoadConfigFromEnvironmentRejectsInvalidSecrets(t *testing.T) {
	t.Parallel()

	valid := validProtectionEnvironment()
	shortKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 31))
	validKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, 32))
	tests := []struct {
		name      string
		getenv    func(string) string
		forbidden []string
	}{
		{
			name:      "nil environment reader",
			getenv:    nil,
			forbidden: []string{environmentSecretMarker},
		},
		{
			name: "missing active key ID",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				activeKeyIDEnvironmentName,
				"",
			)),
			forbidden: secretEnvironmentValues(valid),
		},
		{
			name: "missing encryption keys",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				encryptionKeysEnvironmentName,
				"",
			)),
			forbidden: secretEnvironmentValues(valid),
		},
		{
			name: "both encryption forms",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				encryptionKeyEnvironmentName,
				validKey,
			)),
			forbidden: append(secretEnvironmentValues(valid), validKey),
		},
		{
			name: "invalid single encryption base64",
			getenv: environmentGetter(map[string]string{
				activeKeyIDEnvironmentName:    activeEncryptionKeyID,
				encryptionKeyEnvironmentName:  environmentSecretMarker,
				fingerprintKeyEnvironmentName: valid[fingerprintKeyEnvironmentName],
			}),
			forbidden: []string{environmentSecretMarker},
		},
		{
			name: "short single encryption key",
			getenv: environmentGetter(map[string]string{
				activeKeyIDEnvironmentName:    activeEncryptionKeyID,
				encryptionKeyEnvironmentName:  shortKey,
				fingerprintKeyEnvironmentName: valid[fingerprintKeyEnvironmentName],
			}),
			forbidden: []string{shortKey},
		},
		{
			name: "missing fingerprint key",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				fingerprintKeyEnvironmentName,
				"",
			)),
			forbidden: secretEnvironmentValues(valid),
		},
		{
			name: "malformed encryption JSON",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				encryptionKeysEnvironmentName,
				environmentSecretMarker,
			)),
			forbidden: []string{environmentSecretMarker},
		},
		{
			name: "non-object encryption JSON",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				encryptionKeysEnvironmentName,
				fmt.Sprintf("[%q]", environmentSecretMarker),
			)),
			forbidden: []string{environmentSecretMarker},
		},
		{
			name: "duplicate encryption key ID",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				encryptionKeysEnvironmentName,
				fmt.Sprintf("{%q:%q,%q:%q}", activeEncryptionKeyID, validKey, activeEncryptionKeyID, validKey),
			)),
			forbidden: []string{validKey},
		},
		{
			name: "trailing encryption JSON",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				encryptionKeysEnvironmentName,
				fmt.Sprintf("{%q:%q} %q", activeEncryptionKeyID, validKey, environmentSecretMarker),
			)),
			forbidden: []string{validKey, environmentSecretMarker},
		},
		{
			name: "invalid encryption base64",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				encryptionKeysEnvironmentName,
				fmt.Sprintf("{%q:%q}", activeEncryptionKeyID, environmentSecretMarker),
			)),
			forbidden: []string{environmentSecretMarker},
		},
		{
			name: "short encryption key",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				encryptionKeysEnvironmentName,
				fmt.Sprintf("{%q:%q}", activeEncryptionKeyID, shortKey),
			)),
			forbidden: []string{shortKey},
		},
		{
			name: "invalid fingerprint base64",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				fingerprintKeyEnvironmentName,
				environmentSecretMarker,
			)),
			forbidden: []string{environmentSecretMarker},
		},
		{
			name: "short fingerprint key",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				fingerprintKeyEnvironmentName,
				shortKey,
			)),
			forbidden: []string{shortKey},
		},
		{
			name: "unknown active key",
			getenv: environmentGetter(withEnvironmentValue(
				valid,
				activeKeyIDEnvironmentName,
				"unknown-key",
			)),
			forbidden: secretEnvironmentValues(valid),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := platformprotection.LoadConfigFromEnvironment(test.getenv)
			if err == nil {
				t.Fatal("LoadConfigFromEnvironment() error = nil, want validation error")
			}
			for _, forbidden := range test.forbidden {
				if forbidden != "" && strings.Contains(err.Error(), forbidden) {
					t.Fatalf("LoadConfigFromEnvironment() error exposed secret %q: %v", forbidden, err)
				}
			}
		})
	}
}

// validProtectionEnvironment returns a complete environment using deterministic test secrets.
func validProtectionEnvironment() map[string]string {
	material := testKeyMaterial()
	return map[string]string{
		activeKeyIDEnvironmentName: activeEncryptionKeyID,
		encryptionKeysEnvironmentName: fmt.Sprintf(
			"{%q:%q,%q:%q}",
			oldEncryptionKeyID,
			base64.StdEncoding.EncodeToString(material.old),
			activeEncryptionKeyID,
			base64.StdEncoding.EncodeToString(material.active),
		),
		fingerprintKeyEnvironmentName: base64.StdEncoding.EncodeToString(material.fingerprint),
	}
}

// environmentGetter returns a process-style lookup function over one immutable test map.
func environmentGetter(environment map[string]string) func(string) string {
	return func(name string) string {
		return environment[name]
	}
}

// withEnvironmentValue clones an environment and replaces one value.
func withEnvironmentValue(environment map[string]string, name, value string) map[string]string {
	clone := make(map[string]string, len(environment))
	for existingName, existingValue := range environment {
		clone[existingName] = existingValue
	}
	clone[name] = value
	return clone
}

// secretEnvironmentValues returns only encoded key material for redaction assertions.
func secretEnvironmentValues(environment map[string]string) []string {
	return []string{
		environment[encryptionKeysEnvironmentName],
		environment[fingerprintKeyEnvironmentName],
	}
}
