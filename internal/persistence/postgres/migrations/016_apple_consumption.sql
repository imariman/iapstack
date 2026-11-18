-- Answer App Store CONSUMPTION_REQUEST notifications for applications whose operator opted in.
-- Settings are off unless a row says enabled. Consent is per application and customer.
-- Requests are durable jobs. Every handler decision is one attempt row.
CREATE TABLE apple_consumption_settings (
    project_id text NOT NULL,
    application_id text PRIMARY KEY,
    enabled boolean NOT NULL,
    consent_mode text NOT NULL,
    refund_preference text NOT NULL,
    sample_content_provided boolean NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    updated_by_key_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT apple_consumption_settings_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE CASCADE,
    CONSTRAINT apple_consumption_settings_consent_mode_valid CHECK (
        consent_mode IN ('customer_opt_in', 'application_attested')
    ),
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
    ON apple_consumption_requests (application_id, received_at DESC, id);

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

-- A pending request still has a River job and a protected payload that would be orphaned.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM apple_consumption_requests
        WHERE status = 'pending'
    ) THEN
        RAISE EXCEPTION 'cannot reverse Apple consumption migration while consumption requests are pending';
    END IF;
END $$;

DROP TABLE apple_consumption_attempts;
DROP TABLE apple_consumption_requests;
DROP TABLE apple_consumption_consents;
DROP TABLE apple_consumption_settings;
