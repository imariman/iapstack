package stores_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/imariman/iapstack/internal/stores"
)

const (
	// credentialKind identifies one provider-owned credential package in tests.
	credentialKind stores.CredentialKind = "server_api"
	// credentialContentType identifies the versioned test credential document.
	credentialContentType = "application/vnd.iapstack.test-credentials+json"
	// credentialSchemaVersion identifies the initial test credential schema.
	credentialSchemaVersion = 1
	// credentialSecretMarker is the payload value that diagnostics must never expose.
	credentialSecretMarker = "credential-secret-never-log"
)

// TestCredentialIsOpaqueCopiedAndLogSafe verifies payload ownership and every diagnostic format.
func TestCredentialIsOpaqueCopiedAndLogSafe(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"client_secret":"` + credentialSecretMarker + `"}`)
	credential, err := stores.NewCredential(
		credentialKind,
		credentialContentType,
		credentialSchemaVersion,
		payload,
	)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	payload[0] = 'X'
	returned := credential.Bytes()
	returned[1] = 'Y'
	if !bytes.Contains(credential.Bytes(), []byte(credentialSecretMarker)) {
		t.Fatal("credential changed after mutating caller-owned copies")
	}

	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logger.Info("credential", "value", credential)
	diagnostics := strings.Join([]string{
		fmt.Sprint(credential),
		fmt.Sprintf("%#v", credential),
		output.String(),
	}, "\n")
	if strings.Contains(diagnostics, credentialSecretMarker) {
		t.Fatalf("credential diagnostics exposed payload: %s", diagnostics)
	}
}

// TestCredentialValidationRejectsIncompleteMetadata verifies provider package invariants.
func TestCredentialValidationRejectsIncompleteMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		kind          stores.CredentialKind
		contentType   string
		schemaVersion int
		payload       []byte
	}{
		{name: "kind", contentType: credentialContentType, schemaVersion: 1, payload: []byte("secret")},
		{name: "content type", kind: credentialKind, contentType: "not a media type", schemaVersion: 1, payload: []byte("secret")},
		{name: "schema version", kind: credentialKind, contentType: credentialContentType, payload: []byte("secret")},
		{name: "payload", kind: credentialKind, contentType: credentialContentType, schemaVersion: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := stores.NewCredential(
				test.kind,
				test.contentType,
				test.schemaVersion,
				test.payload,
			); err == nil {
				t.Fatal("NewCredential() error = nil, want validation error")
			}
		})
	}
}
