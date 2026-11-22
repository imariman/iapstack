package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// adminUserIDBytes controls the collision-resistant public account identity length.
	adminUserIDBytes = 12
	// minimumAdminPasswordBytes is the shortest accepted administrator password.
	minimumAdminPasswordBytes = 12
	// maximumAdminPasswordBytes bounds the input of one memory-hard derivation.
	maximumAdminPasswordBytes = 256
	// adminSignInFailureThreshold is the consecutive failure count that locks an account.
	adminSignInFailureThreshold = 5
	// adminSignInLockDuration is how long one lock rejects sign-ins. The failure count resets only on
	// a successful sign-in, so each further failure after the lock expires locks the account again.
	adminSignInLockDuration = 5 * time.Minute
	// generatedAdminPasswordBytes gives a reset password 120 bits of entropy in 20 characters.
	generatedAdminPasswordBytes = 15
)

var (
	// ErrPasswordMismatch indicates that a signed-in administrator supplied a wrong current password.
	ErrPasswordMismatch = errors.New("current password is incorrect")
	// unknownAdminUserSalt keys the derivation spent on unknown usernames so that a missing account
	// costs the same time as a wrong password.
	unknownAdminUserSalt = [16]byte{'i', 'a', 'p', 's', 't', 'a', 'c', 'k', '-', 'n', 'o', '-', 'u', 's', 'e', 'r'}
)

// LockedError reports that an administrator account rejects sign-ins until its lock expires.
type LockedError struct {
	// RetryAfter is the remaining lock time.
	RetryAfter time.Duration
}

// AdminPasswordReset is the outcome of one out-of-band password reset.
type AdminPasswordReset struct {
	Username string
	// Password is the generated replacement. It is returned once and never stored.
	Password string
	// Created reports that the account did not exist and was created by the reset.
	Created bool
}

// Error describes the lock without naming the account.
func (failure *LockedError) Error() string {
	return "administrator account is temporarily locked"
}

// NormalizeAdminUsername trims and lowercases a username and checks its shape.
func NormalizeAdminUsername(username string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(username))
	if err := persistence.ValidateAdminUsername(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

// CreateAdminUser stores one administrator account with a memory-hard password verifier.
func (service *Service) CreateAdminUser(
	ctx context.Context,
	username string,
	password string,
) (persistence.AdminUserSummary, error) {
	normalized, err := NormalizeAdminUsername(username)
	if err != nil {
		return persistence.AdminUserSummary{}, validation.Wrap(err)
	}
	if err := validateAdminPassword(normalized, password); err != nil {
		return persistence.AdminUserSummary{}, validation.Wrap(err)
	}
	record, err := service.newAdminUserRecord(ctx, normalized, password)
	if err != nil {
		return persistence.AdminUserSummary{}, err
	}
	if err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		return repository.PutAdminUser(ctx, record)
	}); err != nil {
		return persistence.AdminUserSummary{}, fmt.Errorf("store administrator account: %w", err)
	}
	return record.AdminUserSummary, nil
}

// AdminUsers returns the stored administrator accounts without password verifiers.
func (service *Service) AdminUsers(ctx context.Context) ([]persistence.AdminUserSummary, error) {
	var users []persistence.AdminUserSummary
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		users, loadErr = repository.AdminUsers(ctx)
		return loadErr
	})
	if err != nil {
		return nil, fmt.Errorf("list administrator accounts: %w", err)
	}
	return users, nil
}

// AuthenticatePassword verifies one username and password and returns the account's administrator identity.
// A wrong password counts toward the account lock. A locked account returns *LockedError.
func (service *Service) AuthenticatePassword(
	ctx context.Context,
	username string,
	password string,
) (Principal, error) {
	normalized, err := NormalizeAdminUsername(username)
	if err != nil || password == "" || len(password) > maximumAdminPasswordBytes {
		return Principal{}, ErrUnauthorized
	}
	record, err := service.adminUserByUsername(ctx, normalized)
	if errors.Is(err, persistence.ErrNotFound) {
		if _, deriveErr := service.derivePassword(ctx, password, unknownAdminUserSalt); deriveErr != nil {
			return Principal{}, deriveErr
		}
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, fmt.Errorf("load administrator account for sign-in: %w", err)
	}
	if err := service.verifyAdminUserPassword(ctx, record, password); err != nil {
		return Principal{}, err
	}
	return Principal{Role: persistence.APIKeyRoleAdmin, UserID: record.ID, Username: record.Username}, nil
}

