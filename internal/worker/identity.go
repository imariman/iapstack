package worker

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

const (
	// automaticIdentityRandomBytes controls collision resistance for generated worker identities.
	automaticIdentityRandomBytes = 8
)

// NewIdentity returns a configured worker identity or generates a process-unique fallback.
func NewIdentity(configured string) (string, error) {
	if configured = strings.TrimSpace(configured); configured != "" {
		return configured, nil
	}
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "worker"
	}
	random := make([]byte, automaticIdentityRandomBytes)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate worker identity: %w", err)
	}
	return fmt.Sprintf("%s-%d-%s", hostname, os.Getpid(), hex.EncodeToString(random)), nil
}
