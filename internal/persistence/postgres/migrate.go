// Package postgres implements PostgreSQL-backed persistence infrastructure.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/jackc/pgx/v5"
	ternmigrate "github.com/jackc/tern/v2/migrate"
)

const (
	// LatestVersion identifies the newest schema version embedded in this build.
	LatestVersion int32 = 6
	// migrationsDirectory is the embedded directory containing sequential SQL migrations.
	migrationsDirectory = "migrations"
	// schemaVersionTable stores the single current migration version for IAPStack.
	schemaVersionTable = "public.schema_version"
)

// Migrator applies the embedded IAPStack schema to one PostgreSQL connection.
type Migrator struct {
	connection *pgx.Conn
	runner     *ternmigrate.Migrator
}

var (
	// ErrDatabaseURLRequired indicates that a database-dependent mode has no connection URL.
	ErrDatabaseURLRequired = errors.New("IAPSTACK_DATABASE_URL is required")
	// ErrInvalidDatabaseURL indicates that PostgreSQL connection configuration cannot be parsed.
	ErrInvalidDatabaseURL = errors.New("IAPSTACK_DATABASE_URL is not a valid PostgreSQL connection string")
)

// embeddedMigrations contains every SQL migration shipped with the IAPStack binary.
//
//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// OpenMigrator validates a database URL, opens a connection, and loads embedded migrations.
func OpenMigrator(ctx context.Context, databaseURL string) (*Migrator, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, ErrDatabaseURLRequired
	}

	connectionConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, ErrInvalidDatabaseURL
	}
	connectionConfig.RuntimeParams["application_name"] = "iapstack-migrate"

	connection, err := pgx.ConnectConfig(ctx, connectionConfig)
	if err != nil {
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}

	migrator, err := NewMigrator(ctx, connection)
	if err != nil {
		_ = connection.Close(ctx)
		return nil, err
	}
	return migrator, nil
}

// NewMigrator loads the embedded migration set for an existing PostgreSQL connection.
func NewMigrator(ctx context.Context, connection *pgx.Conn) (*Migrator, error) {
	if connection == nil {
		return nil, errors.New("PostgreSQL connection is required")
	}

	migrationFiles, err := fs.Sub(embeddedMigrations, migrationsDirectory)
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}

	runner, err := ternmigrate.NewMigrator(ctx, connection, schemaVersionTable)
	if err != nil {
		return nil, fmt.Errorf("initialize migration runner: %w", err)
	}
	if err := runner.LoadMigrations(migrationFiles); err != nil {
		return nil, fmt.Errorf("load embedded migrations: %w", err)
	}
	if len(runner.Migrations) != int(LatestVersion) {
		return nil, fmt.Errorf("embedded migration count is %d, expected %d", len(runner.Migrations), LatestVersion)
	}

	return &Migrator{connection: connection, runner: runner}, nil
}

// Migrate opens PostgreSQL, applies every pending migration, and closes the connection.
func Migrate(ctx context.Context, databaseURL string) (err error) {
	migrator, err := OpenMigrator(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, migrator.Close(ctx))
	}()

	return migrator.Up(ctx)
}

// Up advances the database to the latest schema version embedded in this build.
func (migrator *Migrator) Up(ctx context.Context) error {
	return migrator.MigrateTo(ctx, LatestVersion)
}

// MigrateTo moves the database forward or backward to an explicit schema version.
func (migrator *Migrator) MigrateTo(ctx context.Context, version int32) error {
	if err := migrator.runner.MigrateTo(ctx, version); err != nil {
		return fmt.Errorf("migrate PostgreSQL to version %d: %w", version, err)
	}
	return nil
}

// Version returns the currently applied schema version.
func (migrator *Migrator) Version(ctx context.Context) (int32, error) {
	version, err := migrator.runner.GetCurrentVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read PostgreSQL schema version: %w", err)
	}
	return version, nil
}

// Close releases the PostgreSQL connection owned by the migrator.
func (migrator *Migrator) Close(ctx context.Context) error {
	if migrator == nil || migrator.connection == nil {
		return nil
	}
	return migrator.connection.Close(ctx)
}