// ChangeAdminUserPassword replaces one account's password. With verifyCurrent set, the caller's
// current password must match first; a wrong one returns ErrPasswordMismatch and counts toward the lock.
// Sessions issued before the change stop authenticating.
func (service *Service) ChangeAdminUserPassword(
	ctx context.Context,
	id string,
	currentPassword string,
	verifyCurrent bool,
	newPassword string,
) (persistence.AdminUserSummary, error) {
	var record persistence.AdminUserRecord
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		record, loadErr = repository.AdminUser(ctx, id)
		return loadErr
	})
	if err != nil {
		return persistence.AdminUserSummary{}, fmt.Errorf("load administrator account for password change: %w", err)
	}
	if err := validateAdminPassword(record.Username, newPassword); err != nil {
		return persistence.AdminUserSummary{}, validation.Wrap(err)
	}
	if verifyCurrent {
		if currentPassword == "" || len(currentPassword) > maximumAdminPasswordBytes {
			return persistence.AdminUserSummary{}, ErrPasswordMismatch
		}
		if err := service.verifyAdminUserPassword(ctx, record, currentPassword); err != nil {
			if errors.Is(err, ErrUnauthorized) {
				return persistence.AdminUserSummary{}, ErrPasswordMismatch
			}
			return persistence.AdminUserSummary{}, err
		}
	}
	write, err := service.newAdminPasswordWrite(ctx, record.ID, newPassword)
	if err != nil {
		return persistence.AdminUserSummary{}, err
	}
	var summary persistence.AdminUserSummary
	err = service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var writeErr error
		summary, writeErr = repository.SetAdminUserPassword(ctx, write)
		return writeErr
	})
	if err != nil {
		return persistence.AdminUserSummary{}, fmt.Errorf("store administrator password: %w", err)
	}
	return summary, nil
}

// DeleteAdminUser removes one administrator account while another administrator credential remains.
func (service *Service) DeleteAdminUser(ctx context.Context, id string) error {
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		return repository.DeleteAdminUser(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("delete administrator account: %w", err)
	}
	return nil
}

// ResetAdminUserPassword gives one account a generated password, creating the account when it does
// not exist. It is the out-of-band recovery path and does not ask for the current password.
func (service *Service) ResetAdminUserPassword(ctx context.Context, username string) (AdminPasswordReset, error) {
	normalized, err := NormalizeAdminUsername(username)
	if err != nil {
		return AdminPasswordReset{}, validation.Wrap(err)
	}
	secret := make([]byte, generatedAdminPasswordBytes)
	if _, err := rand.Read(secret); err != nil {
		return AdminPasswordReset{}, fmt.Errorf("generate administrator password: %w", err)
	}
	defer zero(secret)
	password := base64.RawURLEncoding.EncodeToString(secret)
	record, err := service.newAdminUserRecord(ctx, normalized, password)
	if err != nil {
		return AdminPasswordReset{}, err
	}
	created := false
	err = service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		// The callback may run again after a concurrency failure, so it recomputes the outcome.
		created = false
		existing, loadErr := repository.AdminUserByUsername(ctx, normalized)
		if errors.Is(loadErr, persistence.ErrNotFound) {
			created = true
			return repository.PutAdminUser(ctx, record)
		}
		if loadErr != nil {
			return loadErr
		}
		_, writeErr := repository.SetAdminUserPassword(ctx, persistence.AdminUserPasswordWrite{
			ID: existing.ID, PasswordSalt: record.PasswordSalt, PasswordHash: record.PasswordHash,
			ChangedAt: record.PasswordChangedAt,
		})
		return writeErr
	})
	if err != nil {
		return AdminPasswordReset{}, fmt.Errorf("reset administrator password: %w", err)
	}
	return AdminPasswordReset{Username: normalized, Password: password, Created: created}, nil
}

// adminUserByUsername loads one account and its verifier by normalized username.
func (service *Service) adminUserByUsername(
	ctx context.Context,
	username string,
) (persistence.AdminUserRecord, error) {
	var record persistence.AdminUserRecord
	err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var loadErr error
		record, loadErr = repository.AdminUserByUsername(ctx, username)
		return loadErr
	})
	return record, err
}

