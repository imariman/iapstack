-- Add indexes for foreign-key checks.
CREATE INDEX purchase_observations_evidence_scope_idx
    ON purchase_observations (evidence_id, application_id, project_id, customer_id);
CREATE INDEX purchase_observations_internal_product_idx
    ON purchase_observations (product_id, project_id, product_kind);
CREATE INDEX customer_entitlements_mapping_idx
    ON customer_entitlements (source_product_id, entitlement_id);
CREATE INDEX reconciliation_jobs_customer_idx
    ON reconciliation_jobs (customer_id, project_id);

DROP INDEX provider_references_lookup_idx;
DROP INDEX customers_project_idx;
DROP INDEX entitlements_project_idx;

---- create above / drop below ----

CREATE INDEX provider_references_lookup_idx
    ON provider_references (application_id, role, kind, value_fingerprint);
CREATE INDEX customers_project_idx ON customers (project_id);
CREATE INDEX entitlements_project_idx ON entitlements (project_id);

DROP INDEX reconciliation_jobs_customer_idx;
DROP INDEX customer_entitlements_mapping_idx;
DROP INDEX purchase_observations_internal_product_idx;
DROP INDEX purchase_observations_evidence_scope_idx;
