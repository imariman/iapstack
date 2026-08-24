-- Store provider-owned credential packages as protected application-scoped values.
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

---- create above / drop below ----

DROP TABLE application_credentials;
