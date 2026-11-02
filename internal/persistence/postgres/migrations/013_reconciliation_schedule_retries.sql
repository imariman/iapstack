-- Keep a failed subscription schedule until a worker inserts its reconciliation job.
CREATE TABLE reconciliation_schedule_retries (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    application_id text NOT NULL,
    customer_id text NOT NULL,
    kind text NOT NULL,
    payload_ciphertext bytea NOT NULL,
    payload_fingerprint bytea NOT NULL,
    encryption_key_id text NOT NULL,
    metadata jsonb NOT NULL,
    available_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    scheduled_at timestamptz,
    CONSTRAINT reconciliation_schedule_retries_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE RESTRICT,
    CONSTRAINT reconciliation_schedule_retries_customer_fk FOREIGN KEY (customer_id, project_id)
        REFERENCES customers (id, project_id) ON DELETE CASCADE,
    CONSTRAINT reconciliation_schedule_retries_payload_unique UNIQUE (
        application_id, customer_id, kind, payload_fingerprint
    ),
    CONSTRAINT reconciliation_schedule_retries_id_valid CHECK (
        id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT reconciliation_schedule_retries_kind_valid CHECK (
        kind IN ('reconciliation_request', 'purchase_evidence')
    ),
    CONSTRAINT reconciliation_schedule_retries_payload_valid CHECK (
        octet_length(payload_ciphertext) > 0 AND octet_length(payload_fingerprint) = 32
    ),
    CONSTRAINT reconciliation_schedule_retries_key_valid CHECK (
        encryption_key_id <> '' AND encryption_key_id = btrim(encryption_key_id)
    ),
    CONSTRAINT reconciliation_schedule_retries_metadata_valid CHECK (jsonb_typeof(metadata) = 'object')
);

CREATE INDEX reconciliation_schedule_retries_pending_idx
    ON reconciliation_schedule_retries (created_at, id)
    WHERE scheduled_at IS NULL;

---- create above / drop below ----

DROP TABLE reconciliation_schedule_retries;
