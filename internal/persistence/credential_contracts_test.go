package persistence_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
)

const (
	// persistenceCredentialKind identifies one provider-owned credential package.
	persistenceCredentialKind = "server_api"
	// persistenceCredentialContentType identifies the test credential document.
	persistenceCredentialContentType = "application/json"
	// persistenceCredentialKeyID identifies deterministic protected credential metadata.
	persistenceCredentialKeyID = "test-key"
)

// TestCredentialContractsValidateRevisionAndScope verifies write and record invariants.
func TestCredentialContractsValidateRevisionAndScope(t *testing.T) {
	t.Parallel()

	write := persistence.CredentialWrite{
		CredentialKey: persistence.CredentialKey{
			ProjectID:     "project-1",
			ApplicationID: "application-1",
			Kind:          persistenceCredentialKind,
		},
		ContentType:      persistenceCredentialContentType,
		SchemaVersion:    1,
		Payload:          persistenceProtectedCredential("secret"),
		ExpectedRevision: 0,
	}
	if err := write.Validate(); err != nil {
		t.Fatalf("CredentialWrite.Validate() error = %v", err)
	}

	write.ExpectedRevision = -1
	if err := write.Validate(); err == nil {
		t.Fatal("CredentialWrite.Validate() error = nil, want negative revision error")
	}
	now := time.Now().UTC()
	record := persistence.CredentialRecord{
		CredentialKey: write.CredentialKey,
		ContentType:   write.ContentType,
		SchemaVersion: write.SchemaVersion,
		Payload:       write.Payload,
		Revision:      1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("CredentialRecord.Validate() error = %v", err)
	}
	record.UpdatedAt = now.Add(-time.Second)
	if err := record.Validate(); err == nil {
		t.Fatal("CredentialRecord.Validate() error = nil, want timestamp order error")
	}
}

// TestCredentialRecordCloneOwnsCiphertext verifies defensive protected-value copying.
func TestCredentialRecordCloneOwnsCiphertext(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	record := persistence.CredentialRecord{
		CredentialKey: persistence.CredentialKey{
			ProjectID:     "project-1",
			ApplicationID: "application-1",
			Kind:          persistenceCredentialKind,
		},
		ContentType:   persistenceCredentialContentType,
		SchemaVersion: 1,
		Payload:       persistenceProtectedCredential("secret"),
		Revision:      1,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	clone := record.Clone()
	clone.Payload.Ciphertext[0] ^= 0xff
	if clone.Payload.Ciphertext[0] == record.Payload.Ciphertext[0] {
		t.Fatal("CredentialRecord.Clone() retained caller-owned ciphertext")
	}
}

// persistenceProtectedCredential returns deterministic protected metadata for contract tests.
func persistenceProtectedCredential(plaintext string) protection.Value {
	return protection.Value{
		Ciphertext:  []byte("ciphertext:" + plaintext),
		Fingerprint: sha256.Sum256([]byte(plaintext)),
		KeyID:       persistenceCredentialKeyID,
	}
}