// verifyAdminUserPassword checks one password against a loaded account under the lock policy.
// It returns nil on a match, ErrUnauthorized on a mismatch, and *LockedError while the account is
// locked or when this mismatch locks it.
func (service *Service) verifyAdminUserPassword(
	ctx context.Context,
	record persistence.AdminUserRecord,
	password string,
) error {
	now := service.clock().UTC()
	if record.LockedUntil != nil && now.Before(*record.LockedUntil) {
		return &LockedError{RetryAfter: record.LockedUntil.Sub(now)}
	}
	hash, err := service.derivePassword(ctx, password, record.PasswordSalt)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(hash[:], record.PasswordHash[:]) == 1 {
		if record.FailedAttempts == 0 && record.LockedUntil == nil {
			return nil
		}
		if err := service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
			return repository.ClearAdminUserSignInFailures(ctx, record.ID)
		}); err != nil {
			return fmt.Errorf("clear administrator sign-in failures: %w", err)
		}
		return nil
	}
	var lockedUntil *time.Time
	err = service.store.Operate(ctx, func(repository persistence.OperationsTransaction) error {
		var recordErr error
		lockedUntil, recordErr = repository.RecordAdminUserSignInFailure(ctx, persistence.AdminUserSignInFailure{
			ID: record.ID, Threshold: adminSignInFailureThreshold, LockedUntil: now.Add(adminSignInLockDuration),
		})
		return recordErr
	})
	if errors.Is(err, persistence.ErrNotFound) {
		// The account was deleted while its password was being checked.
		return ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("record administrator sign-in failure: %w", err)
	}
	if lockedUntil != nil && now.Before(*lockedUntil) {
		return &LockedError{RetryAfter: lockedUntil.Sub(now)}
	}
	return ErrUnauthorized
}

// newAdminUserRecord derives a verifier for a new account created at the service clock time.
func (service *Service) newAdminUserRecord(
	ctx context.Context,
	username string,
	password string,
) (persistence.AdminUserRecord, error) {
	idBytes := make([]byte, adminUserIDBytes)
	if _, err := rand.Read(idBytes); err != nil {
		return persistence.AdminUserRecord{}, fmt.Errorf("generate administrator account identity: %w", err)
	}
	write, err := service.newAdminPasswordWrite(ctx, hex.EncodeToString(idBytes), password)
	if err != nil {
		return persistence.AdminUserRecord{}, err
	}
	return persistence.AdminUserRecord{
		AdminUserSummary: persistence.AdminUserSummary{
			ID: write.ID, Username: username, CreatedAt: write.ChangedAt, PasswordChangedAt: write.ChangedAt,
		},
		PasswordSalt: write.PasswordSalt, PasswordHash: write.PasswordHash,
	}, nil
}

// newAdminPasswordWrite derives a fresh salted verifier stamped with the service clock time.
func (service *Service) newAdminPasswordWrite(
	ctx context.Context,
	id string,
	password string,
) (persistence.AdminUserPasswordWrite, error) {
	var salt [16]byte
	if _, err := rand.Read(salt[:]); err != nil {
		return persistence.AdminUserPasswordWrite{}, fmt.Errorf("generate administrator password salt: %w", err)
	}
	hash, err := service.derivePassword(ctx, password, salt)
	if err != nil {
		return persistence.AdminUserPasswordWrite{}, fmt.Errorf("derive administrator password verifier: %w", err)
	}
	// PostgreSQL keeps microseconds. Truncating here keeps the stored change time equal to the one
	// returned to the caller, so a session minted right after the change is not older than it.
	return persistence.AdminUserPasswordWrite{
		ID: id, PasswordSalt: salt, PasswordHash: hash,
		ChangedAt: service.clock().UTC().Truncate(time.Microsecond),
	}, nil
}

// derivePassword runs the memory-hard derivation over a password in the password pool and wipes the copied bytes.
func (service *Service) derivePassword(ctx context.Context, password string, salt [16]byte) ([32]byte, error) {
	secret := []byte(password)
	defer zero(secret)
	return deriveInSlot(ctx, service.passwordSlots, secret, salt)
}

// validateAdminPassword enforces the administrator password length and rejects the username as a password.
func validateAdminPassword(username string, password string) error {
	if len(password) < minimumAdminPasswordBytes {
		return fmt.Errorf("administrator password must contain at least %d characters", minimumAdminPasswordBytes)
	}
	if len(password) > maximumAdminPasswordBytes {
		return fmt.Errorf("administrator password must not exceed %d bytes", maximumAdminPasswordBytes)
	}
	if strings.EqualFold(password, username) {
		return errors.New("administrator password must differ from the username")
	}
	return nil
}
