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
	Role          persistence.APIKeyRole
	ProjectID     core.ProjectID
	ApplicationID core.ApplicationID
}

// Service creates and verifies memory-hard API key records.
type Service struct {
	store              persistence.OperationsStore
	bootstrapAdminHash [sha256.Size]byte
	hasBootstrapAdmin  bool
	clock              func() time.Time
}

var (
	// ErrUnauthorized indicates missing or invalid bearer authentication.
	ErrUnauthorized = errors.New("unauthorized")
)

// NewService constructs authentication with an optional bootstrap administrator bearer.
func NewService(store persistence.OperationsStore, bootstrapAdminKey string) (*Service, error) {
	if store == nil {
		return nil, errors.New("authentication operations store is required")
	}
	service := &Service{store: store, clock: time.Now}
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
		return "", err
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
	hash := derive(secret, salt)
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

// Authenticate verifies a bootstrap administrator or stored scoped bearer key.
func (service *Service) Authenticate(ctx context.Context, bearer string) (Principal, error) {
	bearer = strings.TrimSpace(bearer)
	if service.hasBootstrapAdmin {
		digest := sha256.Sum256([]byte(bearer))
		if subtle.ConstantTimeCompare(digest[:], service.bootstrapAdminHash[:]) == 1 {
			return Principal{Role: persistence.APIKeyRoleAdmin}, nil
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
		return Principal{}, ErrUnauthorized
	}
	hash := derive(secret, record.SecretSalt)
	if subtle.ConstantTimeCompare(hash[:], record.SecretHash[:]) != 1 {
		return Principal{}, ErrUnauthorized
	}
	return Principal{Role: record.Role, ProjectID: record.ProjectID, ApplicationID: record.ApplicationID}, nil
}

// Validate checks that principal scope matches its role.
func (principal Principal) Validate() error {
	switch principal.Role {
	case persistence.APIKeyRoleAdmin:
		if principal.ProjectID != "" || principal.ApplicationID != "" {
			return errors.New("admin principal must not have application scope")
		}
		return nil
	case persistence.APIKeyRoleApplication:
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

// zero overwrites temporary bearer material after use.
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
