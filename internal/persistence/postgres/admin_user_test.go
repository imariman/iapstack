package postgres_test

import (
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/persistence/postgres"
)

// adminUserRecord creates one deterministic valid administrator account for repository tests.
func adminUserRecord(id string, username string, createdAt time.Time) persistence.AdminUserRecord {
	var salt [16]byte
	var hash [32]byte
	salt[0] = 1
	hash[0] = 1
	return persistence.AdminUserRecord{
		AdminUserSummary: persistence.AdminUserSummary{
			ID: id, Username: username, CreatedAt: createdAt, PasswordChangedAt: createdAt,
		},
		PasswordSalt: salt, PasswordHash: hash,
	}
}

// TestAdminUserRepositoryStoresAccountsAndSignInFailures verifies account storage, the unique username,
// the lock written at the failure threshold, and the reset on a password change.
func TestAdminUserRepositoryStoresAccountsAndSignInFailures(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	store := openRepositoryStore(t, database.ctx)
	createdAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	operate := func(name string, operation persistence.OperationsFunc) {
		t.Helper()
		if err := store.Operate(database.ctx, operation); err != nil {
			t.Fatalf("%s error = %v", name, err)
		}
	}

	operate("HasAdminUser() empty", func(repository persistence.OperationsTransaction) error {
		exists, err := repository.HasAdminUser(database.ctx)
		if err == nil && exists {
			t.Fatal("HasAdminUser() = true for an empty account table")
		}
		return err
	})
	operate("PutAdminUser()", func(repository persistence.OperationsTransaction) error {
		if err := repository.PutAdminUser(database.ctx, adminUserRecord("account-2", "second", createdAt.Add(time.Minute))); err != nil {
			return err
		}
		return repository.PutAdminUser(database.ctx, adminUserRecord("account-1", "owner@example.com", createdAt))
	})
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		return repository.PutAdminUser(database.ctx, adminUserRecord("account-3", "second", createdAt))
	}); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("PutAdminUser() duplicate username error = %v, want ErrConflict", err)
	}
	if _, err := database.conn.Exec(database.ctx, `
		INSERT INTO admin_users (id, username, password_salt, password_hash, password_changed_at)
		VALUES ('account-4', 'Not Lowercase', $1, $2, now())
	`, make([]byte, 16), make([]byte, 32)); err == nil {
		t.Fatal("admin_users accepted a username outside the stored shape")
	}

	operate("AdminUsers()", func(repository persistence.OperationsTransaction) error {
		users, err := repository.AdminUsers(database.ctx)
		if err != nil {
			return err
		}
		if len(users) != 2 || users[0].ID != "account-1" || users[1].ID != "account-2" ||
			!users[0].CreatedAt.Equal(createdAt) || !users[0].PasswordChangedAt.Equal(createdAt) {
			t.Fatalf("AdminUsers() = %#v, want two accounts oldest first", users)
		}
		exists, err := repository.HasAdminUser(database.ctx)
		if err == nil && !exists {
			t.Fatal("HasAdminUser() = false with stored accounts")
		}
		return err
	})

	lockedUntil := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Microsecond)
	failure := persistence.AdminUserSignInFailure{ID: "account-1", Threshold: 3, LockedUntil: lockedUntil}
	for attempt := 1; attempt <= 3; attempt++ {
		operate("RecordAdminUserSignInFailure()", func(repository persistence.OperationsTransaction) error {
			locked, err := repository.RecordAdminUserSignInFailure(database.ctx, failure)
			if err != nil {
				return err
			}
			if attempt < 3 && locked != nil {
				t.Fatalf("attempt %d lock = %v, want none below the threshold", attempt, locked)
			}
			if attempt == 3 && (locked == nil || !locked.Equal(lockedUntil)) {
				t.Fatalf("threshold attempt lock = %v, want %v", locked, lockedUntil)
			}
			return nil
		})
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, err := repository.RecordAdminUserSignInFailure(database.ctx, persistence.AdminUserSignInFailure{
			ID: "missing", Threshold: 3, LockedUntil: lockedUntil,
		})
		return err
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("RecordAdminUserSignInFailure() missing account error = %v, want ErrNotFound", err)
	}
	operate("AdminUserByUsername() locked", func(repository persistence.OperationsTransaction) error {
		record, err := repository.AdminUserByUsername(database.ctx, "owner@example.com")
		if err != nil {
			return err
		}
		if record.ID != "account-1" || record.FailedAttempts != 3 || record.LockedUntil == nil ||
			!record.LockedUntil.Equal(lockedUntil) || record.PasswordSalt[0] != 1 || record.PasswordHash[0] != 1 {
			t.Fatalf("AdminUserByUsername() = %#v, want a locked account with its verifier", record)
		}
		return repository.ClearAdminUserSignInFailures(database.ctx, "account-1")
	})

	var salt [16]byte
	var hash [32]byte
	salt[0], hash[0] = 2, 2
	changedAt := time.Now().UTC().Truncate(time.Microsecond)
	operate("SetAdminUserPassword()", func(repository persistence.OperationsTransaction) error {
		if _, err := repository.RecordAdminUserSignInFailure(database.ctx, failure); err != nil {
			return err
		}
		summary, err := repository.SetAdminUserPassword(database.ctx, persistence.AdminUserPasswordWrite{
			ID: "account-1", PasswordSalt: salt, PasswordHash: hash, ChangedAt: changedAt,
		})
		if err != nil {
			return err
		}
		if !summary.PasswordChangedAt.Equal(changedAt) || summary.Username != "owner@example.com" {
			t.Fatalf("SetAdminUserPassword() = %#v", summary)
		}
		record, err := repository.AdminUser(database.ctx, "account-1")
		if err != nil {
			return err
		}
		if record.PasswordHash != hash || record.PasswordSalt != salt || record.FailedAttempts != 0 || record.LockedUntil != nil {
			t.Fatalf("AdminUser() after password change = %#v, want the new verifier and cleared failures", record)
		}
		return nil
	})
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, err := repository.SetAdminUserPassword(database.ctx, persistence.AdminUserPasswordWrite{
			ID: "missing", PasswordSalt: salt, PasswordHash: hash, ChangedAt: changedAt,
		})
		return err
	}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("SetAdminUserPassword() missing account error = %v, want ErrNotFound", err)
	}
}

