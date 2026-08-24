-- Add durable, idempotent queues for provider inputs and application webhooks.
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
    state text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    received_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    locked_at timestamptz,
    locked_by text,
    processed_at timestamptz,
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
    CONSTRAINT inbox_messages_state_valid CHECK (state IN ('pending', 'processing', 'processed', 'failed')),
    CONSTRAINT inbox_messages_attempts_valid CHECK (attempts >= 0),
    CONSTRAINT inbox_messages_schedule_valid CHECK (available_at >= received_at),
    CONSTRAINT inbox_messages_lock_pair_valid CHECK ((locked_at IS NULL) = (locked_by IS NULL)),
    CONSTRAINT inbox_messages_state_fields_valid CHECK (
        (state = 'pending' AND locked_at IS NULL AND processed_at IS NULL)
        OR (state = 'processing' AND locked_at IS NOT NULL AND processed_at IS NULL)
        OR (state = 'processed' AND locked_at IS NULL AND processed_at IS NOT NULL)
        OR (state = 'failed' AND locked_at IS NULL AND processed_at IS NULL)
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
    state text NOT NULL DEFAULT 'pending',
    attempts integer NOT NULL DEFAULT 0,
    occurred_at timestamptz NOT NULL,
    available_at timestamptz NOT NULL,
    locked_at timestamptz,
    locked_by text,
    delivered_at timestamptz,
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
    CONSTRAINT outbox_events_state_valid CHECK (state IN ('pending', 'processing', 'delivered', 'failed')),
    CONSTRAINT outbox_events_attempts_valid CHECK (attempts >= 0),
    CONSTRAINT outbox_events_schedule_valid CHECK (available_at >= occurred_at),
    CONSTRAINT outbox_events_lock_pair_valid CHECK ((locked_at IS NULL) = (locked_by IS NULL)),
    CONSTRAINT outbox_events_state_fields_valid CHECK (
        (state = 'pending' AND locked_at IS NULL AND delivered_at IS NULL)
        OR (state = 'processing' AND locked_at IS NOT NULL AND delivered_at IS NULL)
        OR (state = 'delivered' AND locked_at IS NULL AND delivered_at IS NOT NULL)
        OR (state = 'failed' AND locked_at IS NULL AND delivered_at IS NULL)
    )
);

CREATE INDEX inbox_messages_claim_idx ON inbox_messages (available_at, received_at)
    WHERE state = 'pending';
CREATE INDEX inbox_messages_lock_idx ON inbox_messages (locked_at)
    WHERE state = 'processing';
CREATE INDEX outbox_events_claim_idx ON outbox_events (available_at, occurred_at)
    WHERE state = 'pending';
CREATE INDEX outbox_events_lock_idx ON outbox_events (locked_at)
    WHERE state = 'processing';

---- create above / drop below ----

DROP TABLE outbox_events;
DROP TABLE inbox_messages;
