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
	// customerSessionPrefix distinguishes customer-bound sessions from durable API keys.
	customerSessionPrefix = "iaps_"
	// customerSessionPurpose binds protected claims to their dedicated cryptographic domain.
	customerSessionPurpose = "customer_session"
	// defaultCustomerSessionTTL limits exposure after a mobile session or issuer key is compromised.
	defaultCustomerSessionTTL = 15 * time.Minute
	// maximumCustomerSessionBytes bounds bearer parsing before allocation and JSON decoding.
	maximumCustomerSessionBytes = 8 << 10
)

// CustomerSessions mints and authenticates short-lived customer-bound data-plane bearers.
type CustomerSessions struct {
	store      persistence.OperationsStore
	protection protection.Service
	clock      func() time.Time
	ttl        time.Duration
}

// CustomerSession is one newly minted opaque bearer and its expiry metadata.
type CustomerSession struct {
	Token     string
	ExpiresAt time.Time
}

// CustomerPrincipal binds an authenticated application identity to exactly one external customer.
type CustomerPrincipal struct {
	Principal
	ExternalCustomerID string
	ExpiresAt          time.Time
}

// customerSessionClaims are encrypted and authenticated inside one opaque bearer.
type customerSessionClaims struct {
	IssuerKeyID        string    `json:"issuer_key_id"`
	ExternalCustomerID string    `json:"external_customer_id"`
	IssuedAt           time.Time `json:"issued_at"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// customerSessionEnvelope carries only scope metadata and protected bytes needed to open the claims.
type customerSessionEnvelope struct {
	ProjectID       core.ProjectID     `json:"project_id"`
	ApplicationID   core.ApplicationID `json:"application_id"`
	EncryptionKeyID string             `json:"encryption_key_id"`
	Ciphertext      []byte             `json:"ciphertext"`
	Fingerprint     []byte             `json:"fingerprint"`
}

// NewCustomerSessions constructs the bounded customer-session service.
func NewCustomerSessions(
	store persistence.OperationsStore,
	protectionService protection.Service,
) (*CustomerSessions, error) {
	if store == nil || protectionService == nil {
		return nil, errors.New("customer session dependencies are required")
	}
	return &CustomerSessions{
		store: store, protection: protectionService, clock: time.Now, ttl: defaultCustomerSessionTTL,
	}, nil
}

// Mint creates one customer-bound bearer from an authenticated durable application key.
func (service *CustomerSessions) Mint(
	ctx context.Context,
	issuer Principal,
	externalCustomerID string,
) (CustomerSession, error) {
	if err := errors.Join(issuer.Validate(), core.ValidateExternalCustomerID(externalCustomerID)); err != nil {
		return CustomerSession{}, validation.Wrap(err)
	}
	if issuer.Role != persistence.APIKeyRoleApplication || issuer.KeyID == "" {
		return CustomerSession{}, ErrUnauthorized
	}
	now := service.clock().UTC()
	claims := customerSessionClaims{
		IssuerKeyID: issuer.KeyID, ExternalCustomerID: externalCustomerID,
		IssuedAt: now, ExpiresAt: now.Add(service.ttl),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return CustomerSession{}, err
	}
	request, err := protection.NewRequest(protection.Scope{
		ProjectID: issuer.ProjectID, ApplicationID: issuer.ApplicationID, Purpose: customerSessionPurpose,
	}, payload)
	if err != nil {
		return CustomerSession{}, err
	}
	protected, err := service.protection.Protect(ctx, request)
	if err != nil {
		return CustomerSession{}, err
	}
	token, err := encodeCustomerSession(issuer.ProjectID, issuer.ApplicationID, protected)
	if err != nil {
		return CustomerSession{}, err
	}
	return CustomerSession{Token: token, ExpiresAt: claims.ExpiresAt}, nil
}

// Authenticate opens one bearer, checks expiry and issuer revocation, and returns its customer scope.
func (service *CustomerSessions) Authenticate(ctx context.Context, token string) (CustomerPrincipal, error) {
	envelope, value, err := decodeCustomerSession(token)
	if err != nil {
		return CustomerPrincipal{}, ErrUnauthorized
	}
	request, err := protection.NewOpenRequest(protection.Scope{
		ProjectID: envelope.ProjectID, ApplicationID: envelope.ApplicationID, Purpose: customerSessionPurpose,
	}, value)
	if err != nil {
		return CustomerPrincipal{}, ErrUnauthorized
	}
	payload, err := service.protection.Open(ctx, request)
	if err != nil {
		return CustomerPrincipal{}, ErrUnauthorized
	}
	defer zero(payload)
	var claims customerSessionClaims
	if err := decodeCustomerSessionJSON(payload, &claims); err != nil {
		return CustomerPrincipal{}, ErrUnauthorized
	}
	now := service.clock().UTC()
	if claims.IssuerKeyID == "" || core.ValidateExternalCustomerID(claims.ExternalCustomerID) != nil ||
		claims.IssuedAt.IsZero() || claims.ExpiresAt.IsZero() || !claims.ExpiresAt.After(claims.IssuedAt) ||
		!now.Before(claims.ExpiresAt) {
		return CustomerPrincipal{}, ErrUnauthorized
	}
	var issuer persistence.APIKeyRecord
	err = service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		issuer, loadErr = repository.APIKey(ctx, claims.IssuerKeyID)
		return loadErr
	})
	if err != nil || issuer.Role != persistence.APIKeyRoleApplication ||
		issuer.ProjectID != envelope.ProjectID || issuer.ApplicationID != envelope.ApplicationID {
		return CustomerPrincipal{}, ErrUnauthorized
	}
	principal := CustomerPrincipal{
		Principal: Principal{
			KeyID: issuer.ID, Role: issuer.Role, ProjectID: issuer.ProjectID, ApplicationID: issuer.ApplicationID,
		},
		ExternalCustomerID: claims.ExternalCustomerID,
		ExpiresAt:          claims.ExpiresAt,
	}
	if err := principal.Validate(); err != nil {
		return CustomerPrincipal{}, ErrUnauthorized
	}
	return principal, nil
}

// Validate checks durable application scope, customer identity, and session expiry metadata.
func (principal CustomerPrincipal) Validate() error {
	return errors.Join(
		principal.Principal.Validate(),
		core.ValidateExternalCustomerID(principal.ExternalCustomerID),
		validateSessionExpiry(principal.ExpiresAt),
	)
}

// encodeCustomerSession serializes protected claims into one URL-safe opaque bearer.
func encodeCustomerSession(
	projectID core.ProjectID,
	applicationID core.ApplicationID,
	value protection.Value,
) (string, error) {
	envelope := customerSessionEnvelope{
		ProjectID: projectID, ApplicationID: applicationID, EncryptionKeyID: value.KeyID,
		Ciphertext: append([]byte(nil), value.Ciphertext...), Fingerprint: append([]byte(nil), value.Fingerprint[:]...),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	return customerSessionPrefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

// decodeCustomerSession validates one bounded envelope and reconstructs its protected value.
func decodeCustomerSession(token string) (customerSessionEnvelope, protection.Value, error) {
	if len(token) <= len(customerSessionPrefix) || len(token) > maximumCustomerSessionBytes ||
		!strings.HasPrefix(token, customerSessionPrefix) {
		return customerSessionEnvelope{}, protection.Value{}, ErrUnauthorized
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, customerSessionPrefix))
	if err != nil {
		return customerSessionEnvelope{}, protection.Value{}, ErrUnauthorized
	}
	var envelope customerSessionEnvelope
	if err := decodeCustomerSessionJSON(payload, &envelope); err != nil ||
		envelope.ProjectID.Validate() != nil || envelope.ApplicationID.Validate() != nil ||
		len(envelope.Fingerprint) != 32 {
		return customerSessionEnvelope{}, protection.Value{}, ErrUnauthorized
	}
	value := protection.Value{
		Ciphertext: append([]byte(nil), envelope.Ciphertext...), KeyID: envelope.EncryptionKeyID,
	}
	copy(value.Fingerprint[:], envelope.Fingerprint)
	if err := value.Validate(); err != nil {
		return customerSessionEnvelope{}, protection.Value{}, ErrUnauthorized
	}
	return envelope, value, nil
}

// decodeCustomerSessionJSON accepts exactly one strict JSON object.
func decodeCustomerSessionJSON(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing customer session JSON")
	}
	return nil
}

// validateSessionExpiry rejects missing session expiry metadata.
func validateSessionExpiry(value time.Time) error {
	if value.IsZero() {
		return errors.New("customer session expiry is required")
	}
	return nil
}
