package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/imariman/iapstack/internal/auth"
	"github.com/imariman/iapstack/internal/persistence/postgres"
	"github.com/imariman/iapstack/internal/platform/config"
	"github.com/imariman/iapstack/internal/validation"
)

const (
	// adminResetPasswordCommand names the out-of-band administrator password recovery command.
	adminResetPasswordCommand = "reset-password"
)

// runAdminCommand executes one operator command that needs only database access.
// It is the recovery path when no administrator can sign in, so it never asks for a credential:
// whoever can run the binary against the database already controls the installation.
func runAdminCommand(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	output io.Writer,
) error {
	if len(args) != 2 || args[0] != adminResetPasswordCommand {
		return ErrUsage
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	store, err := postgres.OpenStore(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open runtime persistence: %w", err)
	}
	defer store.Close()
	authentication, err := auth.NewService(store, "", 1)
	if err != nil {
		return err
	}
	reset, err := authentication.ResetAdminUserPassword(ctx, args[1])
	if err != nil {
		if errors.Is(err, validation.ErrInvalid) {
			// The process failure log carries only a stable code, so the rule is printed here.
			_, _ = fmt.Fprintln(output,
				"The username must contain 3-64 letters, digits, dots, underscores, at signs, or hyphens, "+
					"and start with a letter or digit.")
			return fmt.Errorf("%w: invalid administrator username", ErrUsage)
		}
		return fmt.Errorf("reset administrator password: %w", err)
	}
	outcome := "The password was reset and the account's earlier dashboard sessions have ended."
	if reset.Created {
		outcome = "The account did not exist and was created."
	}
	_, err = fmt.Fprintf(output,
		"Administrator account: %s\n%s\nTemporary password: %s\n"+
			"Sign in at /dashboard/ with it, then change the password. It is not stored and cannot be shown again.\n",
		reset.Username, outcome, reset.Password)
	return err
}
