package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// testAdminPassword satisfies the administrator password policy.
	testAdminPassword = "correct horse battery"
	// testAdminReplacementPassword is a second valid administrator password.
	testAdminReplacementPassword = "another long passphrase"
)

// TestAdminUserPasswordSignInRetiresBootstrap verifies account creation, normalized sign-in, and bootstrap retirement.
func TestAdminUserPasswordSignInRetiresBootstrap(t *testing.T) {
	ctx := context.Background()
	service, _ := newAdminUserTestService(t)
	if _, err := service.Authenticate(ctx, testBootstrapAdminKey); err != nil {
		t.Fatalf("Authenticate() bootstrap before any account error = %v", err)
	}

	user, err := service.CreateAdminUser(ctx, "  Owner@Example.com ", testAdminPassword)
	if err != nil {
		t.Fatalf("CreateAdminUser() error = %v", err)
	}
	if user.Username != "owner@example.com" || len(user.ID) != adminUserIDBytes*2 ||
		!user.CreatedAt.Equal(user.PasswordChangedAt) {
		t.Fatalf("CreateAdminUser() summary = %#v", user)
	}
	if _, err := service.Authenticate(ctx, testBootstrapAdminKey); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate() bootstrap after account creation error = %v, want unauthorized", err)
	}

	principal, err := service.AuthenticatePassword(ctx, "OWNER@example.com", testAdminPassword)
	if err != nil {
		t.Fatalf("AuthenticatePassword() error = %v", err)
	}
	if principal.Role != persistence.APIKeyRoleAdmin || principal.UserID != user.ID ||
		principal.Username != user.Username || principal.KeyID != "" || principal.Validate() != nil {
		t.Fatalf("AuthenticatePassword() principal = %#v", principal)
	}
	for name, credentials := range map[string][2]string{
		"wrong password":    {"owner@example.com", "wrong horse battery"},
		"unknown username":  {"nobody", testAdminPassword},
		"invalid username":  {"no", testAdminPassword},
		"empty password":    {"owner@example.com", ""},
		"oversize password": {"owner@example.com", string(make([]byte, maximumAdminPasswordBytes+1))},
	} {
		if _, err := service.AuthenticatePassword(ctx, credentials[0], credentials[1]); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("AuthenticatePassword() %s error = %v, want unauthorized", name, err)
		}
	}
}

// TestAdminUserPolicyRejectsWeakInput verifies username shape, password length, and duplicate usernames.
func TestAdminUserPolicyRejectsWeakInput(t *testing.T) {
	ctx := context.Background()
	service, _ := newAdminUserTestService(t)
	for name, input := range map[string][2]string{
		"short username":        {"ab", testAdminPassword},
		"username with space":   {"two words", testAdminPassword},
		"username leading dot":  {".owner", testAdminPassword},
		"short password":        {"owner", "elevenchars"},
		"oversize password":     {"owner", string(make([]byte, maximumAdminPasswordBytes+1))},
		"password equals name":  {"owner-of-everything", "Owner-Of-Everything"},
		"username beyond limit": {string(make([]byte, 65)), testAdminPassword},
	} {
		if _, err := service.CreateAdminUser(ctx, input[0], input[1]); !errors.Is(err, validation.ErrInvalid) {
			t.Fatalf("CreateAdminUser() %s error = %v, want invalid", name, err)
		}
	}
	if _, err := service.CreateAdminUser(ctx, "owner", testAdminPassword); err != nil {
		t.Fatalf("CreateAdminUser() error = %v", err)
	}
	if _, err := service.CreateAdminUser(ctx, "OWNER", testAdminReplacementPassword); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CreateAdminUser() duplicate error = %v, want conflict", err)
	}
	users, err := service.AdminUsers(ctx)
	if err != nil || len(users) != 1 || users[0].Username != "owner" {
		t.Fatalf("AdminUsers() = %#v, %v", users, err)
	}
}

