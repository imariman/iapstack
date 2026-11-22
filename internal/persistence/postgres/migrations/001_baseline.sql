-- IAPStack baseline schema.
-- This single migration creates the whole schema. It replaces the per-feature migrations that
-- existed before the first release, so a database created by an earlier development build must be
-- recreated rather than migrated.

-- Catalog: projects, applications, customers, products, entitlements, and store product mappings.
CREATE TABLE projects (
    id text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT projects_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]')
);

CREATE TABLE applications (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    provider text NOT NULL,
    environment text NOT NULL,
    provider_application_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT applications_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT applications_id_project_unique UNIQUE (id, project_id),
    CONSTRAINT applications_store_scope_unique UNIQUE (provider, environment, provider_application_id),
    CONSTRAINT applications_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT applications_provider_valid CHECK (provider <> '' AND provider = btrim(provider)),
    CONSTRAINT applications_environment_valid CHECK (environment IN ('production', 'sandbox', 'test')),
    CONSTRAINT applications_provider_id_valid CHECK (
        provider_application_id <> '' AND provider_application_id = btrim(provider_application_id)
    )
);

CREATE TABLE customers (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    external_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    merged_into_customer_id text,
    CONSTRAINT customers_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT customers_id_project_unique UNIQUE (id, project_id),
    CONSTRAINT customers_external_id_unique UNIQUE (project_id, external_id),
    CONSTRAINT customers_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT customers_external_id_valid CHECK (external_id <> '' AND external_id = btrim(external_id)),
    CONSTRAINT customers_merged_into_fk FOREIGN KEY (merged_into_customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT customers_merged_into_not_self CHECK (
        merged_into_customer_id IS NULL OR merged_into_customer_id <> id
    )
);

CREATE TABLE products (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    kind text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT products_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT products_id_project_unique UNIQUE (id, project_id),
    CONSTRAINT products_id_project_kind_unique UNIQUE (id, project_id, kind),
    CONSTRAINT products_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT products_kind_valid CHECK (kind <> '' AND kind = btrim(kind))
);

CREATE TABLE entitlements (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT entitlements_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT entitlements_id_project_unique UNIQUE (id, project_id),
    CONSTRAINT entitlements_key_unique UNIQUE (project_id, key),
    CONSTRAINT entitlements_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT entitlements_key_valid CHECK (key <> '' AND key = btrim(key))
);

CREATE TABLE product_entitlements (
    project_id text NOT NULL,
    product_id text NOT NULL,
    entitlement_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (product_id, entitlement_id),
    CONSTRAINT product_entitlements_product_fk FOREIGN KEY (product_id, project_id)
        REFERENCES products (id, project_id) ON DELETE CASCADE,
    CONSTRAINT product_entitlements_entitlement_fk FOREIGN KEY (entitlement_id, project_id)
        REFERENCES entitlements (id, project_id) ON DELETE CASCADE
);

CREATE TABLE store_products (
    project_id text NOT NULL,
    application_id text NOT NULL,
    product_id text NOT NULL,
    provider_product_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, provider_product_id),
    CONSTRAINT store_products_scope_unique UNIQUE (
        application_id,
        provider_product_id,
        product_id,
        project_id
    ),
    CONSTRAINT store_products_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE CASCADE,
    CONSTRAINT store_products_product_fk FOREIGN KEY (product_id, project_id)
        REFERENCES products (id, project_id) ON DELETE CASCADE,
    CONSTRAINT store_products_provider_id_valid CHECK (
        provider_product_id <> '' AND provider_product_id = btrim(provider_product_id)
    )
);

CREATE INDEX applications_project_idx ON applications (project_id);
CREATE INDEX products_project_idx ON products (project_id);
CREATE INDEX product_entitlements_entitlement_idx ON product_entitlements (entitlement_id);
CREATE INDEX store_products_product_idx ON store_products (product_id);
CREATE INDEX customers_merged_into_idx
    ON customers (project_id, merged_into_customer_id)
    WHERE merged_into_customer_id IS NOT NULL;

-- Customer search: newest-first listing and case-sensitive identifier prefixes inside one project.
CREATE INDEX customers_project_created_idx ON customers (project_id, created_at DESC, id);
CREATE INDEX customers_external_id_pattern_idx ON customers (project_id, external_id text_pattern_ops);
CREATE INDEX customers_id_pattern_idx ON customers (project_id, id text_pattern_ops);

-- Purchase evidence: submitted evidence, verified provider artifacts, normalized observations, and protected provider references.
CREATE TABLE purchase_evidence (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id text NOT NULL,
    application_id text NOT NULL,
    customer_id text NOT NULL,
    kind text NOT NULL,
    content_type text NOT NULL,
    payload_ciphertext bytea NOT NULL,
    payload_fingerprint bytea NOT NULL,
    encryption_key_id text NOT NULL,
    received_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT purchase_evidence_scope_unique UNIQUE (id, application_id, project_id, customer_id),
    CONSTRAINT purchase_evidence_payload_unique UNIQUE (application_id, payload_fingerprint),
    CONSTRAINT purchase_evidence_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT purchase_evidence_customer_fk FOREIGN KEY (customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT purchase_evidence_kind_valid CHECK (kind <> '' AND kind = btrim(kind)),
    CONSTRAINT purchase_evidence_content_type_valid CHECK (content_type <> '' AND content_type = btrim(content_type)),
    CONSTRAINT purchase_evidence_payload_valid CHECK (octet_length(payload_ciphertext) > 0),
    CONSTRAINT purchase_evidence_fingerprint_valid CHECK (octet_length(payload_fingerprint) = 32),
    CONSTRAINT purchase_evidence_key_valid CHECK (encryption_key_id <> '' AND encryption_key_id = btrim(encryption_key_id))
);

CREATE TABLE verified_artifacts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    evidence_id bigint NOT NULL,
    kind text NOT NULL,
    content_type text NOT NULL,
    payload_ciphertext bytea NOT NULL,
    payload_fingerprint bytea NOT NULL,
    encryption_key_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT verified_artifacts_evidence_fk FOREIGN KEY (evidence_id)
        REFERENCES purchase_evidence (id) ON DELETE CASCADE,
    CONSTRAINT verified_artifacts_payload_unique UNIQUE (evidence_id, kind, payload_fingerprint),
    CONSTRAINT verified_artifacts_kind_valid CHECK (kind <> '' AND kind = btrim(kind)),
    CONSTRAINT verified_artifacts_content_type_valid CHECK (content_type <> '' AND content_type = btrim(content_type)),
    CONSTRAINT verified_artifacts_payload_valid CHECK (octet_length(payload_ciphertext) > 0),
    CONSTRAINT verified_artifacts_fingerprint_valid CHECK (octet_length(payload_fingerprint) = 32),
    CONSTRAINT verified_artifacts_key_valid CHECK (encryption_key_id <> '' AND encryption_key_id = btrim(encryption_key_id))
);

