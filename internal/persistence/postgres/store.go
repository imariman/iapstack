package postgres

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/imariman/iapstack/internal/persistence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

const (
	// maximumTransactionAttempts bounds retries for serialization failures and deadlocks.
	maximumTransactionAttempts = 4
	// transactionRetryBase is the first full-jitter retry ceiling.
	transactionRetryBase = 5 * time.Millisecond
	// transactionRetryMaximum caps transaction retry delay under repeated contention.
	transactionRetryMaximum = 100 * time.Millisecond
	// runtimeStatementTimeout bounds one PostgreSQL statement independently of request cancellation.
	runtimeStatementTimeout = 15 * time.Second
	// runtimeLockTimeout bounds time spent waiting to acquire a PostgreSQL lock.
	runtimeLockTimeout = 5 * time.Second
	// runtimeIdleTransactionTimeout releases sessions abandoned inside a transaction.
	runtimeIdleTransactionTimeout = 30 * time.Second
)

// Store implements provider-neutral persistence with a PostgreSQL connection pool.
type Store struct {
	pool  *pgxpool.Pool
	river *river.Client[pgx.Tx]
}

// transaction implements every repository against one PostgreSQL transaction.
type transaction struct {
	tx    pgx.Tx
	river *river.Client[pgx.Tx]
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
	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = runtimeStatementTimeout.String()
	poolConfig.ConnConfig.RuntimeParams["lock_timeout"] = runtimeLockTimeout.String()
	poolConfig.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = runtimeIdleTransactionTimeout.String()

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
		return nil, errors.New("postgresql pool is required")
	}
	store := &Store{pool: pool}
	if err := store.Ping(ctx); err != nil {
		return nil, err
	}
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, fmt.Errorf("construct River insert client: %w", err)
	}
	store.river = riverClient
	return store, nil
}

// Ping verifies PostgreSQL connectivity and exact runtime schema compatibility.
func (store *Store) Ping(ctx context.Context) error {
	if store == nil || store.pool == nil {
		return errors.New("postgresql store is not initialized")
	}
	if err := store.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL: %w", err)
	}
	var version int32
	if err := store.pool.QueryRow(ctx, `SELECT version FROM public.schema_version`).Scan(&version); err != nil {
		return fmt.Errorf("read PostgreSQL schema version: %w", err)
	}
	if version != LatestVersion {
		return fmt.Errorf("postgresql schema version is %d, require %d: %w", version, LatestVersion, ErrSchemaVersionMismatch)
	}
	return validateRiverSchema(ctx, store.pool)
}

// Transact executes one callback in a serializable PostgreSQL transaction.
func (store *Store) Transact(ctx context.Context, operation persistence.TransactionFunc) (err error) {
	if operation == nil {
		return errors.New("persistence transaction operation is required")
	}
	return store.transact(ctx, pgx.Serializable, func(repository *transaction) error {
		return operation(repository)
	})
}

// Operate executes one control-plane or queue callback with row-lock-compatible isolation.
func (store *Store) Operate(ctx context.Context, operation persistence.OperationsFunc) error {
	if operation == nil {
		return errors.New("persistence operations callback is required")
	}
	return store.transact(ctx, pgx.ReadCommitted, func(repository *transaction) error {
		return operation(repository)
	})
}

// transact executes one concrete callback at the requested PostgreSQL isolation level.
func (store *Store) transact(
	ctx context.Context,
	isolation pgx.TxIsoLevel,
	operation func(*transaction) error,
) (err error) {
	if store == nil || store.pool == nil {
		return errors.New("postgresql store is not initialized")
	}
	for attempt := 1; attempt <= maximumTransactionAttempts; attempt++ {
		err = store.transactOnce(ctx, isolation, operation)
		if !errors.Is(err, errRetryableTransaction) {
			return err
		}
		if attempt == maximumTransactionAttempts {
			return fmt.Errorf(
				"PostgreSQL transaction retries exhausted after %d attempts: %w",
				maximumTransactionAttempts,
				persistence.ErrUnavailable,
			)
		}
		delay := transactionRetryDelay(attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

// transactOnce executes one PostgreSQL transaction attempt and always releases its connection.
func (store *Store) transactOnce(
	ctx context.Context,
	isolation pgx.TxIsoLevel,
	operation func(*transaction) error,
) (err error) {

	postgresTransaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation})
	if err != nil {
		return classifyError("begin PostgreSQL transaction", err)
	}
	defer func() {
		rollbackContext, cancelRollback := context.WithTimeout(
			context.WithoutCancel(ctx),
			runtimeLockTimeout,
		)
		defer cancelRollback()
		rollbackError := postgresTransaction.Rollback(rollbackContext)
		if rollbackError != nil && !errors.Is(rollbackError, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("roll back PostgreSQL transaction: %w", rollbackError))
		}
	}()

	if err := operation(&transaction{tx: postgresTransaction, river: store.river}); err != nil {
		return err
	}
	if err := postgresTransaction.Commit(ctx); err != nil {
		return classifyError("commit transaction", err)
	}
	return nil
}

// Pool returns the shared PostgreSQL pool used by runtime infrastructure.
func (store *Store) Pool() *pgxpool.Pool {
	if store == nil {
		return nil
	}
	return store.pool
}

// transactionRetryDelay returns exponential full jitter for one completed transaction attempt.
func transactionRetryDelay(attempt int) time.Duration {
	ceiling := transactionRetryBase
	for index := 1; index < attempt && ceiling < transactionRetryMaximum/2; index++ {
		ceiling *= 2
	}
	if ceiling > transactionRetryMaximum {
		ceiling = transactionRetryMaximum
	}
	random, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(ceiling)+1))
	if err != nil {
		return ceiling / 2
	}
	return time.Duration(random.Int64())
}

// Close releases every connection owned by the PostgreSQL pool.
func (store *Store) Close() {
	if store != nil && store.pool != nil {
		store.pool.Close()
	}
}
