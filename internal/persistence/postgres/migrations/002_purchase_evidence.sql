-- Store encrypted evidence and immutable provider-neutral purchase observations.
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
CREATE INDEX provider_references_lookup_idx ON provider_references (application_id, role, kind, value_fingerprint);
CREATE INDEX observation_references_reference_idx ON observation_references (reference_id);

---- create above / drop below ----

DROP TABLE observation_references;
DROP TABLE provider_references;
DROP TABLE purchase_observations;
DROP TABLE verified_artifacts;
DROP TABLE purchase_evidence;
