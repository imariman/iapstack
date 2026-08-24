-- Materialize current customer access while retaining its verified source.
CREATE TABLE customer_entitlements (
    project_id text NOT NULL,
    customer_id text NOT NULL,
    entitlement_id text NOT NULL,
    source_observation_id text NOT NULL,
    source_product_id text NOT NULL,
    access_status text NOT NULL,
    access_reason text NOT NULL,
    effective_starts_at timestamptz,
    effective_ends_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, entitlement_id),
    CONSTRAINT customer_entitlements_customer_fk FOREIGN KEY (customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE CASCADE,
    CONSTRAINT customer_entitlements_entitlement_fk FOREIGN KEY (entitlement_id, project_id)
        REFERENCES entitlements (id, project_id) ON DELETE CASCADE,
    CONSTRAINT customer_entitlements_source_fk FOREIGN KEY (
        source_observation_id,
        customer_id,
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
            'unresolved'
        )
    ),
    CONSTRAINT customer_entitlements_period_valid CHECK (
        (effective_starts_at IS NULL AND effective_ends_at IS NULL)
        OR (effective_starts_at IS NOT NULL AND (effective_ends_at IS NULL OR effective_ends_at > effective_starts_at))
    ),
    CONSTRAINT customer_entitlements_allowed_valid CHECK (
        access_status <> 'allowed' OR effective_starts_at IS NOT NULL
    ),
    CONSTRAINT customer_entitlements_version_valid CHECK (version > 0)
);

CREATE INDEX customer_entitlements_project_idx ON customer_entitlements (project_id, entitlement_id);
CREATE INDEX customer_entitlements_source_idx ON customer_entitlements (source_observation_id);

---- create above / drop below ----

DROP TABLE customer_entitlements;
