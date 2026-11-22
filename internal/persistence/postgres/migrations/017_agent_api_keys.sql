-- Add the project-scoped agent API key role for the MCP endpoint.
-- An agent key names a project and no application. It is read-only unless allow_writes is set,
-- and only an agent key may set allow_writes. Every scoped key now also references its project,
-- so an agent key cannot point at a missing project or outlive a deleted one.
ALTER TABLE api_keys ADD COLUMN allow_writes boolean NOT NULL DEFAULT false;

ALTER TABLE api_keys DROP CONSTRAINT api_keys_role_valid;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_role_valid CHECK (role IN ('admin', 'application', 'agent'));

ALTER TABLE api_keys DROP CONSTRAINT api_keys_scope_valid;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scope_valid CHECK (
    (role = 'admin' AND project_id IS NULL AND application_id IS NULL)
    OR (role = 'application' AND project_id IS NOT NULL AND application_id IS NOT NULL)
    OR (role = 'agent' AND project_id IS NOT NULL AND application_id IS NULL)
);

ALTER TABLE api_keys ADD CONSTRAINT api_keys_allow_writes_valid CHECK (NOT allow_writes OR role = 'agent');

ALTER TABLE api_keys ADD CONSTRAINT api_keys_project_fk FOREIGN KEY (project_id)
    REFERENCES projects (id) ON DELETE CASCADE;

-- Not partial: the cascading foreign key above deletes by project_id across revoked keys too.
CREATE INDEX api_keys_project_idx ON api_keys (project_id);

---- create above / drop below ----

-- The previous schema cannot represent agent keys, so rolling back deletes them.
DELETE FROM api_keys WHERE role = 'agent';

DROP INDEX api_keys_project_idx;
ALTER TABLE api_keys DROP CONSTRAINT api_keys_project_fk;
ALTER TABLE api_keys DROP CONSTRAINT api_keys_allow_writes_valid;

ALTER TABLE api_keys DROP CONSTRAINT api_keys_scope_valid;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scope_valid CHECK (
    (role = 'admin' AND project_id IS NULL AND application_id IS NULL)
    OR (role = 'application' AND project_id IS NOT NULL AND application_id IS NOT NULL)
);

ALTER TABLE api_keys DROP CONSTRAINT api_keys_role_valid;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_role_valid CHECK (role IN ('admin', 'application'));

ALTER TABLE api_keys DROP COLUMN allow_writes;