CREATE TABLE purchase_observations (
    id text PRIMARY KEY,
    evidence_id bigint NOT NULL,
    project_id text NOT NULL,
    application_id text NOT NULL,
    customer_id text NOT NULL,
    product_id text NOT NULL,
    provider_product_id text NOT NULL,
    product_kind text NOT NULL,
    lifecycle_state text NOT NULL,
    provider_state text NOT NULL,
    access_status text NOT NULL,
    access_reason text NOT NULL,
    ownership text NOT NULL,
    quantity bigint NOT NULL,
    occurred_at timestamptz,
    observed_at timestamptz NOT NULL,
    effective_starts_at timestamptz,
    effective_ends_at timestamptz,
    renewal_mode text NOT NULL,
    renewal_status text NOT NULL,
    next_provider_product_id text,
    next_renewal_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Provider-signed transaction price, kept for non-accounting sandbox value analytics.
    price_milliunits bigint,
    price_currency text,
    -- Provider-signed subscription evidence for subscription metrics. NULL always means not recorded:
    -- a provider whose adapter does not map the field, or a non-subscription observation.
    --   offer_phase: Apple offerType/offerDiscountType, Google Play line-item offerPhase.
    --   transaction_reason: Apple transactionReason (PURCHASE or RENEWAL).
    --   lineage_started_at: Apple originalPurchaseDate.
    --   cancellation_reason: Google Play canceledStateContext.
    offer_phase text,
    transaction_reason text,
    lineage_started_at timestamptz,
    cancellation_reason text,
    CONSTRAINT purchase_observations_application_unique UNIQUE (id, application_id),
    CONSTRAINT purchase_observations_customer_unique UNIQUE (id, customer_id, project_id),
    CONSTRAINT purchase_observations_source_unique UNIQUE (id, customer_id, project_id, product_id),
    CONSTRAINT purchase_observations_evidence_fk FOREIGN KEY (evidence_id, application_id, project_id, customer_id)
        REFERENCES purchase_evidence (id, application_id, project_id, customer_id) ON DELETE RESTRICT,
    CONSTRAINT purchase_observations_store_product_fk FOREIGN KEY (
        application_id,
        provider_product_id,
        product_id,
        project_id
    ) REFERENCES store_products (application_id, provider_product_id, product_id, project_id) ON DELETE RESTRICT,
    CONSTRAINT purchase_observations_product_kind_fk FOREIGN KEY (product_id, project_id, product_kind)
        REFERENCES products (id, project_id, kind) ON DELETE RESTRICT,
    CONSTRAINT purchase_observations_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT purchase_observations_lifecycle_valid CHECK (
        lifecycle_state IN (
            'pending',
            'active',
            'grace_period',
            'on_hold',
            'paused',
            'canceled',
            'expired',
            'refunded',
            'revoked',
            'unresolved'
        )
    ),
    CONSTRAINT purchase_observations_provider_state_valid CHECK (
        provider_state <> '' AND provider_state = btrim(provider_state)
    ),
    CONSTRAINT purchase_observations_access_valid CHECK (access_status IN ('allowed', 'denied', 'unresolved')),
    CONSTRAINT purchase_observations_reason_valid CHECK (
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
    CONSTRAINT purchase_observations_ownership_valid CHECK (ownership IN ('purchased', 'family_shared', 'unknown')),
    CONSTRAINT purchase_observations_quantity_valid CHECK (quantity BETWEEN 1 AND 4294967295),
    CONSTRAINT purchase_observations_period_valid CHECK (
        (effective_starts_at IS NULL AND effective_ends_at IS NULL)
        OR (effective_starts_at IS NOT NULL AND (effective_ends_at IS NULL OR effective_ends_at > effective_starts_at))
    ),
    CONSTRAINT purchase_observations_allowed_valid CHECK (
        access_status <> 'allowed'
        OR (
            occurred_at IS NOT NULL
            AND effective_starts_at IS NOT NULL
            AND (product_kind <> 'subscription' OR effective_ends_at IS NOT NULL)
        )
    ),
    CONSTRAINT purchase_observations_state_access_valid CHECK (
        (lifecycle_state <> 'pending' OR access_status = 'unresolved')
        AND (lifecycle_state <> 'expired' OR access_status = 'denied')
        AND (lifecycle_state <> 'revoked' OR access_status = 'denied')
    ),
    CONSTRAINT purchase_observations_renewal_valid CHECK (
        (
            renewal_mode = 'none'
            AND renewal_status = 'not_applicable'
            AND next_provider_product_id IS NULL
            AND next_renewal_at IS NULL
        )
        OR (
            renewal_mode = 'auto'
            AND renewal_status IN ('enabled', 'disabled', 'unknown')
            AND (
                renewal_status <> 'disabled'
                OR (next_provider_product_id IS NULL AND next_renewal_at IS NULL)
            )
        )
        OR (
            renewal_mode = 'prepaid'
            AND renewal_status = 'not_applicable'
            AND next_provider_product_id IS NULL
            AND next_renewal_at IS NULL
        )
        OR (
            renewal_mode = 'unknown'
            AND renewal_status = 'unknown'
            AND next_provider_product_id IS NULL
            AND next_renewal_at IS NULL
        )
    ),
    CONSTRAINT purchase_observations_product_renewal_valid CHECK (
        product_kind = 'subscription' OR renewal_mode = 'none'
    ),
    CONSTRAINT purchase_observations_next_product_valid CHECK (
        next_provider_product_id IS NULL OR next_provider_product_id = btrim(next_provider_product_id)
    ),
    CONSTRAINT purchase_observations_price_pair_valid CHECK (
        (price_milliunits IS NULL AND price_currency IS NULL)
        OR (price_milliunits IS NOT NULL AND price_currency IS NOT NULL)
    ),
    CONSTRAINT purchase_observations_price_amount_valid CHECK (
        price_milliunits IS NULL OR price_milliunits >= 0
    ),
    CONSTRAINT purchase_observations_price_currency_valid CHECK (
        price_currency IS NULL OR price_currency ~ '^[A-Z]{3}$'
    ),
    CONSTRAINT purchase_observations_offer_phase_valid CHECK (
        offer_phase IS NULL
        OR offer_phase IN ('base', 'free_trial', 'discounted', 'proration', 'unknown')
    ),
    CONSTRAINT purchase_observations_transaction_reason_valid CHECK (
        transaction_reason IS NULL OR transaction_reason IN ('purchase', 'renewal')
    ),
    CONSTRAINT purchase_observations_cancellation_reason_valid CHECK (
        cancellation_reason IS NULL
        OR cancellation_reason IN ('customer', 'system', 'developer', 'replacement')
    ),
    CONSTRAINT purchase_observations_offer_phase_kind_valid CHECK (
        product_kind = 'subscription'
        OR (
            offer_phase IS NULL
            AND transaction_reason IS NULL
            AND lineage_started_at IS NULL
            AND cancellation_reason IS NULL
        )
    )
);

CREATE TABLE provider_references (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    application_id text NOT NULL,
    role text NOT NULL,
    kind text NOT NULL,
    value_ciphertext bytea NOT NULL,
    value_fingerprint bytea NOT NULL,
    encryption_key_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT provider_references_application_unique UNIQUE (id, application_id),
    CONSTRAINT provider_references_value_unique UNIQUE (application_id, role, kind, value_fingerprint),
    CONSTRAINT provider_references_application_fk FOREIGN KEY (application_id)
        REFERENCES applications (id) ON DELETE RESTRICT,
    CONSTRAINT provider_references_role_valid CHECK (
        role IN ('transaction', 'lineage', 'linked_lineage', 'query', 'customer_binding')
    ),
    CONSTRAINT provider_references_kind_valid CHECK (kind <> '' AND kind = btrim(kind)),
    CONSTRAINT provider_references_value_valid CHECK (octet_length(value_ciphertext) > 0),
    CONSTRAINT provider_references_fingerprint_valid CHECK (octet_length(value_fingerprint) = 32),
    CONSTRAINT provider_references_key_valid CHECK (encryption_key_id <> '' AND encryption_key_id = btrim(encryption_key_id))
);

CREATE TABLE observation_references (
    observation_id text NOT NULL,
    application_id text NOT NULL,
    reference_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (observation_id, reference_id),
    CONSTRAINT observation_references_observation_fk FOREIGN KEY (observation_id, application_id)
        REFERENCES purchase_observations (id, application_id) ON DELETE CASCADE,
    CONSTRAINT observation_references_reference_fk FOREIGN KEY (reference_id, application_id)
        REFERENCES provider_references (id, application_id) ON DELETE RESTRICT
);

CREATE INDEX purchase_evidence_customer_idx ON purchase_evidence (customer_id, received_at DESC);
CREATE INDEX purchase_observations_customer_idx ON purchase_observations (customer_id, observed_at DESC);
CREATE INDEX purchase_observations_product_idx ON purchase_observations (application_id, provider_product_id, observed_at DESC);
CREATE INDEX observation_references_reference_idx ON observation_references (reference_id);
CREATE INDEX purchase_observations_evidence_scope_idx
    ON purchase_observations (evidence_id, application_id, project_id, customer_id);
CREATE INDEX purchase_observations_internal_product_idx
    ON purchase_observations (product_id, project_id, product_kind);
CREATE INDEX purchase_observations_sandbox_value_idx
    ON purchase_observations (project_id, occurred_at, application_id)
    INCLUDE (observed_at, price_milliunits, price_currency, lifecycle_state, ownership);
CREATE INDEX purchase_observations_subscription_metrics_idx
    ON purchase_observations (project_id, application_id)
    WHERE product_kind = 'subscription';

-- Durable queues: provider notification inbox and application event outbox.
CREATE TABLE inbox_messages (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    application_id text NOT NULL,
    provider text NOT NULL,
    kind text NOT NULL,
    content_type text NOT NULL,
    payload_ciphertext bytea NOT NULL,
    payload_fingerprint bytea NOT NULL,
    encryption_key_id text NOT NULL,
    received_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    river_job_id bigint UNIQUE,
    processed_at timestamptz,
    failed_at timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT inbox_messages_payload_unique UNIQUE (application_id, payload_fingerprint),
    CONSTRAINT inbox_messages_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT inbox_messages_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT inbox_messages_provider_valid CHECK (provider <> '' AND provider = btrim(provider)),
    CONSTRAINT inbox_messages_kind_valid CHECK (kind <> '' AND kind = btrim(kind)),
    CONSTRAINT inbox_messages_content_type_valid CHECK (content_type <> '' AND content_type = btrim(content_type)),
    CONSTRAINT inbox_messages_payload_valid CHECK (octet_length(payload_ciphertext) > 0),
    CONSTRAINT inbox_messages_fingerprint_valid CHECK (octet_length(payload_fingerprint) = 32),
    CONSTRAINT inbox_messages_key_valid CHECK (encryption_key_id <> '' AND encryption_key_id = btrim(encryption_key_id)),
    CONSTRAINT inbox_messages_schedule_valid CHECK (available_at >= received_at),
    CONSTRAINT inbox_messages_river_job_valid CHECK (river_job_id IS NULL OR river_job_id > 0),
    CONSTRAINT inbox_messages_outcome_valid CHECK (
        NOT (processed_at IS NOT NULL AND failed_at IS NOT NULL)
        AND (last_error_code IS NULL OR failed_at IS NOT NULL)
    )
);

CREATE TABLE outbox_events (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    application_id text NOT NULL,
    event_type text NOT NULL,
    aggregate_type text NOT NULL,
    aggregate_id text NOT NULL,
    payload jsonb NOT NULL,
    payload_fingerprint bytea NOT NULL,
    occurred_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    river_job_id bigint UNIQUE,
    delivered_at timestamptz,
    failed_at timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outbox_events_payload_unique UNIQUE (
        application_id,
        event_type,
        aggregate_type,
        aggregate_id,
        payload_fingerprint
    ),
    CONSTRAINT outbox_events_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT outbox_events_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT outbox_events_type_valid CHECK (event_type <> '' AND event_type = btrim(event_type)),
    CONSTRAINT outbox_events_aggregate_type_valid CHECK (
        aggregate_type <> '' AND aggregate_type = btrim(aggregate_type)
    ),
    CONSTRAINT outbox_events_aggregate_id_valid CHECK (aggregate_id <> '' AND aggregate_id = btrim(aggregate_id)),
    CONSTRAINT outbox_events_payload_valid CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT outbox_events_fingerprint_valid CHECK (octet_length(payload_fingerprint) = 32),
    CONSTRAINT outbox_events_schedule_valid CHECK (available_at >= occurred_at),
    CONSTRAINT outbox_events_river_job_valid CHECK (river_job_id IS NULL OR river_job_id > 0),
    CONSTRAINT outbox_events_outcome_valid CHECK (
        NOT (delivered_at IS NOT NULL AND failed_at IS NOT NULL)
        AND (last_error_code IS NULL OR failed_at IS NOT NULL)
    )
);
CREATE INDEX outbox_events_aggregate_idx
    ON outbox_events (project_id, aggregate_type, aggregate_id, occurred_at DESC, id DESC);

-- Protected provider credential packages per application.
CREATE TABLE application_credentials (
    project_id text NOT NULL,
    application_id text NOT NULL,
    kind text NOT NULL,
    content_type text NOT NULL,
    schema_version integer NOT NULL,
    payload_ciphertext bytea NOT NULL,
    payload_fingerprint bytea NOT NULL,
    encryption_key_id text NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, kind),
    CONSTRAINT application_credentials_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE CASCADE,
    CONSTRAINT application_credentials_kind_valid CHECK (
        kind <> '' AND kind = btrim(kind) AND kind !~ '[[:cntrl:]]'
    ),
    CONSTRAINT application_credentials_content_type_valid CHECK (
        content_type <> '' AND content_type = btrim(content_type) AND content_type !~ '[[:cntrl:]]'
    ),
    CONSTRAINT application_credentials_schema_version_valid CHECK (schema_version > 0),
    CONSTRAINT application_credentials_payload_valid CHECK (octet_length(payload_ciphertext) > 0),
    CONSTRAINT application_credentials_fingerprint_valid CHECK (octet_length(payload_fingerprint) = 32),
    CONSTRAINT application_credentials_key_valid CHECK (
        encryption_key_id <> ''
        AND encryption_key_id = btrim(encryption_key_id)
        AND encryption_key_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT application_credentials_revision_valid CHECK (revision > 0),
    CONSTRAINT application_credentials_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE INDEX application_credentials_project_idx ON application_credentials (project_id, application_id);

-- Operations: API keys, protected webhook configuration, and reconciliation jobs.
-- An agent key names a project and no application. It is read-only unless allow_writes is set, and
-- only an agent key may set allow_writes. Every scoped key references its project, so an agent key
-- cannot point at a missing project or outlive a deleted one.
CREATE TABLE api_keys (
    id text PRIMARY KEY,
    role text NOT NULL,
    project_id text,
    application_id text,
    secret_salt bytea NOT NULL,
    secret_hash bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    allow_writes boolean NOT NULL DEFAULT false,
    CONSTRAINT api_keys_role_valid CHECK (role IN ('admin', 'application', 'agent')),
    CONSTRAINT api_keys_secret_valid CHECK (octet_length(secret_salt) = 16 AND octet_length(secret_hash) = 32),
    CONSTRAINT api_keys_scope_valid CHECK (
        (role = 'admin' AND project_id IS NULL AND application_id IS NULL)
        OR (role = 'application' AND project_id IS NOT NULL AND application_id IS NOT NULL)
        OR (role = 'agent' AND project_id IS NOT NULL AND application_id IS NULL)
    ),
    CONSTRAINT api_keys_allow_writes_valid CHECK (NOT allow_writes OR role = 'agent'),
    CONSTRAINT api_keys_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE CASCADE,
    CONSTRAINT api_keys_project_fk FOREIGN KEY (project_id)
        REFERENCES projects (id) ON DELETE CASCADE
);

CREATE TABLE webhook_endpoints (
    project_id text NOT NULL,
    application_id text PRIMARY KEY,
    url text NOT NULL,
    secret_ciphertext bytea NOT NULL,
    secret_fingerprint bytea NOT NULL,
    encryption_key_id text NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT webhook_endpoints_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE CASCADE,
    CONSTRAINT webhook_endpoints_url_valid CHECK (url <> '' AND url = btrim(url)),
    CONSTRAINT webhook_endpoints_secret_valid CHECK (
        octet_length(secret_ciphertext) > 0 AND octet_length(secret_fingerprint) = 32
    ),
    CONSTRAINT webhook_endpoints_key_valid CHECK (
        encryption_key_id <> '' AND encryption_key_id = btrim(encryption_key_id)
    ),
    CONSTRAINT webhook_endpoints_revision_valid CHECK (revision > 0),
    CONSTRAINT webhook_endpoints_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE TABLE reconciliation_jobs (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    application_id text NOT NULL,
    customer_id text NOT NULL,
    payload_ciphertext bytea NOT NULL,
    payload_fingerprint bytea NOT NULL,
    encryption_key_id text NOT NULL,
    available_at timestamptz NOT NULL,
    river_job_id bigint UNIQUE,
    processed_at timestamptz,
    failed_at timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reconciliation_jobs_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT reconciliation_jobs_customer_fk FOREIGN KEY (customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE CASCADE,
    CONSTRAINT reconciliation_jobs_payload_unique UNIQUE (application_id, customer_id, payload_fingerprint),
    CONSTRAINT reconciliation_jobs_payload_valid CHECK (
        octet_length(payload_ciphertext) > 0 AND octet_length(payload_fingerprint) = 32
    ),
    CONSTRAINT reconciliation_jobs_key_valid CHECK (
        encryption_key_id <> '' AND encryption_key_id = btrim(encryption_key_id)
    ),
    CONSTRAINT reconciliation_jobs_river_job_valid CHECK (river_job_id IS NULL OR river_job_id > 0),
    CONSTRAINT reconciliation_jobs_outcome_valid CHECK (
        NOT (processed_at IS NOT NULL AND failed_at IS NOT NULL)
        AND (last_error_code IS NULL OR failed_at IS NOT NULL)
    )
);

CREATE INDEX api_keys_application_idx ON api_keys (application_id) WHERE revoked_at IS NULL;
-- Not partial: the cascading project foreign key deletes by project_id across revoked keys too.
CREATE INDEX api_keys_project_idx ON api_keys (project_id);
CREATE INDEX reconciliation_jobs_customer_idx
    ON reconciliation_jobs (customer_id, project_id);

-- Queue retention and worker claim indexes.
CREATE INDEX inbox_messages_processed_retention_idx
    ON inbox_messages (processed_at) WHERE processed_at IS NOT NULL;
CREATE INDEX inbox_messages_failed_retention_idx
    ON inbox_messages (failed_at) WHERE failed_at IS NOT NULL;
CREATE INDEX reconciliation_jobs_processed_retention_idx
    ON reconciliation_jobs (processed_at) WHERE processed_at IS NOT NULL;
CREATE INDEX reconciliation_jobs_failed_retention_idx
    ON reconciliation_jobs (failed_at) WHERE failed_at IS NOT NULL;
CREATE INDEX outbox_events_delivered_retention_idx
    ON outbox_events (delivered_at) WHERE delivered_at IS NOT NULL;
CREATE INDEX outbox_events_failed_retention_idx
    ON outbox_events (failed_at) WHERE failed_at IS NOT NULL;

-- Add dashboard administrator accounts that sign in with a username and password.
-- Only a memory-hard password verifier is stored. Consecutive failed sign-ins are counted per
-- account so the service can lock it for a bounded window. session_generation grows by one with
-- every password change, and a dashboard session is valid only for the generation it was issued in.
CREATE TABLE admin_users (
    id text PRIMARY KEY,
    username text NOT NULL,
    password_salt bytea NOT NULL,
    password_hash bytea NOT NULL,
    password_changed_at timestamptz NOT NULL,
    failed_attempts integer NOT NULL DEFAULT 0,
    locked_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    session_generation bigint NOT NULL DEFAULT 1,
    CONSTRAINT admin_users_id_valid CHECK (id <> '' AND id = btrim(id)),
    CONSTRAINT admin_users_username_unique UNIQUE (username),
    CONSTRAINT admin_users_username_valid CHECK (username ~ '^[a-z0-9][a-z0-9._@-]{2,63}$'),
    CONSTRAINT admin_users_password_valid CHECK (
        octet_length(password_salt) = 16 AND octet_length(password_hash) = 32
    ),
    CONSTRAINT admin_users_failed_attempts_valid CHECK (failed_attempts >= 0),
    CONSTRAINT admin_users_session_generation_valid CHECK (session_generation > 0)
);

-- Customer identity: aliases and merges recorded without rewriting historical purchase rows.
CREATE TABLE customer_aliases (
    project_id text NOT NULL,
    external_id text NOT NULL,
    canonical_customer_id text NOT NULL,
    actor_role text NOT NULL,
    actor_key_id text NOT NULL,
    -- The transport the alias arrived on: api, dashboard, mcp, or the offline import command.
    actor_channel text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, external_id),
    CONSTRAINT customer_aliases_customer_fk FOREIGN KEY (canonical_customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT customer_aliases_actor_role_valid CHECK (actor_role IN ('admin', 'agent', 'application')),
    CONSTRAINT customer_aliases_actor_key_valid CHECK (
        actor_key_id <> '' AND actor_key_id = btrim(actor_key_id)
    ),
    CONSTRAINT customer_aliases_actor_channel_valid CHECK (
        actor_channel IN ('api', 'dashboard', 'mcp', 'import')
    ),
    -- Agent keys authenticate only on the MCP endpoint and application keys only on the HTTP API.
    CONSTRAINT customer_aliases_actor_channel_role_valid CHECK (
        (actor_role <> 'agent' OR actor_channel = 'mcp')
        AND (actor_role <> 'application' OR actor_channel = 'api')
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

-- Customer timeline: identity audit rows read by canonical customer.
CREATE INDEX customer_aliases_canonical_idx
    ON customer_aliases (project_id, canonical_customer_id, created_at DESC);
CREATE INDEX customer_merges_canonical_idx
    ON customer_merges (project_id, canonical_customer_id, created_at DESC);

-- Store official financial-report imports without raw files, order ids, or purchase prices.
CREATE TABLE financial_report_imports (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    provider text NOT NULL,
    report_kind text NOT NULL,
    period_start date NOT NULL,
    period_end date NOT NULL,
    source text NOT NULL,
    received_sha256 bytea NOT NULL,
    byte_size bigint NOT NULL,
    content_type text NOT NULL,
    parser_version text NOT NULL,
    status text NOT NULL,
    reason text NOT NULL,
    supersedes_import_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT financial_report_imports_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT financial_report_imports_supersedes_fk FOREIGN KEY (supersedes_import_id) REFERENCES financial_report_imports (id) ON DELETE RESTRICT,
    CONSTRAINT financial_report_imports_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT financial_report_imports_provider_kind_valid CHECK (
        (provider = 'apple_app_store' AND report_kind = 'apple_financial_consolidated')
        OR (provider = 'google_play' AND report_kind = 'google_play_earnings')
        OR (provider = 'huawei_appgallery' AND report_kind = 'huawei_settlement')
    ),
    CONSTRAINT financial_report_imports_period_valid CHECK (period_start <= period_end),
    CONSTRAINT financial_report_imports_source_valid CHECK (source IN ('upload', 'provider_api')),
    CONSTRAINT financial_report_imports_status_valid CHECK (
        status IN ('accepted', 'unrecognized', 'rejected', 'superseded')
    ),
    CONSTRAINT financial_report_imports_reason_valid CHECK (
        (status = 'accepted' AND reason = '')
        OR (status <> 'accepted' AND reason <> '' AND reason = btrim(reason))
    ),
    CONSTRAINT financial_report_imports_sha_valid CHECK (octet_length(received_sha256) = 32),
    CONSTRAINT financial_report_imports_byte_size_valid CHECK (byte_size > 0 AND byte_size <= 33554432),
    CONSTRAINT financial_report_imports_content_type_valid CHECK (
        content_type <> '' AND content_type = btrim(content_type) AND char_length(content_type) <= 200
    ),
    CONSTRAINT financial_report_imports_parser_version_valid CHECK (
        parser_version <> '' AND parser_version = btrim(parser_version) AND char_length(parser_version) <= 100
    ),
    CONSTRAINT financial_report_imports_supersedes_not_self CHECK (
        supersedes_import_id IS NULL OR supersedes_import_id <> id
    )
);

CREATE UNIQUE INDEX financial_report_imports_digest_unique
    ON financial_report_imports (project_id, provider, report_kind, period_start, period_end, received_sha256);

CREATE UNIQUE INDEX financial_report_imports_one_accepted_period_idx
    ON financial_report_imports (project_id, provider, report_kind, period_start, period_end)
    WHERE status = 'accepted';

CREATE INDEX financial_report_imports_project_idx
    ON financial_report_imports (project_id, created_at DESC, id);

CREATE TABLE financial_report_amounts (
    import_id text NOT NULL,
    currency text NOT NULL,
    amount_decimal numeric NOT NULL,
    amount_scale integer NOT NULL,
    minor_units bigint,
    commission_state text NOT NULL,
    tax_state text NOT NULL,
    covered_application_id text NOT NULL,
    PRIMARY KEY (import_id, covered_application_id, currency),
    CONSTRAINT financial_report_amounts_import_fk FOREIGN KEY (import_id)
        REFERENCES financial_report_imports (id) ON DELETE CASCADE,
    CONSTRAINT financial_report_amounts_application_fk FOREIGN KEY (covered_application_id)
        REFERENCES applications (id) ON DELETE RESTRICT,
    CONSTRAINT financial_report_amounts_currency_valid CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT financial_report_amounts_scale_valid CHECK (amount_scale >= 0 AND amount_scale <= 18),
    CONSTRAINT financial_report_amounts_commission_valid CHECK (commission_state IN ('included', 'unknown')),
    CONSTRAINT financial_report_amounts_tax_valid CHECK (tax_state = 'not_itemized'),
    CONSTRAINT financial_report_amounts_application_valid CHECK (
        covered_application_id <> '' AND covered_application_id = btrim(covered_application_id)
    )
);

CREATE FUNCTION financial_report_amounts_require_accepted() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM financial_report_imports
        WHERE id = NEW.import_id AND status = 'accepted'
    ) THEN
        RAISE EXCEPTION 'financial report amounts require an accepted import'
            USING ERRCODE = '23514', CONSTRAINT = 'financial_report_amounts_accepted_valid';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM financial_report_imports AS import
        JOIN applications AS application ON application.id = NEW.covered_application_id
        WHERE import.id = NEW.import_id
            AND application.project_id = import.project_id
            AND application.environment = 'production'
    ) THEN
        RAISE EXCEPTION 'financial report amount application is outside the import project'
            USING ERRCODE = '23514', CONSTRAINT = 'financial_report_amounts_project_valid';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER financial_report_amounts_require_accepted_trg
    BEFORE INSERT OR UPDATE ON financial_report_amounts
    FOR EACH ROW
    EXECUTE FUNCTION financial_report_amounts_require_accepted();

CREATE FUNCTION financial_report_imports_drop_amounts() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'accepted' AND NEW.status <> 'accepted' THEN
        DELETE FROM financial_report_amounts WHERE import_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER financial_report_imports_drop_amounts_trg
    AFTER UPDATE OF status ON financial_report_imports
    FOR EACH ROW
    EXECUTE FUNCTION financial_report_imports_drop_amounts();

-- Imports: one-way historical grants that are never treated as a store observation.
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

-- Keep a failed subscription schedule until a worker inserts its reconciliation job.
-- attempts and next_attempt_at keep a poison row out of the due batch.
-- A scheduled or dead row is terminal and no longer occupies the pending payload identity.
CREATE TABLE reconciliation_schedule_retries (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    application_id text NOT NULL,
    customer_id text NOT NULL,
    payload_ciphertext bytea NOT NULL,
    payload_fingerprint bytea NOT NULL,
    encryption_key_id text NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    scheduled_at timestamptz,
    dead_at timestamptz,
    CONSTRAINT reconciliation_schedule_retries_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT reconciliation_schedule_retries_customer_fk FOREIGN KEY (customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE CASCADE,
    CONSTRAINT reconciliation_schedule_retries_id_valid CHECK (
        id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT reconciliation_schedule_retries_payload_valid CHECK (
        octet_length(payload_ciphertext) > 0 AND octet_length(payload_fingerprint) = 32
    ),
    CONSTRAINT reconciliation_schedule_retries_key_valid CHECK (
        encryption_key_id <> '' AND encryption_key_id = btrim(encryption_key_id)
    ),
    CONSTRAINT reconciliation_schedule_retries_attempts_valid CHECK (attempts >= 0),
    CONSTRAINT reconciliation_schedule_retries_outcome_exclusive CHECK (
        scheduled_at IS NULL OR dead_at IS NULL
    )
);

-- Pending rows only. A scheduled or dead retry must not block a later retry for the same payload.
CREATE UNIQUE INDEX reconciliation_schedule_retries_payload_unique
    ON reconciliation_schedule_retries (application_id, customer_id, payload_fingerprint)
    WHERE scheduled_at IS NULL AND dead_at IS NULL;

CREATE INDEX reconciliation_schedule_retries_pending_idx
    ON reconciliation_schedule_retries (next_attempt_at, id)
    WHERE scheduled_at IS NULL AND dead_at IS NULL;

CREATE INDEX reconciliation_schedule_retries_scheduled_retention_idx
    ON reconciliation_schedule_retries (scheduled_at, id)
    WHERE scheduled_at IS NOT NULL;

CREATE INDEX reconciliation_schedule_retries_dead_retention_idx
    ON reconciliation_schedule_retries (dead_at, id)
    WHERE dead_at IS NOT NULL;

-- Manual grants: operator-issued access recorded as an audited projection source that is never store evidence.
CREATE TABLE manual_grants (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    customer_id text NOT NULL,
    application_id text NOT NULL,
    product_id text NOT NULL,
    entitlement_id text NOT NULL,
    reason text NOT NULL,
    actor_key_id text NOT NULL,
    -- The transport the grant and its revocation arrived on: api, dashboard, or mcp.
    actor_channel text NOT NULL,
    idempotency_key text NOT NULL,
    effective_starts_at timestamptz NOT NULL,
    effective_ends_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoked_by_key_id text,
    revoked_by_channel text,
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
    CONSTRAINT manual_grants_actor_channel_valid CHECK (
        actor_channel IN ('api', 'dashboard', 'mcp', 'import')
    ),
    CONSTRAINT manual_grants_idempotency_key_valid CHECK (
        idempotency_key <> ''
        AND idempotency_key = btrim(idempotency_key)
        AND char_length(idempotency_key) <= 200
        AND idempotency_key !~ '[[:cntrl:]]'
    ),
    CONSTRAINT manual_grants_period_valid CHECK (effective_ends_at > effective_starts_at),
    CONSTRAINT manual_grants_revocation_valid CHECK (
        (
            revoked_at IS NULL AND revoked_by_key_id IS NULL AND revoked_by_channel IS NULL
            AND revocation_reason IS NULL
        )
        OR (
            revoked_at IS NOT NULL
            AND revoked_by_key_id IS NOT NULL
            AND revoked_by_channel IS NOT NULL
            AND revoked_by_channel IN ('api', 'dashboard', 'mcp', 'import')
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

-- Entitlement projection: current customer access together with the store observation, import, or manual grant it came from.
CREATE TABLE customer_entitlements (
    project_id text NOT NULL,
    customer_id text NOT NULL,
    entitlement_id text NOT NULL,
    source_observation_id text,
    source_product_id text NOT NULL,
    access_status text NOT NULL,
    access_reason text NOT NULL,
    effective_starts_at timestamptz,
    effective_ends_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- The customer that owns the source row. It differs from customer_id after a customer merge.
    source_customer_id text NOT NULL,
    source_kind text NOT NULL DEFAULT 'store_observation',
    source_import_id text,
    source_manual_grant_id text,
    PRIMARY KEY (customer_id, entitlement_id),
    CONSTRAINT customer_entitlements_customer_fk FOREIGN KEY (customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE CASCADE,
    CONSTRAINT customer_entitlements_entitlement_fk FOREIGN KEY (entitlement_id, project_id)
        REFERENCES entitlements (id, project_id) ON DELETE CASCADE,
    CONSTRAINT customer_entitlements_source_fk FOREIGN KEY (
        source_observation_id,
        source_customer_id,
        project_id,
        source_product_id
    ) REFERENCES purchase_observations (id, customer_id, project_id, product_id) ON DELETE RESTRICT,
    CONSTRAINT customer_entitlements_mapping_fk FOREIGN KEY (source_product_id, entitlement_id)
        REFERENCES product_entitlements (product_id, entitlement_id) ON DELETE RESTRICT,
    CONSTRAINT customer_entitlements_access_valid CHECK (access_status IN ('allowed', 'denied', 'unresolved')),
    CONSTRAINT customer_entitlements_reason_valid CHECK (
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
            'unresolved',
            'manual_grant'
        )
    ),
    -- An allowed manual projection reports manual_grant, and nothing else does.
    CONSTRAINT customer_entitlements_manual_reason_valid CHECK (
        (access_reason = 'manual_grant') = (source_kind = 'manual' AND access_status = 'allowed')
    ),
    CONSTRAINT customer_entitlements_period_valid CHECK (
        (effective_starts_at IS NULL AND effective_ends_at IS NULL)
        OR (effective_starts_at IS NOT NULL AND (effective_ends_at IS NULL OR effective_ends_at > effective_starts_at))
    ),
    CONSTRAINT customer_entitlements_allowed_valid CHECK (
        access_status <> 'allowed' OR effective_starts_at IS NOT NULL
    ),
    CONSTRAINT customer_entitlements_version_valid CHECK (version > 0),
    CONSTRAINT customer_entitlements_source_customer_valid CHECK (
        source_customer_id <> ''
        AND source_customer_id = btrim(source_customer_id)
        AND source_customer_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT customer_entitlements_source_kind_valid CHECK (
        source_kind IN ('store_observation', 'import', 'manual')
    ),
    CONSTRAINT customer_entitlements_source_exclusive CHECK (
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
    CONSTRAINT customer_entitlements_source_import_fk FOREIGN KEY (source_import_id)
        REFERENCES import_grants (id) ON DELETE RESTRICT,
    CONSTRAINT customer_entitlements_source_manual_fk FOREIGN KEY (source_manual_grant_id)
        REFERENCES manual_grants (id) ON DELETE RESTRICT
);

CREATE INDEX customer_entitlements_project_idx ON customer_entitlements (project_id, entitlement_id);
CREATE INDEX customer_entitlements_source_idx ON customer_entitlements (source_observation_id);
CREATE INDEX customer_entitlements_mapping_idx
    ON customer_entitlements (source_product_id, entitlement_id);
CREATE INDEX customer_entitlements_source_manual_idx
    ON customer_entitlements (source_manual_grant_id)
    WHERE source_manual_grant_id IS NOT NULL;

-- Answer App Store CONSUMPTION_REQUEST notifications for applications whose operator opted in.
-- Settings are off unless a row says enabled. Consent is opt-in only: one recorded decision per
-- application and customer, written by the host backend with the application bearer.
-- Requests are durable jobs. Every handler decision is one attempt row.
CREATE TABLE apple_consumption_settings (
    project_id text NOT NULL,
    application_id text PRIMARY KEY,
    enabled boolean NOT NULL,
    refund_preference text NOT NULL,
    sample_content_provided boolean NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    updated_by_key_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT apple_consumption_settings_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE CASCADE,
    CONSTRAINT apple_consumption_settings_refund_preference_valid CHECK (
        refund_preference IN ('none', 'decline', 'grant_full', 'grant_prorated')
    ),
    CONSTRAINT apple_consumption_settings_revision_valid CHECK (revision > 0),
    CONSTRAINT apple_consumption_settings_actor_valid CHECK (
        updated_by_key_id IS NULL OR (
            updated_by_key_id <> '' AND updated_by_key_id = btrim(updated_by_key_id)
            AND updated_by_key_id !~ '[[:cntrl:]]'
        )
    ),
    CONSTRAINT apple_consumption_settings_timestamps_valid CHECK (updated_at >= created_at)
);

CREATE TABLE apple_consumption_consents (
    project_id text NOT NULL,
    application_id text NOT NULL,
    customer_id text NOT NULL,
    consented boolean NOT NULL,
    actor_key_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, customer_id),
    CONSTRAINT apple_consumption_consents_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE CASCADE,
    CONSTRAINT apple_consumption_consents_customer_fk FOREIGN KEY (customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE CASCADE,
    CONSTRAINT apple_consumption_consents_actor_valid CHECK (
        actor_key_id <> '' AND actor_key_id = btrim(actor_key_id) AND actor_key_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT apple_consumption_consents_timestamps_valid CHECK (updated_at >= created_at)
);

-- The id is derived from a keyed fingerprint of Apple's notificationUUID, so a redelivery is one request.
-- The transaction id and app account token stay inside the protected payload.
CREATE TABLE apple_consumption_requests (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    application_id text NOT NULL,
    customer_id text,
    provider_product_id text NOT NULL,
    product_type text NOT NULL,
    customer_reason text,
    payload_ciphertext bytea NOT NULL,
    payload_fingerprint bytea NOT NULL,
    encryption_key_id text NOT NULL,
    signed_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    deadline_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    status_reason text,
    attempt_count integer NOT NULL DEFAULT 0,
    completed_at timestamptz,
    river_job_id bigint UNIQUE,
    processed_at timestamptz,
    failed_at timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT apple_consumption_requests_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT apple_consumption_requests_customer_fk FOREIGN KEY (customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE CASCADE,
    CONSTRAINT apple_consumption_requests_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT apple_consumption_requests_product_valid CHECK (
        provider_product_id <> '' AND provider_product_id = btrim(provider_product_id)
    ),
    CONSTRAINT apple_consumption_requests_product_type_valid CHECK (
        product_type IN ('consumable', 'non_consumable', 'auto_renewable_subscription', 'non_renewing_subscription')
    ),
    CONSTRAINT apple_consumption_requests_customer_reason_valid CHECK (
        customer_reason IS NULL OR customer_reason IN (
            'unintended_purchase', 'fulfillment_issue', 'unsatisfied_with_purchase', 'legal', 'other'
        )
    ),
    CONSTRAINT apple_consumption_requests_payload_valid CHECK (
        octet_length(payload_ciphertext) > 0 AND octet_length(payload_fingerprint) = 32
    ),
    CONSTRAINT apple_consumption_requests_key_valid CHECK (
        encryption_key_id <> '' AND encryption_key_id = btrim(encryption_key_id)
    ),
    CONSTRAINT apple_consumption_requests_schedule_valid CHECK (
        deadline_at > signed_at AND available_at >= received_at
    ),
    CONSTRAINT apple_consumption_requests_status_valid CHECK (
        status IN ('pending', 'sent', 'skipped', 'expired', 'failed')
    ),
    CONSTRAINT apple_consumption_requests_status_reason_valid CHECK (
        status_reason IS NULL OR status_reason ~ '^[a-z0-9_]{1,64}$'
    ),
    CONSTRAINT apple_consumption_requests_terminal_valid CHECK (
        (status = 'pending') = (completed_at IS NULL)
        AND (status = 'pending' OR status_reason IS NOT NULL)
    ),
    CONSTRAINT apple_consumption_requests_attempts_valid CHECK (attempt_count >= 0),
    CONSTRAINT apple_consumption_requests_river_job_valid CHECK (river_job_id IS NULL OR river_job_id > 0),
    CONSTRAINT apple_consumption_requests_outcome_valid CHECK (
        NOT (processed_at IS NOT NULL AND failed_at IS NOT NULL)
        AND (last_error_code IS NULL OR failed_at IS NOT NULL)
        AND (processed_at IS NULL OR status <> 'pending')
        AND (failed_at IS NULL OR status = 'failed')
    )
);

CREATE INDEX apple_consumption_requests_application_idx
    ON apple_consumption_requests (application_id, received_at DESC, id DESC);

-- Bound retention cleanup scans to closed requests, like migration 008 does for the other queues.
CREATE INDEX apple_consumption_requests_processed_retention_idx
    ON apple_consumption_requests (processed_at) WHERE processed_at IS NOT NULL;
CREATE INDEX apple_consumption_requests_failed_retention_idx
    ON apple_consumption_requests (failed_at) WHERE failed_at IS NOT NULL;

-- Every handler run that reaches a decision writes one row, including skips and expiry.
-- request_body is the exact JSON sent to Apple. It never contains an identifier.
CREATE TABLE apple_consumption_attempts (
    request_id text NOT NULL REFERENCES apple_consumption_requests (id) ON DELETE CASCADE,
    attempt integer NOT NULL,
    attempted_at timestamptz NOT NULL,
    outcome text NOT NULL,
    reason text NOT NULL,
    http_status integer,
    apple_error_code bigint,
    request_body jsonb,
    PRIMARY KEY (request_id, attempt),
    CONSTRAINT apple_consumption_attempts_attempt_valid CHECK (attempt > 0),
    CONSTRAINT apple_consumption_attempts_outcome_valid CHECK (
        outcome IN ('sent', 'skipped', 'expired', 'transient_error', 'permanent_error')
    ),
    CONSTRAINT apple_consumption_attempts_reason_valid CHECK (reason ~ '^[a-z0-9_]{1,64}$'),
    CONSTRAINT apple_consumption_attempts_http_status_valid CHECK (
        http_status IS NULL OR http_status BETWEEN 100 AND 599
    ),
    CONSTRAINT apple_consumption_attempts_body_valid CHECK (
        request_body IS NULL OR jsonb_typeof(request_body) = 'object'
    )
);

---- create above / drop below ----

-- Removing the baseline removes every IAPStack table and the data in it.
DROP TABLE IF EXISTS
    admin_users,
    api_keys,
    apple_consumption_attempts,
    apple_consumption_consents,
    apple_consumption_requests,
    apple_consumption_settings,
    application_credentials,
    applications,
    customer_aliases,
    customer_entitlements,
    customer_merges,
    customers,
    entitlements,
    financial_report_amounts,
    financial_report_imports,
    import_batches,
    import_grants,
    inbox_messages,
    manual_grants,
    observation_references,
    outbox_events,
    product_entitlements,
    products,
    projects,
    provider_references,
    purchase_evidence,
    purchase_observations,
    reconciliation_jobs,
    reconciliation_schedule_retries,
    store_products,
    verified_artifacts,
    webhook_endpoints
CASCADE;

DROP FUNCTION IF EXISTS financial_report_imports_drop_amounts();
DROP FUNCTION IF EXISTS financial_report_amounts_require_accepted();
