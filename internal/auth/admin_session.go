package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/protection"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// adminSessionPrefix distinguishes dashboard sessions from durable API keys and customer sessions.
	adminSessionPrefix = "iapd_"
	// adminSessionPurpose binds protected dashboard claims to their dedicated cryptographic domain.
	adminSessionPurpose = "dashboard_admin_session"
	// adminSessionProjectID supplies a fixed protection scope for installation-wide administrator sessions.
	adminSessionProjectID core.ProjectID = "iapstack-control-plane"
	// adminSessionApplicationID supplies a fixed protection scope for installation-wide administrator sessions.
	adminSessionApplicationID core.ApplicationID = "dashboard"
	// defaultAdminSessionTTL keeps browser access convenient while bounding a stolen session.
	defaultAdminSessionTTL = 12 * time.Hour
	// adminSessionClockSkew tolerates small time differences between horizontally scaled API instances.
	adminSessionClockSkew = time.Minute
	// maximumAdminSessionBytes bounds cookie parsing before allocation and JSON decoding.
	maximumAdminSessionBytes = 4 << 10
)

// AdminSessions mints and authenticates short-lived dashboard administrator sessions.
type AdminSessions struct {
	store      persistence.OperationsStore
	protection protection.Service
	clock      func() time.Time
	ttl        time.Duration
}

// AdminSession is one protected dashboard session and its expiry metadata.
type AdminSession struct {
	Token     string
	ExpiresAt time.Time
}

// AdminPrincipal binds an authenticated administrator identity to a dashboard session expiry.
type AdminPrincipal struct {
	Principal
	ExpiresAt time.Time
}

