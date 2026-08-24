-- Add authenticated API identities, protected webhook configuration, and reconciliation audit records.
CREATE TABLE api_keys (
    id text PRIMARY KEY,
    role text NOT NULL,
    project_id text,
    application_id text,
    secret_salt bytea NOT NULL,
    secret_hash bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    CONSTRAINT api_keys_role_valid CHECK (role IN ('admin', 'application')),
    CONSTRAINT api_keys_secret_valid CHECK (octet_length(secret_salt) = 16 AND octet_length(secret_hash) = 32),
    CONSTRAINT api_keys_scope_valid CHECK (
        (role = 'admin' AND project_id IS NULL AND application_id IS NULL)
        OR (role = 'application' AND project_id IS NOT NULL AND application_id IS NOT NULL)
    ),
    CONSTRAINT api_keys_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE CASCADE
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

---- create above / drop below ----

DROP TABLE reconciliation_jobs;
DROP TABLE webhook_endpoints;
DROP TABLE api_keys;
