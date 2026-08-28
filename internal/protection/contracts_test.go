package protection_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/imariman/iapstack/internal/protection"
)

const (
	// contractKeyID identifies deterministic protected metadata in port tests.
	contractKeyID = "test-key"
	// contractPlaintext identifies sensitive request bytes used by defensive-copy tests.
	contractPlaintext = "sensitive-token"
)

// recordingProtector retains the request header so tests can inspect helper cleanup.
type recordingProtector struct {
	request protection.Request
}

// Protect records one request and returns deterministic protected metadata.
func (protector *recordingProtector) Protect(
	_ context.Context,
	request protection.Request,
) (protection.Value, error) {
	protector.request = request
	return protectedValue([]byte(contractPlaintext)), nil
}

// TestValueValidation verifies that protected values require complete envelope metadata.
func TestValueValidation(t *testing.T) {
	t.Parallel()

	valid := protectedValue([]byte("payload"))
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name  string
		value protection.Value
	}{
		{name: "ciphertext", value: protection.Value{Fingerprint: valid.Fingerprint, KeyID: contractKeyID}},
		{name: "fingerprint", value: protection.Value{Ciphertext: []byte("ciphertext"), KeyID: contractKeyID}},
		{name: "key ID", value: protection.Value{Ciphertext: []byte("ciphertext"), Fingerprint: valid.Fingerprint}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.value.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want validation error")
			}
		})
	}
}

// TestRequestDefensiveCopiesAndRedaction verifies the plaintext boundary cannot be mutated or formatted.
func TestRequestDefensiveCopiesAndRedaction(t *testing.T) {
	t.Parallel()

	original := []byte(contractPlaintext)
	request, err := protection.NewRequest(contractScope(), original)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	original[0] = 'X'
	firstCopy := request.Bytes()
	firstCopy[0] = 'Y'
	if string(request.Bytes()) != contractPlaintext {
		t.Fatalf("Bytes() did not preserve the defensively copied plaintext")
	}
	if strings.Contains(request.String(), contractPlaintext) {
		t.Fatalf("String() exposed plaintext: %s", request.String())
	}
	if formatted := fmt.Sprintf("%#v", request); strings.Contains(formatted, contractPlaintext) {
		t.Fatalf("detailed formatting exposed plaintext: %s", formatted)
	}
}

// TestProtectDestroysRequestCopy verifies the convenience boundary clears its private plaintext.
func TestProtectDestroysRequestCopy(t *testing.T) {
	t.Parallel()

	plaintext := []byte(contractPlaintext)
	protector := &recordingProtector{}
	if _, err := protection.Protect(context.Background(), protector, contractScope(), plaintext); err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	if string(plaintext) != contractPlaintext {
		t.Fatal("Protect() mutated caller-owned plaintext")
	}
	for _, value := range protector.request.Bytes() {
		if value != 0 {
			t.Fatal("Protect() retained request-owned plaintext after returning")
		}
	}
}

// TestOpenRequestDefensivelyCopiesValue verifies ciphertext ownership at the opening boundary.
func TestOpenRequestDefensivelyCopiesValue(t *testing.T) {
	t.Parallel()

	value := protectedValue([]byte(contractPlaintext))
	request, err := protection.NewOpenRequest(contractScope(), value)
	if err != nil {
		t.Fatalf("NewOpenRequest() error = %v", err)
	}
	value.Ciphertext[0] = 'X'
	if request.Value.Ciphertext[0] == 'X' {
		t.Fatal("NewOpenRequest() retained caller-owned ciphertext")
	}
	if strings.Contains(request.String(), contractPlaintext) {
		t.Fatalf("String() exposed protected data: %s", request.String())
	}
	if formatted := fmt.Sprintf("%#v", request); strings.Contains(formatted, contractPlaintext) {
		t.Fatalf("detailed formatting exposed protected data: %s", formatted)
	}
}

// contractScope returns one valid provider-neutral protection scope.
func contractScope() protection.Scope {
	return protection.Scope{
		ProjectID:     "project-1",
		ApplicationID: "application-1",
		Purpose:       "purchase_evidence",
	}
}

// protectedValue builds deterministic encrypted metadata without representing production encryption.
func protectedValue(plaintext []byte) protection.Value {
	return protection.Value{
		Ciphertext:  append([]byte("ciphertext:"), plaintext...),
		Fingerprint: sha256.Sum256(plaintext),
		KeyID:       contractKeyID,
	}
}
