// Package persistencetest provides in-memory repositories shared by tests of packages that depend on persistence.
package persistencetest

import (
	"context"
	"sort"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
)

// AdminUserRepository implements the administrator account repository over an in-memory map.
// A test transaction embeds it in place of persistence.OperationsTransaction, so every other
// repository method stays unimplemented and panics when a test reaches it unexpectedly.
// It is not safe for concurrent use.
type AdminUserRepository struct {
	persistence.OperationsTransaction
	// Accounts holds the stored accounts by identity. It must be non-nil before a write.
	Accounts map[string]persistence.AdminUserRecord
	// HasActiveAdminKey reports whether an administrator key remains. Nil means none does.
	HasActiveAdminKey func() bool
}

// AdminUser returns one account by identity.
func (users AdminUserRepository) AdminUser(_ context.Context, id string) (persistence.AdminUserRecord, error) {
	record, exists := users.Accounts[id]
	if !exists {
		return persistence.AdminUserRecord{}, persistence.ErrNotFound
	}
	return record, nil
}

// AdminUserByUsername returns one account by normalized username.
func (users AdminUserRepository) AdminUserByUsername(_ context.Context, username string) (persistence.AdminUserRecord, error) {
	for _, record := range users.Accounts {
		if record.Username == username {
			return record, nil
		}
	}
	return persistence.AdminUserRecord{}, persistence.ErrNotFound
}

// AdminUsers returns every account oldest first without password verifiers.
func (users AdminUserRepository) AdminUsers(context.Context) ([]persistence.AdminUserSummary, error) {
	summaries := make([]persistence.AdminUserSummary, 0, len(users.Accounts))
	for _, record := range users.Accounts {
		summaries = append(summaries, record.AdminUserSummary)
	}
	sort.Slice(summaries, func(left, right int) bool {
		if summaries[left].CreatedAt.Equal(summaries[right].CreatedAt) {
			return summaries[left].ID < summaries[right].ID
		}
		return summaries[left].CreatedAt.Before(summaries[right].CreatedAt)
	})
	return summaries, nil
}

// HasAdminUser reports whether any account is stored.
func (users AdminUserRepository) HasAdminUser(context.Context) (bool, error) {
	return len(users.Accounts) > 0, nil
}

// PutAdminUser stores one validated account and rejects a duplicate identity or username.
func (users AdminUserRepository) PutAdminUser(_ context.Context, record persistence.AdminUserRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if _, exists := users.Accounts[record.ID]; exists {
		return persistence.ErrConflict
	}
	for _, existing := range users.Accounts {
		if existing.Username == record.Username {
			return persistence.ErrConflict
		}
	}
	users.Accounts[record.ID] = record
	return nil
}

// SetAdminUserPassword replaces one verifier and clears the failed sign-in state.
func (users AdminUserRepository) SetAdminUserPassword(
	_ context.Context,
	write persistence.AdminUserPasswordWrite,
) (persistence.AdminUserSummary, error) {
	if err := write.Validate(); err != nil {
		return persistence.AdminUserSummary{}, err
	}
	record, exists := users.Accounts[write.ID]
	if !exists {
		return persistence.AdminUserSummary{}, persistence.ErrNotFound
	}
	record.PasswordSalt, record.PasswordHash = write.PasswordSalt, write.PasswordHash
	record.PasswordChangedAt = write.ChangedAt
	record.FailedAttempts, record.LockedUntil = 0, nil
	users.Accounts[write.ID] = record
	return record.AdminUserSummary, nil
}

// RecordAdminUserSignInFailure counts one failure and locks the account at the threshold.
func (users AdminUserRepository) RecordAdminUserSignInFailure(
	_ context.Context,
	failure persistence.AdminUserSignInFailure,
) (*time.Time, error) {
	if err := failure.Validate(); err != nil {
		return nil, err
	}
	record, exists := users.Accounts[failure.ID]
	if !exists {
		return nil, persistence.ErrNotFound
	}
	record.FailedAttempts++
	if record.FailedAttempts >= failure.Threshold {
		lockedUntil := failure.LockedUntil
		record.LockedUntil = &lockedUntil
	}
	users.Accounts[failure.ID] = record
	if record.FailedAttempts < failure.Threshold {
		return nil, nil
	}
	return record.LockedUntil, nil
}

// ClearAdminUserSignInFailures resets the failure count and lock of one account.
func (users AdminUserRepository) ClearAdminUserSignInFailures(_ context.Context, id string) error {
	record, exists := users.Accounts[id]
	if !exists {
		return nil
	}
	record.FailedAttempts, record.LockedUntil = 0, nil
	users.Accounts[id] = record
	return nil
}

// DeleteAdminUser removes one account unless it is the final administrator credential.
func (users AdminUserRepository) DeleteAdminUser(_ context.Context, id string) error {
	if _, exists := users.Accounts[id]; !exists {
		return persistence.ErrNotFound
	}
	if len(users.Accounts) == 1 && (users.HasActiveAdminKey == nil || !users.HasActiveAdminKey()) {
		return persistence.ErrConflict
	}
	delete(users.Accounts, id)
	return nil
}
