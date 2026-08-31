package webhookreceiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// receiverSchema creates the bounded persistent identity table used by the sandbox receiver.
	receiverSchema = `
CREATE TABLE IF NOT EXISTS iapstack_webhook_receiver_events (
    event_id text PRIMARY KEY,
    application_id text NOT NULL,
    event_type text NOT NULL,
    body_sha256 bytea NOT NULL CHECK (octet_length(body_sha256) = 32),
    received_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS iapstack_webhook_receiver_events_received_idx
    ON iapstack_webhook_receiver_events (received_at DESC);`
)

// PostgresStore persists webhook event identities in PostgreSQL.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// OpenPostgresStore opens bounded receiver persistence and creates its isolated table.
func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	if databaseURL == "" {
		return nil, errors.New("webhook receiver database URL is required")
	}
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("webhook receiver database URL is invalid")
	}
	configuration.MaxConns = 2
	configuration.MinConns = 0
	configuration.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		return nil, errors.New("open webhook receiver database")
	}
	store := &PostgresStore{pool: pool}
	if _, err := pool.Exec(ctx, receiverSchema); err != nil {
		pool.Close()
		return nil, errors.New("initialize webhook receiver database")
	}
	return store, nil
}

// Ping verifies that PostgreSQL can serve deduplication reads and writes.
func (store *PostgresStore) Ping(ctx context.Context) error {
	return store.pool.Ping(ctx)
}

// Save inserts one event or compares an existing identity with its authenticated body.
func (store *PostgresStore) Save(ctx context.Context, event Event) (SaveResult, error) {
	var insertedID string
	err := store.pool.QueryRow(ctx, `
INSERT INTO iapstack_webhook_receiver_events (
    event_id, application_id, event_type, body_sha256, received_at
) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (event_id) DO NOTHING
RETURNING event_id`,
		event.ID,
		event.ApplicationID,
		event.EventType,
		event.BodyFingerprint[:],
		event.ReceivedAt,
	).Scan(&insertedID)
	if err == nil {
		return SaveInserted, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("insert webhook receiver event: %w", err)
	}
	var fingerprint []byte
	if err := store.pool.QueryRow(ctx, `
SELECT body_sha256
FROM iapstack_webhook_receiver_events
WHERE event_id = $1`, event.ID).Scan(&fingerprint); err != nil {
		return "", fmt.Errorf("load webhook receiver event: %w", err)
	}
	if bytes.Equal(fingerprint, event.BodyFingerprint[:]) {
		return SaveDuplicate, nil
	}
	return SaveConflict, nil
}

// Close releases the PostgreSQL connection pool.
func (store *PostgresStore) Close() {
	store.pool.Close()
}
