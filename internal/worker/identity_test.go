package worker

import (
	"strings"
	"testing"
)

// TestNewIdentityPreservesConfiguredValue verifies stable operator-defined worker ownership.
func TestNewIdentityPreservesConfiguredValue(t *testing.T) {
	identity, err := NewIdentity("worker-configured")
	if err != nil {
		t.Fatalf("NewIdentity() error = %v", err)
	}
	if identity != "worker-configured" {
		t.Fatalf("NewIdentity() = %q, want worker-configured", identity)
	}
}

// TestNewIdentityGeneratesDistinctFallbacks verifies multiple local workers cannot share a default lease identity.
func TestNewIdentityGeneratesDistinctFallbacks(t *testing.T) {
	first, err := NewIdentity("")
	if err != nil {
		t.Fatalf("first NewIdentity() error = %v", err)
	}
	second, err := NewIdentity("   ")
	if err != nil {
		t.Fatalf("second NewIdentity() error = %v", err)
	}
	if first == second || strings.TrimSpace(first) == "" || strings.TrimSpace(second) == "" {
		t.Fatalf("generated worker identities = (%q, %q), want distinct non-empty values", first, second)
	}
}
