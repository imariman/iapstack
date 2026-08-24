-- Add indexes for foreign-key checks and bounded terminal queue retention.
CREATE INDEX purchase_observations_evidence_scope_idx
    ON purchase_observations (evidence_id, application_id, project_id, customer_id);
CREATE INDEX purchase_observations_internal_product_idx
    ON purchase_observations (product_id, project_id, product_kind);
CREATE INDEX customer_entitlements_mapping_idx
    ON customer_entitlements (source_product_id, entitlement_id);
CREATE INDEX reconciliation_jobs_customer_idx
    ON reconciliation_jobs (customer_id, project_id);

CREATE INDEX inbox_messages_retention_idx
    ON inbox_messages (COALESCE(processed_at, updated_at), id)
    WHERE state IN ('processed', 'failed');
CREATE INDEX outbox_events_retention_idx
    ON outbox_events (COALESCE(delivered_at, updated_at), id)
    WHERE state IN ('delivered', 'failed');
CREATE INDEX reconciliation_jobs_retention_idx
    ON reconciliation_jobs (COALESCE(processed_at, updated_at), id)
    WHERE state IN ('processed', 'failed');

DROP INDEX provider_references_lookup_idx;
DROP INDEX customers_project_idx;
DROP INDEX entitlements_project_idx;

---- create above / drop below ----

CREATE INDEX provider_references_lookup_idx
    ON provider_references (application_id, role, kind, value_fingerprint);
CREATE INDEX customers_project_idx ON customers (project_id);
CREATE INDEX entitlements_project_idx ON entitlements (project_id);

DROP INDEX reconciliation_jobs_retention_idx;
DROP INDEX outbox_events_retention_idx;
DROP INDEX inbox_messages_retention_idx;
DROP INDEX reconciliation_jobs_customer_idx;
DROP INDEX customer_entitlements_mapping_idx;
DROP INDEX purchase_observations_internal_product_idx;
DROP INDEX purchase_observations_evidence_scope_idx;
