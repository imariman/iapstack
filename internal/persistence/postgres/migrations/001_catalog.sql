-- Create provider-neutral ownership and catalog records.
CREATE TABLE projects (
    id text PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT projects_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]')
);

CREATE TABLE applications (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    provider text NOT NULL,
    environment text NOT NULL,
    provider_application_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT applications_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT applications_id_project_unique UNIQUE (id, project_id),
    CONSTRAINT applications_store_scope_unique UNIQUE (provider, environment, provider_application_id),
    CONSTRAINT applications_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT applications_provider_valid CHECK (provider <> '' AND provider = btrim(provider)),
    CONSTRAINT applications_environment_valid CHECK (environment IN ('production', 'sandbox', 'test')),
    CONSTRAINT applications_provider_id_valid CHECK (
        provider_application_id <> '' AND provider_application_id = btrim(provider_application_id)
    )
);

CREATE TABLE customers (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    external_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT customers_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT customers_id_project_unique UNIQUE (id, project_id),
    CONSTRAINT customers_external_id_unique UNIQUE (project_id, external_id),
    CONSTRAINT customers_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT customers_external_id_valid CHECK (external_id <> '' AND external_id = btrim(external_id))
);

CREATE TABLE products (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    kind text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT products_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT products_id_project_unique UNIQUE (id, project_id),
    CONSTRAINT products_id_project_kind_unique UNIQUE (id, project_id, kind),
    CONSTRAINT products_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT products_kind_valid CHECK (kind <> '' AND kind = btrim(kind))
);

CREATE TABLE entitlements (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT entitlements_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT entitlements_id_project_unique UNIQUE (id, project_id),
    CONSTRAINT entitlements_key_unique UNIQUE (project_id, key),
    CONSTRAINT entitlements_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT entitlements_key_valid CHECK (key <> '' AND key = btrim(key))
);

CREATE TABLE product_entitlements (
    project_id text NOT NULL,
    product_id text NOT NULL,
    entitlement_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (product_id, entitlement_id),
    CONSTRAINT product_entitlements_product_fk FOREIGN KEY (product_id, project_id)
        REFERENCES products (id, project_id) ON DELETE CASCADE,
    CONSTRAINT product_entitlements_entitlement_fk FOREIGN KEY (entitlement_id, project_id)
        REFERENCES entitlements (id, project_id) ON DELETE CASCADE
);

CREATE TABLE store_products (
    project_id text NOT NULL,
    application_id text NOT NULL,
    product_id text NOT NULL,
    provider_product_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (application_id, provider_product_id),
    CONSTRAINT store_products_scope_unique UNIQUE (
        application_id,
        provider_product_id,
        product_id,
        project_id
    ),
    CONSTRAINT store_products_application_fk FOREIGN KEY (application_id, project_id)
        REFERENCES applications (id, project_id) ON DELETE CASCADE,
    CONSTRAINT store_products_product_fk FOREIGN KEY (product_id, project_id)
        REFERENCES products (id, project_id) ON DELETE CASCADE,
    CONSTRAINT store_products_provider_id_valid CHECK (
        provider_product_id <> '' AND provider_product_id = btrim(provider_product_id)
    )
);

CREATE INDEX applications_project_idx ON applications (project_id);
CREATE INDEX customers_project_idx ON customers (project_id);
CREATE INDEX products_project_idx ON products (project_id);
CREATE INDEX entitlements_project_idx ON entitlements (project_id);
CREATE INDEX product_entitlements_entitlement_idx ON product_entitlements (entitlement_id);
CREATE INDEX store_products_product_idx ON store_products (product_id);

---- create above / drop below ----

DROP TABLE store_products;
DROP TABLE product_entitlements;
DROP TABLE entitlements;
DROP TABLE products;
DROP TABLE customers;
DROP TABLE applications;
DROP TABLE projects;
