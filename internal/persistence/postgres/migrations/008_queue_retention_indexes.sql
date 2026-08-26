-- Bound retention cleanup scans to terminal queue records.
CREATE INDEX inbox_messages_processed_retention_idx
    ON inbox_messages (processed_at) WHERE processed_at IS NOT NULL;
CREATE INDEX inbox_messages_failed_retention_idx
    ON inbox_messages (failed_at) WHERE failed_at IS NOT NULL;
CREATE INDEX reconciliation_jobs_processed_retention_idx
    ON reconciliation_jobs (processed_at) WHERE processed_at IS NOT NULL;
CREATE INDEX reconciliation_jobs_failed_retention_idx
    ON reconciliation_jobs (failed_at) WHERE failed_at IS NOT NULL;
CREATE INDEX outbox_events_delivered_retention_idx
    ON outbox_events (delivered_at) WHERE delivered_at IS NOT NULL;
CREATE INDEX outbox_events_failed_retention_idx
    ON outbox_events (failed_at) WHERE failed_at IS NOT NULL;

---- create above / drop below ----

DROP INDEX outbox_events_failed_retention_idx;
DROP INDEX outbox_events_delivered_retention_idx;
DROP INDEX reconciliation_jobs_failed_retention_idx;
DROP INDEX reconciliation_jobs_processed_retention_idx;
DROP INDEX inbox_messages_failed_retention_idx;
DROP INDEX inbox_messages_processed_retention_idx;
