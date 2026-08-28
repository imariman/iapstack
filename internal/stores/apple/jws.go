package apple

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

var (
	// appleWWDRIntermediateOID identifies Apple Worldwide Developer Relations intermediate certificates.
	appleWWDRIntermediateOID = []int{1, 2, 840, 113635, 100, 6, 2, 1}
)

// jwsHeader contains the protected Apple certificate chain and signing algorithm.
type jwsHeader struct {
	Algorithm string   `json:"alg"`
	Chain     []string `json:"x5c"`
}

// trustedRoots contains parsed Apple roots and their exact DER fingerprints.
type trustedRoots struct {
	pool         *x509.CertPool
	fingerprints map[[sha256.Size]byte]struct{}
}

// verifyTransactionJWS verifies Apple's ES256 signature and certificate chain before decoding a transaction.
func verifyTransactionJWS(signed string, roots trustedRoots) (transactionPayload, error) {
	var payload transactionPayload
	if err := verifyAppleJWS(signed, roots, &payload); err != nil {
		return transactionPayload{}, err
	}
	payload.AppAccountToken = strings.ToLower(payload.AppAccountToken)
	return payload, nil
}

// verifyRenewalJWS authenticates renewal claims and canonicalizes their UUID customer binding.
func verifyRenewalJWS(signed string, roots trustedRoots) (renewalPayload, error) {
	var payload renewalPayload
	if err := verifyAppleJWS(signed, roots, &payload); err != nil {
		return renewalPayload{}, err
	}
	payload.AppAccountToken = strings.ToLower(payload.AppAccountToken)
	return payload, nil
}

// verifyAppleJWS authenticates one Apple compact JWS before decoding its typed payload.
func verifyAppleJWS(signed string, roots trustedRoots, destination any) error {
	parts := strings.Split(signed, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return errors.New("apple payload is not a compact JWS")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errors.New("apple JWS header is not base64url")
	}
	var header jwsHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return errors.New("apple JWS header is not valid JSON")
	}
	if header.Algorithm != "ES256" || len(header.Chain) != 3 {
		return errors.New("apple JWS must use ES256 with a three-certificate chain")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errors.New("apple JWS payload is not base64url")
	}
	var metadata struct {
		SignedDate int64 `json:"signedDate"`
	}
	if err := json.Unmarshal(payloadBytes, &metadata); err != nil {
		return errors.New("apple JWS payload is not valid JSON")
	}
	if metadata.SignedDate <= 0 {
		return errors.New("apple JWS signed date is required")
	}
	certificates, err := parseJWSCertificates(header.Chain)
	if err != nil {
		return err
	}
	if _, trusted := roots.fingerprints[sha256.Sum256(certificates[2].Raw)]; !trusted {
		return errors.New("apple JWS root certificate is not trusted")
	}
	if !hasExtension(certificates[1], appleWWDRIntermediateOID) {
		return errors.New("apple JWS intermediate certificate is not a WWDR certificate")
	}
	intermediates := x509.NewCertPool()
	intermediates.AddCert(certificates[1])
	if _, err := certificates[0].Verify(x509.VerifyOptions{
		Roots:         roots.pool,
		Intermediates: intermediates,
		CurrentTime:   milliseconds(metadata.SignedDate),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return fmt.Errorf("verify Apple JWS certificate chain: %w", err)
	}
	publicKey, ok := certificates[0].PublicKey.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		return errors.New("apple JWS leaf key is not P-256 ECDSA")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		return errors.New("apple JWS signature is not an ES256 signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(publicKey, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		return errors.New("apple JWS signature verification failed")
	}
	if err := json.Unmarshal(payloadBytes, destination); err != nil {
		return errors.New("apple JWS payload does not match its contract")
	}
	return nil
}

// parseTrustedRoots parses one or more configured Apple root certificates from PEM.
func parseTrustedRoots(values []string) (trustedRoots, error) {
	if len(values) == 0 {
		return trustedRoots{}, errors.New("apple root certificates are required")
	}
	result := trustedRoots{pool: x509.NewCertPool(), fingerprints: make(map[[sha256.Size]byte]struct{})}
	for _, value := range values {
		remainder := []byte(value)
		parsed := false
		for len(remainder) > 0 {
			block, rest := pem.Decode(remainder)
			if block == nil {
				if strings.TrimSpace(string(remainder)) != "" {
					return trustedRoots{}, errors.New("apple root certificate is not valid PEM")
				}
				break
			}
			remainder = rest
			if block.Type != "CERTIFICATE" {
				return trustedRoots{}, errors.New("apple root PEM must contain certificates")
			}
			certificate, err := x509.ParseCertificate(block.Bytes)
			if err != nil || !certificate.IsCA {
				return trustedRoots{}, errors.New("apple root certificate is invalid")
			}
			result.pool.AddCert(certificate)
			result.fingerprints[sha256.Sum256(certificate.Raw)] = struct{}{}
			parsed = true
		}
		if !parsed {
			return trustedRoots{}, errors.New("apple root certificate is empty")
		}
	}
	return result, nil
}

// parseJWSCertificates decodes the leaf, intermediate, and root certificates in x5c order.
func parseJWSCertificates(chain []string) ([]*x509.Certificate, error) {
	certificates := make([]*x509.Certificate, 0, len(chain))
	for _, encoded := range chain {
		der, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("apple JWS certificate is not base64 DER")
		}
		certificate, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, errors.New("apple JWS certificate is invalid")
		}
		certificates = append(certificates, certificate)
	}
	return certificates, nil
}

// hasExtension reports whether a certificate contains the required object identifier.
func hasExtension(certificate *x509.Certificate, identifier []int) bool {
	for _, extension := range certificate.Extensions {
		if extension.Id.Equal(identifier) {
			return true
		}
	}
	return false
}

// milliseconds converts a provider Unix millisecond timestamp to UTC.
func milliseconds(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}