// TestAdminUserLocksAfterRepeatedFailures verifies the lock threshold, the lock window, and the reset on success.
func TestAdminUserLocksAfterRepeatedFailures(t *testing.T) {
	ctx := context.Background()
	service, store := newAdminUserTestService(t)
	now := time.Date(2026, time.November, 22, 10, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now }
	user, err := service.CreateAdminUser(ctx, "owner", testAdminPassword)
	if err != nil {
		t.Fatalf("CreateAdminUser() error = %v", err)
	}

	for attempt := 1; attempt < adminSignInFailureThreshold; attempt++ {
		if _, err := service.AuthenticatePassword(ctx, "owner", "wrong horse battery"); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("failed attempt %d error = %v, want unauthorized", attempt, err)
		}
	}
	var locked *LockedError
	if _, err := service.AuthenticatePassword(ctx, "owner", "wrong horse battery"); !errors.As(err, &locked) ||
		locked.RetryAfter != adminSignInLockDuration {
		t.Fatalf("threshold attempt error = %v, want a %s lock", err, adminSignInLockDuration)
	}
	// A locked account rejects even the right password and does not count further attempts.
	now = now.Add(time.Minute)
	if _, err := service.AuthenticatePassword(ctx, "owner", testAdminPassword); !errors.As(err, &locked) ||
		locked.RetryAfter != adminSignInLockDuration-time.Minute {
		t.Fatalf("locked sign-in error = %v, want the remaining lock", err)
	}
	if attempts := store.users[user.ID].FailedAttempts; attempts != adminSignInFailureThreshold {
		t.Fatalf("failed attempts while locked = %d, want %d", attempts, adminSignInFailureThreshold)
	}

	// After the lock expires, one more wrong password locks the account again.
	now = now.Add(adminSignInLockDuration)
	if _, err := service.AuthenticatePassword(ctx, "owner", "wrong horse battery"); !errors.As(err, &locked) {
		t.Fatalf("attempt after lock expiry error = %v, want a new lock", err)
	}
	now = now.Add(adminSignInLockDuration)
	if _, err := service.AuthenticatePassword(ctx, "owner", testAdminPassword); err != nil {
		t.Fatalf("sign-in after lock expiry error = %v", err)
	}
	if record := store.users[user.ID]; record.FailedAttempts != 0 || record.LockedUntil != nil {
		t.Fatalf("account state after successful sign-in = %#v, want cleared failures", record)
	}
}

// TestAdminUserPasswordChangeEndsEarlierSessions verifies the current-password check and session invalidation.
func TestAdminUserPasswordChangeEndsEarlierSessions(t *testing.T) {
	ctx := context.Background()
	service, store := newAdminUserTestService(t)
	now := time.Date(2026, time.November, 22, 10, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now }
	sessions, err := NewAdminSessions(store, newCustomerSessionKeyring(t))
	if err != nil {
		t.Fatalf("NewAdminSessions() error = %v", err)
	}
	sessions.clock = func() time.Time { return now }
	user, err := service.CreateAdminUser(ctx, "owner", testAdminPassword)
	if err != nil {
		t.Fatalf("CreateAdminUser() error = %v", err)
	}
	principal, err := service.AuthenticatePassword(ctx, "owner", testAdminPassword)
	if err != nil {
		t.Fatalf("AuthenticatePassword() error = %v", err)
	}
	earlier, err := sessions.Mint(ctx, principal)
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	authenticated, err := sessions.Authenticate(ctx, earlier.Token)
	if err != nil || authenticated.UserID != user.ID || authenticated.Username != "owner" || authenticated.KeyID != "" {
		t.Fatalf("Authenticate() account session = %#v, %v", authenticated, err)
	}

	now = now.Add(time.Hour)
	if _, err := service.ChangeAdminUserPassword(
		ctx, user.ID, "wrong horse battery", true, testAdminReplacementPassword,
	); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("ChangeAdminUserPassword() wrong current password error = %v, want mismatch", err)
	}
	if _, err := service.ChangeAdminUserPassword(ctx, user.ID, testAdminPassword, true, "short"); !errors.Is(err, validation.ErrInvalid) {
		t.Fatalf("ChangeAdminUserPassword() weak password error = %v, want invalid", err)
	}
	if _, err := service.ChangeAdminUserPassword(ctx, "missing", "", false, testAdminReplacementPassword); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("ChangeAdminUserPassword() missing account error = %v, want not found", err)
	}
	changed, err := service.ChangeAdminUserPassword(ctx, user.ID, testAdminPassword, true, testAdminReplacementPassword)
	if err != nil || !changed.PasswordChangedAt.Equal(now) {
		t.Fatalf("ChangeAdminUserPassword() = %#v, %v", changed, err)
	}
	if record := store.users[user.ID]; record.FailedAttempts != 0 {
		t.Fatalf("failed attempts after password change = %d, want 0", record.FailedAttempts)
	}
	if _, err := sessions.Authenticate(ctx, earlier.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate() session issued before the change error = %v, want unauthorized", err)
	}
	// A session minted at the same instant as the change belongs to the administrator who made it.
	replacement, err := sessions.Mint(ctx, principal)
	if err != nil {
		t.Fatalf("Mint() replacement error = %v", err)
	}
	if _, err := sessions.Authenticate(ctx, replacement.Token); err != nil {
		t.Fatalf("Authenticate() replacement session error = %v", err)
	}
	if _, err := service.AuthenticatePassword(ctx, "owner", testAdminPassword); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticatePassword() old password error = %v, want unauthorized", err)
	}
	if _, err := service.AuthenticatePassword(ctx, "owner", testAdminReplacementPassword); err != nil {
		t.Fatalf("AuthenticatePassword() new password error = %v", err)
	}

	// The final account cannot be deleted while no administrator key exists.
	if err := service.DeleteAdminUser(ctx, user.ID); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("DeleteAdminUser() final administrator error = %v, want conflict", err)
	}
	if _, err := service.Create(ctx, Principal{Role: persistence.APIKeyRoleAdmin}); err != nil {
		t.Fatalf("Create() administrator key error = %v", err)
	}
	if err := service.DeleteAdminUser(ctx, user.ID); err != nil {
		t.Fatalf("DeleteAdminUser() error = %v", err)
	}
	if _, err := sessions.Authenticate(ctx, replacement.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate() deleted account session error = %v, want unauthorized", err)
	}
}

