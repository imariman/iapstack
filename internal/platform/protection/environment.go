package protection

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// activeKeyIDEnvironment names the encryption key selected for new writes.
	activeKeyIDEnvironment = "IAPSTACK_PROTECTION_ACTIVE_KEY_ID"
	// encryptionKeysEnvironment names the JSON object containing base64 encryption root keys.
	encryptionKeysEnvironment = "IAPSTACK_PROTECTION_KEYS"
	// fingerprintKeyEnvironment names the stable base64 fingerprint root key.
	fingerprintKeyEnvironment = "IAPSTACK_PROTECTION_FINGERPRINT_KEY"
)

// LoadConfigFromEnvironment parses and validates protection secrets without exposing their values.
func LoadConfigFromEnvironment(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, fmt.Errorf("%w: environment reader is required", ErrInvalidConfiguration)
	}

	activeKeyID := strings.TrimSpace(getenv(activeKeyIDEnvironment))
	if activeKeyID == "" {
		return Config{}, missingEnvironmentError(activeKeyIDEnvironment)
	}
	encodedKeys := strings.TrimSpace(getenv(encryptionKeysEnvironment))
	if encodedKeys == "" {
		return Config{}, missingEnvironmentError(encryptionKeysEnvironment)
	}
	encodedFingerprintKey := strings.TrimSpace(getenv(fingerprintKeyEnvironment))
	if encodedFingerprintKey == "" {
		return Config{}, missingEnvironmentError(fingerprintKeyEnvironment)
	}

	encryptionKeys, err := decodeEncryptionKeys(encodedKeys)
	if err != nil {
		return Config{}, err
	}
	defer zeroRootKeys(encryptionKeys)
	fingerprintKey, err := decodeRootKey(fingerprintKeyEnvironment, encodedFingerprintKey)
	if err != nil {
		return Config{}, err
	}
	defer zeroBytes(fingerprintKey)
	return NewConfig(activeKeyID, encryptionKeys, fingerprintKey)
}

// OpenFromEnvironment loads validated secrets and constructs an immutable protection keyring.
func OpenFromEnvironment(getenv func(string) string) (*Keyring, error) {
	config, err := LoadConfigFromEnvironment(getenv)
	if err != nil {
		return nil, err
	}
	keyring, err := New(config)
	zeroRootKeys(config.encryptionKeys)
	zeroBytes(config.fingerprintKey)
	return keyring, err
}

// decodeEncryptionKeys parses a duplicate-free JSON object of key IDs and base64 root keys.
func decodeEncryptionKeys(encoded string) (map[string][]byte, error) {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, malformedEnvironmentError(encryptionKeysEnvironment)
	}

	keys := make(map[string][]byte)
	completed := false
	defer func() {
		if !completed {
			zeroRootKeys(keys)
		}
	}()
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, malformedEnvironmentError(encryptionKeysEnvironment)
		}
		keyID, ok := token.(string)
		if !ok {
			return nil, malformedEnvironmentError(encryptionKeysEnvironment)
		}
		if _, duplicate := keys[keyID]; duplicate {
			return nil, fmt.Errorf("%w: %s contains a duplicate key ID", ErrInvalidConfiguration, encryptionKeysEnvironment)
		}
		var encodedKey string
		if err := decoder.Decode(&encodedKey); err != nil {
			return nil, malformedEnvironmentError(encryptionKeysEnvironment)
		}
		key, err := decodeRootKey(encryptionKeysEnvironment, encodedKey)
		if err != nil {
			return nil, err
		}
		keys[keyID] = key
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, malformedEnvironmentError(encryptionKeysEnvironment)
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return nil, malformedEnvironmentError(encryptionKeysEnvironment)
	}
	completed = true
	return keys, nil
}

// decodeRootKey decodes one strict base64 value and enforces the root-key byte length.
func decodeRootKey(environmentName, encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		zeroBytes(key)
		return nil, fmt.Errorf("%w: %s must contain strict base64", ErrInvalidConfiguration, environmentName)
	}
	if len(key) != keySize {
		zeroBytes(key)
		return nil, fmt.Errorf(
			"%w: %s keys must decode to %d bytes",
			ErrInvalidConfiguration,
			environmentName,
			keySize,
		)
	}
	return key, nil
}

// rejectTrailingJSON requires the encryption keyring to contain exactly one JSON value.
func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("trailing JSON value")
}

// missingEnvironmentError reports only the name of an absent secret variable.
func missingEnvironmentError(environmentName string) error {
	return fmt.Errorf("%w: %s is required", ErrInvalidConfiguration, environmentName)
}

// malformedEnvironmentError reports only the name of a malformed secret variable.
func malformedEnvironmentError(environmentName string) error {
	return fmt.Errorf("%w: %s is malformed", ErrInvalidConfiguration, environmentName)
}
