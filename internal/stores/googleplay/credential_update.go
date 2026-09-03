package googleplay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/imariman/iapstack/internal/stores"
)

// WithRTDN returns a Google Play credential revision with only its authenticated
// Pub/Sub configuration changed. Existing service-account material remains opaque.
func WithRTDN(
	credential stores.Credential,
	subscription string,
	pushServiceAccountEmail string,
	audience string,
) (stores.Credential, error) {
	if credential.Kind != CredentialKind || credential.ContentType != CredentialContentType ||
		credential.SchemaVersion != CredentialSchemaVersion {
		return stores.Credential{}, errors.New("Google Play credential metadata is invalid")
	}
	configuration := rtdnConfiguration{
		Subscription: subscription, PushServiceAccountEmail: pushServiceAccountEmail, Audience: audience,
	}
	if err := configuration.Validate(); err != nil {
		return stores.Credential{}, err
	}

	payload := credential.Bytes()
	defer zero(payload)
	var document map[string]json.RawMessage
	if err := decodeStrict(payload, &document); err != nil {
		return stores.Credential{}, fmt.Errorf("decode Google Play credential: %w", err)
	}
	defer func() {
		for _, value := range document {
			zero(value)
		}
	}()
	allowed := map[string]bool{"client_email": true, "private_key_id": true, "private_key": true, "rtdn": true}
	for key := range document {
		if !allowed[key] {
			return stores.Credential{}, fmt.Errorf("Google Play credential contains unknown field %q", key)
		}
	}
	for _, key := range []string{"client_email", "private_key_id", "private_key"} {
		value, exists := document[key]
		if !exists || len(bytes.TrimSpace(value)) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return stores.Credential{}, fmt.Errorf("Google Play credential field %q is required", key)
		}
	}

	rtdn, err := json.Marshal(configuration)
	if err != nil {
		return stores.Credential{}, fmt.Errorf("encode Google Play RTDN configuration: %w", err)
	}
	document["rtdn"] = rtdn
	updated, err := json.Marshal(document)
	if err != nil {
		return stores.Credential{}, fmt.Errorf("encode Google Play credential: %w", err)
	}
	defer zero(updated)
	return stores.NewCredential(CredentialKind, CredentialContentType, CredentialSchemaVersion, updated)
}
