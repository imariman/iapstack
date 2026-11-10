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

---- create above / drop below ----

-- Unscheduled rows, including dead ones, still hold protected verification payloads and never became a job.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM reconciliation_schedule_retries
        WHERE scheduled_at IS NULL
    ) THEN
        RAISE EXCEPTION 'cannot reverse reconciliation schedule retries migration while pending retries exist';
    END IF;
END $$;

DROP TABLE reconciliation_schedule_retries;
