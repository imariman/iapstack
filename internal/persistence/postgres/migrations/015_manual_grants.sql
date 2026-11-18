-- Record operator-issued access as an audited projection source that is never store evidence,
-- and index the customer support search and timeline reads.
CREATE TABLE manual_grants (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    customer_id text NOT NULL,
    application_id text NOT NULL,
    product_id text NOT NULL,
    entitlement_id text NOT NULL,
    reason text NOT NULL,
    actor_key_id text NOT NULL,
    idempotency_key text NOT NULL,
    effective_starts_at timestamptz NOT NULL,
    effective_ends_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoked_by_key_id text,
    revocation_reason text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT manual_grants_idempotency_unique UNIQUE (project_id, idempotency_key),
    CONSTRAINT manual_grants_customer_fk FOREIGN KEY (customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT manual_grants_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT manual_grants_product_fk FOREIGN KEY (product_id, project_id)
        REFERENCES products (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT manual_grants_entitlement_fk FOREIGN KEY (entitlement_id, project_id)
        REFERENCES entitlements (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT manual_grants_mapping_fk FOREIGN KEY (product_id, entitlement_id)
        REFERENCES product_entitlements (product_id, entitlement_id) ON DELETE RESTRICT,
    CONSTRAINT manual_grants_id_valid CHECK (id ~ '^mgr-[0-9a-f]{32}$'),
    CONSTRAINT manual_grants_reason_valid CHECK (
        reason = btrim(reason) AND char_length(reason) BETWEEN 1 AND 500 AND reason !~ '[[:cntrl:]]'
    ),
    CONSTRAINT manual_grants_actor_valid CHECK (
        actor_key_id <> '' AND actor_key_id = btrim(actor_key_id) AND actor_key_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT manual_grants_idempotency_key_valid CHECK (
        idempotency_key <> ''
        AND idempotency_key = btrim(idempotency_key)
        AND char_length(idempotency_key) <= 200
        AND idempotency_key !~ '[[:cntrl:]]'
    ),
    CONSTRAINT manual_grants_period_valid CHECK (effective_ends_at > effective_starts_at),
    CONSTRAINT manual_grants_revocation_valid CHECK (
        (revoked_at IS NULL AND revoked_by_key_id IS NULL AND revocation_reason IS NULL)
        OR (
            revoked_at IS NOT NULL
            AND revoked_by_key_id IS NOT NULL
            AND revocation_reason IS NOT NULL
            AND revoked_at >= effective_starts_at
            AND revoked_by_key_id <> ''
            AND revoked_by_key_id = btrim(revoked_by_key_id)
            AND revoked_by_key_id !~ '[[:cntrl:]]'
            AND revocation_reason = btrim(revocation_reason)
            AND char_length(revocation_reason) BETWEEN 1 AND 500
            AND revocation_reason !~ '[[:cntrl:]]'
        )
    )
);

CREATE INDEX manual_grants_customer_idx
    ON manual_grants (project_id, customer_id, created_at DESC, id);
CREATE INDEX manual_grants_application_idx ON manual_grants (application_id, project_id);
CREATE INDEX manual_grants_product_idx ON manual_grants (product_id, entitlement_id);

ALTER TABLE customer_entitlements
    ADD COLUMN source_manual_grant_id text;

ALTER TABLE customer_entitlements
    DROP CONSTRAINT customer_entitlements_source_exclusive,
    DROP CONSTRAINT customer_entitlements_source_kind_valid;

ALTER TABLE customer_entitlements
    ADD CONSTRAINT customer_entitlements_source_kind_valid CHECK (
        source_kind IN ('store_observation', 'import', 'manual')
    ),
    ADD CONSTRAINT customer_entitlements_source_exclusive CHECK (
        (
            source_kind = 'store_observation'
            AND source_observation_id IS NOT NULL
            AND source_import_id IS NULL
            AND source_manual_grant_id IS NULL
        )
        OR (
            source_kind = 'import'
            AND source_observation_id IS NULL
            AND source_import_id IS NOT NULL
            AND source_manual_grant_id IS NULL
        )
        OR (
            source_kind = 'manual'
            AND source_observation_id IS NULL
            AND source_import_id IS NULL
            AND source_manual_grant_id IS NOT NULL
        )
    ),
    ADD CONSTRAINT customer_entitlements_source_manual_fk FOREIGN KEY (source_manual_grant_id)
        REFERENCES manual_grants (id) ON DELETE RESTRICT;

CREATE INDEX customer_entitlements_source_manual_idx
    ON customer_entitlements (source_manual_grant_id)
    WHERE source_manual_grant_id IS NOT NULL;

-- Customer search: newest-first listing and case-sensitive identifier prefixes inside one project.
CREATE INDEX customers_project_created_idx ON customers (project_id, created_at DESC, id);
CREATE INDEX customers_external_id_pattern_idx ON customers (project_id, external_id text_pattern_ops);
CREATE INDEX customers_id_pattern_idx ON customers (project_id, id text_pattern_ops);

-- Customer timeline: identity audit rows and webhook deliveries read by canonical customer.
CREATE INDEX customer_aliases_canonical_idx
    ON customer_aliases (project_id, canonical_customer_id, created_at DESC);
CREATE INDEX customer_merges_canonical_idx
    ON customer_merges (project_id, canonical_customer_id, created_at DESC);
CREATE INDEX outbox_events_aggregate_idx
    ON outbox_events (project_id, aggregate_type, aggregate_id, occurred_at DESC, id DESC);

---- create above / drop below ----

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM manual_grants) THEN
        RAISE EXCEPTION 'cannot reverse manual grants migration while manual grants exist';
    END IF;
    IF EXISTS (
        SELECT 1 FROM customer_entitlements
        WHERE source_kind = 'manual' OR source_manual_grant_id IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'cannot reverse manual grants migration while manual projections exist';
    END IF;
END $$;

DROP INDEX outbox_events_aggregate_idx;
DROP INDEX customer_merges_canonical_idx;
DROP INDEX customer_aliases_canonical_idx;
DROP INDEX customers_id_pattern_idx;
DROP INDEX customers_external_id_pattern_idx;
DROP INDEX customers_project_created_idx;

DROP INDEX customer_entitlements_source_manual_idx;

ALTER TABLE customer_entitlements
    DROP CONSTRAINT customer_entitlements_source_manual_fk,
    DROP CONSTRAINT customer_entitlements_source_exclusive,
    DROP CONSTRAINT customer_entitlements_source_kind_valid;

ALTER TABLE customer_entitlements
    ADD CONSTRAINT customer_entitlements_source_kind_valid CHECK (
        source_kind IN ('store_observation', 'import')
    ),
    ADD CONSTRAINT customer_entitlements_source_exclusive CHECK (
        (
            source_kind = 'store_observation'
            AND source_observation_id IS NOT NULL
            AND source_import_id IS NULL
        )
        OR (
            source_kind = 'import'
            AND source_observation_id IS NULL
            AND source_import_id IS NOT NULL
        )
    );

ALTER TABLE customer_entitlements
    DROP COLUMN source_manual_grant_id;

DROP TABLE manual_grants;
