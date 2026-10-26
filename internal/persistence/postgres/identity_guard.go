package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ClearIdentityMigrationGuards removes rows that block reversing the customer identity migration.
// Tests use it before migrating to version 0. Production reversal still refuses while those rows exist.
func ClearIdentityMigrationGuards(ctx context.Context, connection *pgx.Conn) error {
	_, err := connection.Exec(ctx, `
		DO $$
		BEGIN
			IF to_regclass('public.customer_aliases') IS NOT NULL THEN
				DELETE FROM customer_aliases;
			END IF;
			IF to_regclass('public.customer_merges') IS NOT NULL THEN
				DELETE FROM customer_merges;
			END IF;
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = 'customers' AND column_name = 'merged_into_customer_id'
			) THEN
				UPDATE customers SET merged_into_customer_id = NULL WHERE merged_into_customer_id IS NOT NULL;
			END IF;
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = 'customer_entitlements' AND column_name = 'source_customer_id'
			) THEN
				DELETE FROM customer_entitlements
				WHERE source_customer_id IS DISTINCT FROM customer_id;
			END IF;
			IF EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public' AND table_name = 'customer_entitlements' AND column_name = 'source_kind'
			) THEN
				DELETE FROM customer_entitlements
				WHERE source_kind IS DISTINCT FROM 'store_observation'
					OR source_import_id IS NOT NULL
					OR source_observation_id IS NULL;
			END IF;
			IF to_regclass('public.import_grants') IS NOT NULL THEN
				DELETE FROM import_grants;
			END IF;
			IF to_regclass('public.import_batches') IS NOT NULL THEN
				DELETE FROM import_batches;
			END IF;
		END $$
	`)
	return err
}