// TestResetAdminUserPasswordRecoversAccess verifies the out-of-band reset creates or repairs an account.
func TestResetAdminUserPasswordRecoversAccess(t *testing.T) {
	ctx := context.Background()
	service, store := newAdminUserTestService(t)
	now := time.Date(2026, time.November, 22, 10, 0, 0, 0, time.UTC)
	service.clock = func() time.Time { return now }

	created, err := service.ResetAdminUserPassword(ctx, " Owner ")
	if err != nil || !created.Created || created.Username != "owner" || len(created.Password) != 20 {
		t.Fatalf("ResetAdminUserPassword() new account = %#v, %v", created, err)
	}
	if _, err := service.AuthenticatePassword(ctx, "owner", created.Password); err != nil {
		t.Fatalf("AuthenticatePassword() generated password error = %v", err)
	}
	for attempt := 0; attempt < adminSignInFailureThreshold; attempt++ {
		_, _ = service.AuthenticatePassword(ctx, "owner", "wrong horse battery")
	}
	var locked *LockedError
	if _, err := service.AuthenticatePassword(ctx, "owner", created.Password); !errors.As(err, &locked) {
		t.Fatalf("AuthenticatePassword() locked account error = %v, want a lock", err)
	}

	now = now.Add(time.Second)
	reset, err := service.ResetAdminUserPassword(ctx, "owner")
	if err != nil || reset.Created || reset.Password == created.Password {
		t.Fatalf("ResetAdminUserPassword() existing account = %#v, %v", reset, err)
	}
	if len(store.users) != 1 {
		t.Fatalf("stored accounts after reset = %d, want 1", len(store.users))
	}
	if _, err := service.AuthenticatePassword(ctx, "owner", created.Password); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("AuthenticatePassword() replaced password error = %v, want unauthorized", err)
	}
	if _, err := service.AuthenticatePassword(ctx, "owner", reset.Password); err != nil {
		t.Fatalf("AuthenticatePassword() reset password on an unlocked account error = %v", err)
	}
	if _, err := service.ResetAdminUserPassword(ctx, "no"); !errors.Is(err, validation.ErrInvalid) {
		t.Fatalf("ResetAdminUserPassword() invalid username error = %v, want invalid", err)
	}
}

// newAdminUserTestService constructs authentication over an empty in-memory store with a bootstrap key.
func newAdminUserTestService(t *testing.T) (*Service, *fakeOperationsStore) {
	t.Helper()

	store := &fakeOperationsStore{records: make(map[string]persistence.APIKeyRecord)}
	service, err := NewService(store, testBootstrapAdminKey, 4)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, store
}

// TestPrincipalActorIDNamesKeysAndAccounts verifies the audit identity of each administrator kind.
func TestPrincipalActorIDNamesKeysAndAccounts(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		principal Principal
		want      string
	}{
		"stored key": {Principal{KeyID: "0123456789abcdef01234567", Role: persistence.APIKeyRoleAdmin}, "0123456789abcdef01234567"},
		"account":    {Principal{UserID: "account-1", Username: "owner", Role: persistence.APIKeyRoleAdmin}, "account:owner"},
		"bootstrap":  {Principal{Role: persistence.APIKeyRoleAdmin}, ""},
	} {
		if got := test.principal.ActorID(); got != test.want {
			t.Fatalf("%s ActorID() = %q, want %q", name, got, test.want)
		}
	}
}

// TestPasswordDerivationsDoNotTakeAPIKeySlots verifies a saturated password pool leaves API key authentication working.
func TestPasswordDerivationsDoNotTakeAPIKeySlots(t *testing.T) {
	ctx := context.Background()
	service, _ := newAdminUserTestService(t)
	if _, err := service.CreateAdminUser(ctx, "owner", testAdminPassword); err != nil {
		t.Fatalf("CreateAdminUser() error = %v", err)
	}
	bearer, err := service.Create(ctx, Principal{Role: persistence.APIKeyRoleAdmin})
	if err != nil {
		t.Fatalf("Create() administrator key error = %v", err)
	}
	for slot := 0; slot < cap(service.passwordSlots); slot++ {
		service.passwordSlots <- struct{}{}
	}

	for _, username := range []string{"owner", "nobody"} {
		if _, err := service.AuthenticatePassword(ctx, username, testAdminPassword); !errors.Is(err, ErrCapacity) {
			t.Fatalf("AuthenticatePassword(%s) with a full password pool error = %v, want ErrCapacity", username, err)
		}
	}
	if _, err := service.Authenticate(ctx, bearer); err != nil {
		t.Fatalf("Authenticate() API key with a full password pool error = %v", err)
	}
}
