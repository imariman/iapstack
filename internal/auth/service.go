// Package auth creates and verifies scoped bearer API keys without storing bearer secrets.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/core"
	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
	"golang.org/x/crypto/argon2"
)

const (
	// bearerPrefix distinguishes IAPStack API keys from unrelated credentials.
	bearerPrefix = "iap_"
	// keyIDBytes controls the collision-resistant public lookup identity length.
	keyIDBytes = 12
	// secretBytes controls bearer secret entropy.
	secretBytes = 32
	// minimumBootstrapAdminKeyBytes prevents trivially brute-forced installation credentials.
	minimumBootstrapAdminKeyBytes = 32
	// accountActorPrefix marks an audit actor that is an administrator account rather than a stored key.
	// Key ids are hexadecimal, so the prefix cannot collide with one.
	accountActorPrefix = "account:"
	// maxConcurrentPasswordDerivations caps the memory-hard work that unauthenticated password
	// sign-in attempts can run at once. Administrator sign-ins are rare, so one slot is enough.
	maxConcurrentPasswordDerivations = 1
	// argonTime controls Argon2id iterations.
	argonTime uint32 = 2
	// argonMemory controls Argon2id memory in KiB.
	argonMemory uint32 = 19 * 1024
	// argonThreads controls Argon2id parallelism.
	argonThreads uint8 = 1
	// argonOutputBytes matches the durable API key hash column.
	argonOutputBytes uint32 = 32
)

// Principal is one authenticated administration or application identity.
type Principal struct {
	// KeyID is the stored API key that authenticated the request. It is empty for administrator
	// accounts and for the bootstrap administrator.
	KeyID string
	// UserID and Username identify the administrator account that signed in with a password.
	// Both are empty for API keys and for the bootstrap administrator.
	UserID        string
	Username      string
	Role          persistence.APIKeyRole
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
}

// Service creates and verifies memory-hard API key records.
type Service struct {
	store              persistence.OperationsStore
	bootstrapAdminHash [sha256.Size]byte
	hasBootstrapAdmin  bool
	derivationSlots    chan struct{}
	// passwordSlots bounds password derivations apart from API key derivations. Password sign-in is
	// reachable without a credential, so a flood of it must not take the slots that API keys need.
	passwordSlots chan struct{}
	clock         func() time.Time
}

var (
	// ErrUnauthorized indicates missing or invalid bearer authentication.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrCapacity indicates that every bounded Argon2id derivation slot is occupied.
	ErrCapacity = errors.New("authentication capacity exhausted")
)

// NewService constructs authentication with an optional bootstrap administrator bearer.
func NewService(
	store persistence.OperationsStore,
	bootstrapAdminKey string,
	maxConcurrentDerivations int,
) (*Service, error) {
	if store == nil {
		return nil, errors.New("authentication operations store is required")
	}
	if maxConcurrentDerivations <= 0 {
		return nil, errors.New("maximum concurrent authentication derivations must be positive")
	}
	service := &Service{
		store: store, derivationSlots: make(chan struct{}, maxConcurrentDerivations),
		passwordSlots: make(chan struct{}, maxConcurrentPasswordDerivations), clock: time.Now,
	}
	if bootstrapAdminKey = strings.TrimSpace(bootstrapAdminKey); bootstrapAdminKey != "" {
		if len(bootstrapAdminKey) < minimumBootstrapAdminKeyBytes {
			return nil, errors.New("bootstrap administrator key must contain at least 32 bytes")
		}
		service.bootstrapAdminHash = sha256.Sum256([]byte(bootstrapAdminKey))
		service.hasBootstrapAdmin = true
	}
	return service, nil
}

// Create generates a bearer once and stores only its public identity and Argon2id verifier.
func (service *Service) Create(ctx context.Context, principal Principal) (string, error) {
	if err := principal.Validate(); err != nil {
		return "", validation.Wrap(err)
	}
	idBytes := make([]byte, keyIDBytes)
	secret := make([]byte, secretBytes)
	var salt [16]byte
	if _, err := rand.Read(idBytes); err != nil {
		return "", fmt.Errorf("generate API key identity: %w", err)
	}
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate API key secret: %w", err)
	}
	defer zero(secret)
	if _, err := rand.Read(salt[:]); err != nil {
		return "", fmt.Errorf("generate API key salt: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	hash, err := service.deriveVerifier(ctx, secret, salt)
	if err != nil {
		return "", fmt.Errorf("derive API key verifier: %w", err)
	}
	record := persistence.APIKeyRecord{
		ID:            id,
		Role:          principal.Role,
		ProjectID:     principal.ProjectID,
		ApplicationID: principal.ApplicationID,
		SecretSalt:    salt,
		SecretHash:    hash,
		CreatedAt:     service.clock().UTC(),
	}
	if err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		return repository.PutAPIKey(ctx, record)
	}); err != nil {
		return "", fmt.Errorf("store API key: %w", err)
	}
	return bearerPrefix + id + "." + base64.RawURLEncoding.EncodeToString(secret), nil
}

// Keys returns one page of API key lifecycle metadata without bearer secrets or verifiers.
func (service *Service) Keys(ctx context.Context, pageToken string) (persistence.APIKeyPage, error) {
	var page persistence.APIKeyPage
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		page, loadErr = repository.APIKeys(ctx, pageToken)
		return loadErr
	})
	if err != nil {
		return persistence.APIKeyPage{}, fmt.Errorf("list API keys: %w", err)
	}
	return page, nil
}

