package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

var (
	// ErrRiverSchemaVersionMismatch indicates that River has unapplied migrations for this build.
	ErrRiverSchemaVersionMismatch = errors.New("River schema version does not match this IAPStack build")
)

// MigrateRiver applies every River migration bundled with the pinned dependency.
func MigrateRiver(ctx context.Context, databaseURL string) error {
	pool, err := openRiverPool(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	migrator, err := newRiverMigrator(pool)
	if err != nil {
		return err
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("migrate River schema: %w", err)
	}
	return nil
}

// RemoveRiver removes every River schema object and is intended for migration lifecycle tests.
func RemoveRiver(ctx context.Context, databaseURL string) error {
	pool, err := openRiverPool(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	migrator, err := newRiverMigrator(pool)
	if err != nil {
		return err
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionDown, &rivermigrate.MigrateOpts{
		TargetVersion: -1,
	}); err != nil {
		return fmt.Errorf("remove River schema: %w", err)
	}
	return nil
}

// validateRiverSchema verifies that every migration bundled with River is applied.
func validateRiverSchema(ctx context.Context, pool *pgxpool.Pool) error {
	migrator, err := newRiverMigrator(pool)
	if err != nil {
		return err
	}
	result, err := migrator.Validate(ctx)
	if err != nil {
		return fmt.Errorf("validate River schema: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("River migration state is incomplete: %w", ErrRiverSchemaVersionMismatch)
	}
	return nil
}

// newRiverMigrator creates a quiet migrator for the main River migration line.
func newRiverMigrator(pool *pgxpool.Pool) (*rivermigrate.Migrator[pgx.Tx], error) {
	return rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// openRiverPool validates the configured URL and opens one migration-only pool.
func openRiverPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, ErrDatabaseURLRequired
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, ErrInvalidDatabaseURL
	}
	config.ConnConfig.RuntimeParams["application_name"] = "iapstack-river-migrate"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open River migration pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping River migration PostgreSQL: %w", err)
	}
	return pool, nil
}
