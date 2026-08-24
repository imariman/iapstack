-- Add durable, idempotent audit records for provider inputs and application webhooks.
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

---- create above / drop below ----

DROP TABLE outbox_events;
DROP TABLE inbox_messages;
