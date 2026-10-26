-- Record one-way historical grants without treating the export as a store observation.
CREATE TABLE import_grants (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    customer_id text NOT NULL,
    application_id text NOT NULL,
    product_id text NOT NULL,
    entitlement_id text NOT NULL,
    origin text NOT NULL,
    origin_store text NOT NULL,
    environment text NOT NULL,
    access_status text NOT NULL,
    access_reason text NOT NULL,
    effective_starts_at timestamptz,
    effective_ends_at timestamptz,
    observed_at timestamptz NOT NULL,
    export_sha256 bytea NOT NULL,
    lineage_fingerprint bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT import_grants_lineage_unique UNIQUE (
        project_id, customer_id, application_id, product_id, entitlement_id, lineage_fingerprint
    ),
    CONSTRAINT import_grants_customer_fk FOREIGN KEY (customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT import_grants_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT import_grants_product_fk FOREIGN KEY (product_id, project_id)
        REFERENCES products (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT import_grants_entitlement_fk FOREIGN KEY (entitlement_id, project_id)
        REFERENCES entitlements (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT import_grants_mapping_fk FOREIGN KEY (product_id, entitlement_id)
        REFERENCES product_entitlements (product_id, entitlement_id) ON DELETE RESTRICT,
    CONSTRAINT import_grants_id_valid CHECK (id ~ '^imp-[0-9a-f]{32}$'),
    CONSTRAINT import_grants_origin_valid CHECK (origin = 'revenuecat'),
    CONSTRAINT import_grants_origin_store_valid CHECK (
        origin_store <> '' AND origin_store = btrim(origin_store) AND origin_store !~ '[[:cntrl:]]'
    ),
    CONSTRAINT import_grants_environment_valid CHECK (environment IN ('production', 'sandbox')),
    CONSTRAINT import_grants_access_valid CHECK (access_status IN ('allowed', 'denied', 'unresolved')),
    CONSTRAINT import_grants_reason_valid CHECK (
        access_reason IN (
            'purchase_valid',
            'grace_period',
            'pending_payment',
            'canceled_at_period_end',
            'expired',
            'billing_issue',
            'paused',
            'refunded',
            'revoked',
            'provider_decision',
            'unresolved'
        )
    ),
    CONSTRAINT import_grants_period_valid CHECK (
        (effective_starts_at IS NULL AND effective_ends_at IS NULL)
        OR (effective_starts_at IS NOT NULL AND (effective_ends_at IS NULL OR effective_ends_at > effective_starts_at))
    ),
    CONSTRAINT import_grants_allowed_valid CHECK (
        access_status <> 'allowed' OR effective_starts_at IS NOT NULL
    ),
    CONSTRAINT import_grants_sha_valid CHECK (octet_length(export_sha256) = 32),
    CONSTRAINT import_grants_lineage_valid CHECK (octet_length(lineage_fingerprint) = 32)
);

CREATE INDEX import_grants_customer_idx
    ON import_grants (project_id, customer_id, entitlement_id, application_id, product_id, observed_at DESC, id);

CREATE TABLE import_batches (
    project_id text NOT NULL,
    batch_id text NOT NULL,
    export_sha256 bytea NOT NULL,
    format text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, batch_id),
    CONSTRAINT import_batches_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT import_batches_id_valid CHECK (
        batch_id <> '' AND batch_id = btrim(batch_id) AND batch_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT import_batches_sha_valid CHECK (octet_length(export_sha256) = 32),
    CONSTRAINT import_batches_format_valid CHECK (format IN ('transactions-csv', 'subscriber-jsonl'))
);

ALTER TABLE customer_entitlements
    ADD COLUMN source_kind text NOT NULL DEFAULT 'store_observation',
    ADD COLUMN source_import_id text;

ALTER TABLE customer_entitlements
    ALTER COLUMN source_observation_id DROP NOT NULL;

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
    ),
    ADD CONSTRAINT customer_entitlements_source_import_fk FOREIGN KEY (source_import_id)
        REFERENCES import_grants (id) ON DELETE RESTRICT;

---- create above / drop below ----

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM import_grants) THEN
        RAISE EXCEPTION 'cannot reverse import grants migration while import grants exist';
    END IF;
    IF EXISTS (SELECT 1 FROM import_batches) THEN
        RAISE EXCEPTION 'cannot reverse import grants migration while import batches exist';
    END IF;
    IF EXISTS (
        SELECT 1 FROM customer_entitlements
        WHERE source_kind IS DISTINCT FROM 'store_observation'
            OR source_import_id IS NOT NULL
            OR source_observation_id IS NULL
    ) THEN
        RAISE EXCEPTION 'cannot reverse import grants migration while import projections exist';
    END IF;
END $$;

ALTER TABLE customer_entitlements
    DROP CONSTRAINT customer_entitlements_source_import_fk,
    DROP CONSTRAINT customer_entitlements_source_exclusive,
    DROP CONSTRAINT customer_entitlements_source_kind_valid;

ALTER TABLE customer_entitlements
    DROP COLUMN source_import_id,
    DROP COLUMN source_kind;

ALTER TABLE customer_entitlements
    ALTER COLUMN source_observation_id SET NOT NULL;

DROP TABLE import_batches;
DROP TABLE import_grants;