// Revoke idempotently disables one stored API key at the service clock time.
func (service *Service) Revoke(ctx context.Context, id string) (persistence.APIKeySummary, error) {
	var summary persistence.APIKeySummary
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var revokeErr error
		summary, revokeErr = repository.RevokeAPIKey(ctx, id, service.clock().UTC())
		return revokeErr
	})
	if err != nil {
		return persistence.APIKeySummary{}, fmt.Errorf("revoke API key: %w", err)
	}
	return summary, nil
}

// HasActiveAdmin reports whether a non-revoked stored administrator key or an administrator account exists.
// The bootstrap administrator authenticates only while this is false.
func (service *Service) HasActiveAdmin(ctx context.Context) (bool, error) {
	var hasAdmin bool
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var checkErr error
		hasAdmin, checkErr = hasStoredAdministrator(ctx, repository)
		return checkErr
	})
	if err != nil {
		return false, fmt.Errorf("check stored administrators: %w", err)
	}
	return hasAdmin, nil
}

// hasStoredAdministrator reports whether an active administrator key or an administrator account exists.
func hasStoredAdministrator(ctx context.Context, repository persistence.OperationsRepository) (bool, error) {
	hasKey, err := repository.HasActiveAdminAPIKey(ctx)
	if err != nil || hasKey {
		return hasKey, err
	}
	return repository.HasAdminUser(ctx)
}

// Authenticate verifies a bootstrap administrator or stored scoped bearer key.
func (service *Service) Authenticate(ctx context.Context, bearer string) (Principal, error) {
	bearer = strings.TrimSpace(bearer)
	if service.hasBootstrapAdmin {
		digest := sha256.Sum256([]byte(bearer))
		if subtle.ConstantTimeCompare(digest[:], service.bootstrapAdminHash[:]) == 1 {
			hasAdmin, err := service.HasActiveAdmin(ctx)
			if err != nil {
				return Principal{}, err
			}
			if !hasAdmin {
				return Principal{Role: persistence.APIKeyRoleAdmin}, nil
			}
			return Principal{}, ErrUnauthorized
		}
	}
	id, secret, err := parseBearer(bearer)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	defer zero(secret)
	var record persistence.APIKeyRecord
	err = service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		record, loadErr = repository.APIKey(ctx, id)
		return loadErr
	})
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			return Principal{}, ErrUnauthorized
		}
		return Principal{}, fmt.Errorf("load API key for authentication: %w", err)
	}
	hash, err := service.deriveVerifier(ctx, secret, record.SecretSalt)
	if err != nil {
		return Principal{}, err
	}
	if subtle.ConstantTimeCompare(hash[:], record.SecretHash[:]) != 1 {
		return Principal{}, ErrUnauthorized
	}
	return Principal{KeyID: record.ID, Role: record.Role, ProjectID: record.ProjectID, ApplicationID: record.ApplicationID}, nil
}

// Validate checks that principal scope matches its role.
func (principal Principal) Validate() error {
	switch principal.Role {
	case persistence.APIKeyRoleAdmin:
		if principal.ProjectID != "" || principal.ApplicationID != "" {
			return errors.New("admin principal must not have application scope")
		}
		if principal.KeyID != "" && principal.UserID != "" {
			return errors.New("admin principal must not combine an API key with an administrator account")
		}
		if (principal.UserID == "") != (principal.Username == "") {
			return errors.New("admin principal must carry an administrator account ID together with its username")
		}
		return nil
	case persistence.APIKeyRoleApplication:
		if principal.UserID != "" || principal.Username != "" {
			return errors.New("application principal must not carry an administrator account")
		}
		return errors.Join(principal.ProjectID.Validate(), principal.ApplicationID.Validate())
	default:
		return fmt.Errorf("unsupported principal role %q", principal.Role)
	}
}

// parseBearer separates and decodes one public key identity and private secret.
func parseBearer(bearer string) (string, []byte, error) {
	if !strings.HasPrefix(bearer, bearerPrefix) {
		return "", nil, ErrUnauthorized
	}
	parts := strings.Split(strings.TrimPrefix(bearer, bearerPrefix), ".")
	if len(parts) != 2 || len(parts[0]) != keyIDBytes*2 {
		return "", nil, ErrUnauthorized
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		return "", nil, ErrUnauthorized
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(secret) != secretBytes {
		zero(secret)
		return "", nil, ErrUnauthorized
	}
	return parts[0], secret, nil
}

// derive computes the fixed Argon2id verifier for one secret and random salt.
func derive(secret []byte, salt [16]byte) [32]byte {
	derived := argon2.IDKey(secret, salt[:], argonTime, argonMemory, argonThreads, argonOutputBytes)
	var result [32]byte
	copy(result[:], derived)
	zero(derived)
	return result
}

// deriveVerifier fails fast when the process-wide service capacity for expensive work is occupied.
func (service *Service) deriveVerifier(
	ctx context.Context,
	secret []byte,
	salt [16]byte,
) ([32]byte, error) {
	return deriveInSlot(ctx, service.derivationSlots, secret, salt)
}

// deriveInSlot runs one derivation inside the given bounded pool and fails fast when the pool is full.
func deriveInSlot(ctx context.Context, slots chan struct{}, secret []byte, salt [16]byte) ([32]byte, error) {
	if err := ctx.Err(); err != nil {
		return [32]byte{}, err
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
		return derive(secret, salt), nil
	default:
		return [32]byte{}, ErrCapacity
	}
}

// ActorID returns the durable identity recorded in audit rows: the stored key id, or
// "account:<username>" for an administrator account. The bootstrap administrator has none.
func (principal Principal) ActorID() string {
	if principal.KeyID != "" {
		return principal.KeyID
	}
	if principal.Username != "" {
		return accountActorPrefix + principal.Username
	}
	return ""
}

// zero overwrites temporary bearer material after use.
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
