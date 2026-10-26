-- Store official financial-report imports without raw files, order ids, or purchase prices.
CREATE TABLE financial_report_imports (
    id text PRIMARY KEY,
    project_id text NOT NULL,
    provider text NOT NULL,
    report_kind text NOT NULL,
    period_start date NOT NULL,
    period_end date NOT NULL,
    source text NOT NULL,
    received_sha256 bytea NOT NULL,
    byte_size bigint NOT NULL,
    content_type text NOT NULL,
    parser_version text NOT NULL,
    status text NOT NULL,
    reason text NOT NULL,
    supersedes_import_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT financial_report_imports_project_fk FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE RESTRICT,
    CONSTRAINT financial_report_imports_supersedes_fk FOREIGN KEY (supersedes_import_id) REFERENCES financial_report_imports (id) ON DELETE RESTRICT,
    CONSTRAINT financial_report_imports_id_valid CHECK (id <> '' AND id = btrim(id) AND id !~ '[[:cntrl:]]'),
    CONSTRAINT financial_report_imports_provider_kind_valid CHECK (
        (provider = 'apple_app_store' AND report_kind = 'apple_financial_consolidated')
        OR (provider = 'google_play' AND report_kind = 'google_play_earnings')
        OR (provider = 'huawei_appgallery' AND report_kind = 'huawei_settlement')
    ),
    CONSTRAINT financial_report_imports_period_valid CHECK (period_start <= period_end),
    CONSTRAINT financial_report_imports_source_valid CHECK (source IN ('upload', 'provider_api')),
    CONSTRAINT financial_report_imports_status_valid CHECK (
        status IN ('accepted', 'unrecognized', 'rejected', 'superseded')
    ),
    CONSTRAINT financial_report_imports_reason_valid CHECK (
        (status = 'accepted' AND reason = '')
        OR (status <> 'accepted' AND reason <> '' AND reason = btrim(reason))
    ),
    CONSTRAINT financial_report_imports_sha_valid CHECK (octet_length(received_sha256) = 32),
    CONSTRAINT financial_report_imports_byte_size_valid CHECK (byte_size > 0 AND byte_size <= 33554432),
    CONSTRAINT financial_report_imports_content_type_valid CHECK (
        content_type <> '' AND content_type = btrim(content_type) AND char_length(content_type) <= 200
    ),
    CONSTRAINT financial_report_imports_parser_version_valid CHECK (
        parser_version <> '' AND parser_version = btrim(parser_version) AND char_length(parser_version) <= 100
    ),
    CONSTRAINT financial_report_imports_supersedes_not_self CHECK (
        supersedes_import_id IS NULL OR supersedes_import_id <> id
    )
);

CREATE UNIQUE INDEX financial_report_imports_digest_unique
    ON financial_report_imports (project_id, provider, report_kind, period_start, period_end, received_sha256);

CREATE UNIQUE INDEX financial_report_imports_one_accepted_period_idx
    ON financial_report_imports (project_id, provider, report_kind, period_start, period_end)
    WHERE status = 'accepted';

CREATE INDEX financial_report_imports_project_idx
    ON financial_report_imports (project_id, created_at DESC, id);

CREATE TABLE financial_report_amounts (
    import_id text NOT NULL,
    currency text NOT NULL,
    amount_decimal numeric NOT NULL,
    amount_scale integer NOT NULL,
    minor_units bigint,
    commission_state text NOT NULL,
    tax_state text NOT NULL,
    covered_application_id text NOT NULL,
    PRIMARY KEY (import_id, covered_application_id, currency),
    CONSTRAINT financial_report_amounts_import_fk FOREIGN KEY (import_id)
        REFERENCES financial_report_imports (id) ON DELETE CASCADE,
    CONSTRAINT financial_report_amounts_application_fk FOREIGN KEY (covered_application_id)
        REFERENCES applications (id) ON DELETE RESTRICT,
    CONSTRAINT financial_report_amounts_currency_valid CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT financial_report_amounts_scale_valid CHECK (amount_scale >= 0 AND amount_scale <= 18),
    CONSTRAINT financial_report_amounts_commission_valid CHECK (commission_state IN ('included', 'unknown')),
    CONSTRAINT financial_report_amounts_tax_valid CHECK (tax_state = 'not_itemized'),
    CONSTRAINT financial_report_amounts_application_valid CHECK (
        covered_application_id <> '' AND covered_application_id = btrim(covered_application_id)
    )
);

CREATE FUNCTION financial_report_amounts_require_accepted() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM financial_report_imports
        WHERE id = NEW.import_id AND status = 'accepted'
    ) THEN
        RAISE EXCEPTION 'financial report amounts require an accepted import'
            USING ERRCODE = '23514', CONSTRAINT = 'financial_report_amounts_accepted_valid';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM financial_report_imports AS import
        JOIN applications AS application ON application.id = NEW.covered_application_id
        WHERE import.id = NEW.import_id
            AND application.project_id = import.project_id
            AND application.environment = 'production'
    ) THEN
        RAISE EXCEPTION 'financial report amount application is outside the import project'
            USING ERRCODE = '23514', CONSTRAINT = 'financial_report_amounts_project_valid';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER financial_report_amounts_require_accepted_trg
    BEFORE INSERT OR UPDATE ON financial_report_amounts
    FOR EACH ROW
    EXECUTE FUNCTION financial_report_amounts_require_accepted();

CREATE FUNCTION financial_report_imports_drop_amounts() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'accepted' AND NEW.status <> 'accepted' THEN
        DELETE FROM financial_report_amounts WHERE import_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER financial_report_imports_drop_amounts_trg
    AFTER UPDATE OF status ON financial_report_imports
    FOR EACH ROW
    EXECUTE FUNCTION financial_report_imports_drop_amounts();

---- create above / drop below ----

DROP TRIGGER IF EXISTS financial_report_imports_drop_amounts_trg ON financial_report_imports;
DROP TRIGGER IF EXISTS financial_report_amounts_require_accepted_trg ON financial_report_amounts;
DROP FUNCTION IF EXISTS financial_report_imports_drop_amounts();
DROP FUNCTION IF EXISTS financial_report_amounts_require_accepted();
DROP TABLE IF EXISTS financial_report_amounts;
DROP TABLE IF EXISTS financial_report_imports;
