-- Record canonical customer identity without rewriting historical purchase rows.
ALTER TABLE customers
    ADD COLUMN merged_into_customer_id text,
    ADD CONSTRAINT customers_merged_into_fk FOREIGN KEY (merged_into_customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE RESTRICT,
    ADD CONSTRAINT customers_merged_into_not_self CHECK (
        merged_into_customer_id IS NULL OR merged_into_customer_id <> id
    );

CREATE INDEX customers_merged_into_idx
    ON customers (project_id, merged_into_customer_id)
    WHERE merged_into_customer_id IS NOT NULL;

CREATE TABLE customer_aliases (
    project_id text NOT NULL,
    external_id text NOT NULL,
    canonical_customer_id text NOT NULL,
    actor_role text NOT NULL,
    actor_key_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, external_id),
    CONSTRAINT customer_aliases_customer_fk FOREIGN KEY (canonical_customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT customer_aliases_actor_role_valid CHECK (actor_role IN ('admin', 'application')),
    CONSTRAINT customer_aliases_actor_key_valid CHECK (
        actor_key_id <> '' AND actor_key_id = btrim(actor_key_id)
    ),
    CONSTRAINT customer_aliases_external_id_valid CHECK (
        external_id <> '' AND external_id = btrim(external_id)
    )
);

CREATE TABLE customer_merges (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    canonical_customer_id text NOT NULL,
    source_customer_id text NOT NULL,
    idempotency_key text NOT NULL,
    actor_key_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT customer_merges_idempotency_unique UNIQUE (project_id, idempotency_key),
    CONSTRAINT customer_merges_source_unique UNIQUE (source_customer_id),
    CONSTRAINT customer_merges_distinct_customers CHECK (source_customer_id <> canonical_customer_id),
    CONSTRAINT customer_merges_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT customer_merges_canonical_id_valid CHECK (
        canonical_customer_id <> ''
        AND canonical_customer_id = btrim(canonical_customer_id)
        AND canonical_customer_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT customer_merges_source_id_valid CHECK (
        source_customer_id <> ''
        AND source_customer_id = btrim(source_customer_id)
        AND source_customer_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT customer_merges_idempotency_key_valid CHECK (
        idempotency_key <> ''
        AND idempotency_key = btrim(idempotency_key)
        AND idempotency_key !~ '[[:cntrl:]]'
    ),
    CONSTRAINT customer_merges_actor_key_valid CHECK (
        actor_key_id <> '' AND actor_key_id = btrim(actor_key_id) AND actor_key_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT customer_merges_canonical_fk FOREIGN KEY (canonical_customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT customer_merges_source_fk FOREIGN KEY (source_customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE RESTRICT
);

ALTER TABLE customer_entitlements
    ADD COLUMN source_customer_id text;

UPDATE customer_entitlements
SET source_customer_id = customer_id
WHERE source_customer_id IS NULL;

ALTER TABLE customer_entitlements
    ALTER COLUMN source_customer_id SET NOT NULL,
    ADD CONSTRAINT customer_entitlements_source_customer_valid CHECK (
        source_customer_id <> ''
        AND source_customer_id = btrim(source_customer_id)
        AND source_customer_id !~ '[[:cntrl:]]'
    );

ALTER TABLE customer_entitlements
    DROP CONSTRAINT customer_entitlements_source_fk;

ALTER TABLE customer_entitlements
    ADD CONSTRAINT customer_entitlements_source_fk FOREIGN KEY (
        source_observation_id,
        source_customer_id,
        project_id,
        source_product_id
    ) REFERENCES purchase_observations (id, customer_id, project_id, product_id) ON DELETE RESTRICT;

---- create above / drop below ----

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM customer_aliases) THEN
        RAISE EXCEPTION 'cannot reverse customer identity migration while aliases exist';
    END IF;
    IF EXISTS (SELECT 1 FROM customer_merges) THEN
        RAISE EXCEPTION 'cannot reverse customer identity migration while merges exist';
    END IF;
    IF EXISTS (SELECT 1 FROM customers WHERE merged_into_customer_id IS NOT NULL) THEN
        RAISE EXCEPTION 'cannot reverse customer identity migration while customers are merged';
    END IF;
    IF EXISTS (
        SELECT 1 FROM customer_entitlements
        WHERE source_customer_id IS DISTINCT FROM customer_id
    ) THEN
        RAISE EXCEPTION 'cannot reverse customer identity migration while source_customer_id differs';
    END IF;
END $$;

ALTER TABLE customer_entitlements
    DROP CONSTRAINT customer_entitlements_source_fk;

ALTER TABLE customer_entitlements
    DROP CONSTRAINT customer_entitlements_source_customer_valid,
    DROP COLUMN source_customer_id;

ALTER TABLE customer_entitlements
    ADD CONSTRAINT customer_entitlements_source_fk FOREIGN KEY (
        source_observation_id,
        customer_id,
        project_id,
        source_product_id
    ) REFERENCES purchase_observations (id, customer_id, project_id, product_id) ON DELETE RESTRICT;

DROP TABLE customer_merges;
DROP TABLE customer_aliases;

DROP INDEX customers_merged_into_idx;

ALTER TABLE customers
    DROP CONSTRAINT customers_merged_into_not_self,
    DROP CONSTRAINT customers_merged_into_fk,
    DROP COLUMN merged_into_customer_id;