// adminSessionClaims are encrypted and authenticated inside one opaque browser session.
type adminSessionClaims struct {
	IssuerKeyID string    `json:"issuer_key_id"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// adminSessionEnvelope carries only protected metadata required to open dashboard claims.
type adminSessionEnvelope struct {
	EncryptionKeyID string `json:"encryption_key_id"`
	Ciphertext      []byte `json:"ciphertext"`
	Fingerprint     []byte `json:"fingerprint"`
}

// NewAdminSessions constructs the protected dashboard-session service.
func NewAdminSessions(
	store persistence.OperationsStore,
	protectionService protection.Service,
) (*AdminSessions, error) {
	if store == nil || protectionService == nil {
		return nil, errors.New("administrator session dependencies are required")
	}
	return &AdminSessions{
		store: store, protection: protectionService, clock: time.Now, ttl: defaultAdminSessionTTL,
	}, nil
}

// Mint exchanges one authenticated administrator bearer for a bounded browser session.
func (service *AdminSessions) Mint(ctx context.Context, issuer Principal) (AdminSession, error) {
	if err := issuer.Validate(); err != nil {
		return AdminSession{}, validation.Wrap(err)
	}
	if issuer.Role != persistence.APIKeyRoleAdmin {
		return AdminSession{}, ErrUnauthorized
	}
	now := service.clock().UTC()
	claims := adminSessionClaims{
		IssuerKeyID: issuer.KeyID,
		IssuedAt:    now,
		ExpiresAt:   now.Add(service.ttl),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return AdminSession{}, err
	}
	defer zero(payload)
	protected, err := protection.Protect(ctx, service.protection, adminSessionScope(), payload)
	if err != nil {
		return AdminSession{}, err
	}
	token, err := encodeAdminSession(protected)
	if err != nil {
		return AdminSession{}, err
	}
	return AdminSession{Token: token, ExpiresAt: claims.ExpiresAt}, nil
}

// Authenticate opens one dashboard session, checks expiry and stored-key revocation, and returns its administrator.
func (service *AdminSessions) Authenticate(ctx context.Context, token string) (AdminPrincipal, error) {
	value, err := decodeAdminSession(token)
	if err != nil {
		return AdminPrincipal{}, ErrUnauthorized
	}
	request, err := protection.NewOpenRequest(adminSessionScope(), value)
	if err != nil {
		return AdminPrincipal{}, ErrUnauthorized
	}
	payload, err := service.protection.Open(ctx, request)
	if err != nil {
		return AdminPrincipal{}, ErrUnauthorized
	}
	defer zero(payload)
	var claims adminSessionClaims
	if err := decodeAdminSessionJSON(payload, &claims); err != nil {
		return AdminPrincipal{}, ErrUnauthorized
	}
	now := service.clock().UTC()
	if claims.IssuedAt.IsZero() || claims.ExpiresAt.IsZero() || !claims.ExpiresAt.After(claims.IssuedAt) ||
		claims.IssuedAt.After(now.Add(adminSessionClockSkew)) || !now.Before(claims.ExpiresAt) {
		return AdminPrincipal{}, ErrUnauthorized
	}

	principal := Principal{Role: persistence.APIKeyRoleAdmin}
	if claims.IssuerKeyID != "" {
		var issuer persistence.APIKeyRecord
		err = service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
			var loadErr error
			issuer, loadErr = repository.APIKey(ctx, claims.IssuerKeyID)
			return loadErr
		})
		if err != nil || issuer.ID != claims.IssuerKeyID || issuer.Role != persistence.APIKeyRoleAdmin || issuer.Validate() != nil {
			return AdminPrincipal{}, ErrUnauthorized
		}
		principal.KeyID = issuer.ID
	}
	result := AdminPrincipal{Principal: principal, ExpiresAt: claims.ExpiresAt}
	if err := result.Validate(); err != nil {
		return AdminPrincipal{}, ErrUnauthorized
	}
	return result, nil
}

// Validate checks administrator scope and dashboard session expiry metadata.
func (principal AdminPrincipal) Validate() error {
	if principal.Role != persistence.APIKeyRoleAdmin {
		return errors.New("dashboard session principal must be an administrator")
	}
	return errors.Join(principal.Principal.Validate(), validateAdminSessionExpiry(principal.ExpiresAt))
}

// adminSessionScope returns the fixed authenticated-encryption scope for dashboard sessions.
func adminSessionScope() protection.Scope {
	return protection.Scope{
		ProjectID:     adminSessionProjectID,
		ApplicationID: adminSessionApplicationID,
		Purpose:       adminSessionPurpose,
	}
}

// encodeAdminSession serializes protected dashboard claims into one cookie-safe token.
func encodeAdminSession(value protection.Value) (string, error) {
	envelope := adminSessionEnvelope{
		EncryptionKeyID: value.KeyID,
		Ciphertext:      append([]byte(nil), value.Ciphertext...),
		Fingerprint:     append([]byte(nil), value.Fingerprint[:]...),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	return adminSessionPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

// decodeAdminSession validates one bounded token and reconstructs its protected value.
func decodeAdminSession(token string) (protection.Value, error) {
	if len(token) <= len(adminSessionPrefix) || len(token) > maximumAdminSessionBytes ||
		!strings.HasPrefix(token, adminSessionPrefix) {
		return protection.Value{}, ErrUnauthorized
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, adminSessionPrefix))
	if err != nil {
		return protection.Value{}, ErrUnauthorized
	}
	var envelope adminSessionEnvelope
	if err := decodeAdminSessionJSON(payload, &envelope); err != nil || len(envelope.Fingerprint) != 32 {
		return protection.Value{}, ErrUnauthorized
	}
	value := protection.Value{
		Ciphertext: append([]byte(nil), envelope.Ciphertext...),
		KeyID:      envelope.EncryptionKeyID,
	}
	copy(value.Fingerprint[:], envelope.Fingerprint)
	if err := value.Validate(); err != nil {
		return protection.Value{}, ErrUnauthorized
	}
	return value, nil
}

// decodeAdminSessionJSON accepts exactly one strict JSON object.
func decodeAdminSessionJSON(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing administrator session JSON")
	}
	return nil
}

// validateAdminSessionExpiry rejects missing dashboard session expiry metadata.
func validateAdminSessionExpiry(value time.Time) error {
	if value.IsZero() {
		return errors.New("administrator session expiry is required")
	}
	return nil
}
