// Package stores defines ports implemented by concrete purchase providers.
package stores

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"mime"
)

// Evidence is an adapter-owned, content-typed purchase artifact. Keeping the
// payload opaque lets new providers and artifact versions be added without
// changing the store-neutral verification request.
type Evidence struct {
	ContentType string
	payload     []byte
}

type VerifiedArtifact struct {
	Kind     string
	Evidence Evidence
}

// NewEvidence validates and defensively copies a content-typed provider artifact.
func NewEvidence(contentType string, payload []byte) (Evidence, error) {
	evidence := Evidence{
		ContentType: contentType,
		payload:     append([]byte(nil), payload...),
	}
	if err := evidence.Validate(); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

// Validate checks that the evidence has a valid media type and a non-empty payload.
func (evidence Evidence) Validate() error {
	if evidence.ContentType == "" {
		return errors.New("evidence content type is required")
	}
	if _, _, err := mime.ParseMediaType(evidence.ContentType); err != nil {
		return errors.New("evidence content type must be a valid media type")
	}
	if len(evidence.payload) == 0 {
		return errors.New("evidence payload is required")
	}
	return nil
}

// Validate checks the artifact discriminator and its opaque evidence.
func (artifact VerifiedArtifact) Validate() error {
	if artifact.Kind == "" {
		return errors.New("verified artifact kind is required")
	}
	return artifact.Evidence.Validate()
}

// Bytes returns a defensive copy of the private evidence payload.
func (evidence Evidence) Bytes() []byte {
	return append([]byte(nil), evidence.payload...)
}

// Digest returns the hexadecimal SHA-256 digest of the private payload.
func (evidence Evidence) Digest() string {
	digest := sha256.Sum256(evidence.payload)
	return hex.EncodeToString(digest[:])
}

// LogValue returns payload metadata without exposing the evidence contents.
func (evidence Evidence) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("content_type", evidence.ContentType),
		slog.Int("size", len(evidence.payload)),
		slog.String("sha256", evidence.Digest()),
	)
}