// TestAdminUserAndKeyLifecyclePreserveOneAdministrator verifies that an account lets the final administrator
// key be revoked and that the final account cannot be deleted once no administrator key remains.
func TestAdminUserAndKeyLifecyclePreserveOneAdministrator(t *testing.T) {
	database := openTestDatabase(t, postgres.LatestVersion)
	store := openRepositoryStore(t, database.ctx)
	createdAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		if err := repository.PutAPIKey(database.ctx, apiKeyRecord("admin-key-1", persistence.APIKeyRoleAdmin, "", "", createdAt)); err != nil {
			return err
		}
		for id, username := range map[string]string{"account-1": "owner", "account-2": "second"} {
			if err := repository.PutAdminUser(database.ctx, adminUserRecord(id, username, createdAt)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed administrators error = %v", err)
	}
	deleteAccount := func(id string) error {
		return store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
			return repository.DeleteAdminUser(database.ctx, id)
		})
	}

	if err := deleteAccount("account-2"); err != nil {
		t.Fatalf("DeleteAdminUser() with another account error = %v", err)
	}
	if err := deleteAccount("account-2"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("DeleteAdminUser() missing account error = %v, want ErrNotFound", err)
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, err := repository.RevokeAPIKey(database.ctx, "admin-key-1", time.Now().UTC())
		return err
	}); err != nil {
		t.Fatalf("RevokeAPIKey() final key with an account error = %v", err)
	}
	if err := deleteAccount("account-1"); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("DeleteAdminUser() final administrator error = %v, want ErrConflict", err)
	}
	if err := store.Operate(database.ctx, func(repository persistence.OperationsTransaction) error {
		_, err := repository.AdminUser(database.ctx, "account-1")
		return err
	}); err != nil {
		t.Fatalf("refused deletion removed the final account: %v", err)
	}
}
