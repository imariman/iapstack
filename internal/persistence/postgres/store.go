package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store implements provider-neutral persistence with a PostgreSQL connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// transaction implements every repository against one PostgreSQL transaction.
type transaction struct {
	tx pgx.Tx
}

var (
	// storeContract verifies that Store implements the durable storage boundary.
	_ persistence.Store = (*Store)(nil)
	// operationsStoreContract verifies that Store implements operational atomic work.
	_ persistence.OperationsStore = (*Store)(nil)
	// transactionContract verifies that transaction implements every repository boundary.
	_ persistence.Transaction = (*transaction)(nil)
)

// OpenStore validates configuration, opens a PostgreSQL pool, and verifies connectivity.
func OpenStore(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, ErrDatabaseURLRequired
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, ErrInvalidDatabaseURL
	}
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "iapstack"

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	store, err := NewStore(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

// NewStore validates an existing PostgreSQL pool and verifies connectivity.
func NewStore(ctx context.Context, pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("PostgreSQL pool is required")
	}
	store := &Store{pool: pool}
	if err := store.Ping(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// Ping verifies that PostgreSQL accepts a query through the pool.
func (store *Store) Ping(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return errors.New("PostgreSQL store is not initialized")
	}
	if err := store.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return nil
}

// Transact executes one callback in a serializable PostgreSQL transaction.
func (store *Store) Transact(ctx context.Context, operation persistence.TransactionFunc) (err error) {
	if operation == nil {
		return errors.New("persistence transaction operation is required")
	}
	return store.transact(ctx, func(repository *transaction) error {
		return operation(repository)
	})
}

// Operate executes one control-plane or queue callback in a serializable transaction.
func (store *Store) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	if operation == nil {
		return errors.New("persistence operations callback is required")
	}
	return store.transact(ctx, func(repository *transaction) error {
		return operation(repository)
	})
}

// transact executes one concrete callback in a serializable PostgreSQL transaction.
func (store *Store) transact(ctx context.Context, operation func(*transaction) error) (err error) {
	if store == nil || store.pool == nil {
		return errors.New("PostgreSQL store is not initialized")
	}

	postgresTransaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin PostgreSQL transaction: %w", err)
	}
	defer func() {
		rollbackError := postgresTransaction.Rollback(ctx)
		if rollbackError != nil && !errors.Is(rollbackError, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("roll back PostgreSQL transaction: %w", rollbackError))
		}
	}()

	if err := operation(&transaction{tx: postgresTransaction}); err != nil {
		return err
	}
	if err := postgresTransaction.Commit(ctx); err != nil {
		return classifyError("commit transaction", err)
	}
	return nil
}

// Close releases every connection owned by the PostgreSQL pool.
func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}
