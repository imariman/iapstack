-- Retain provider-signed subscription evidence for subscription metrics.
-- NULL always means not recorded: rows stored before this migration, providers whose adapter
-- does not map the field, and every non-subscription observation.
--   offer_phase: Apple offerType/offerDiscountType, Google Play line-item offerPhase.
--   transaction_reason: Apple transactionReason (PURCHASE or RENEWAL).
--   lineage_started_at: Apple originalPurchaseDate.
--   cancellation_reason: Google Play canceledStateContext.
ALTER TABLE purchase_observations
    ADD COLUMN offer_phase text,
    ADD COLUMN transaction_reason text,
    ADD COLUMN lineage_started_at timestamptz,
    ADD COLUMN cancellation_reason text,
    ADD CONSTRAINT purchase_observations_offer_phase_valid CHECK (
        offer_phase IS NULL
        OR offer_phase IN ('base', 'free_trial', 'discounted', 'proration', 'unknown')
    ),
    ADD CONSTRAINT purchase_observations_transaction_reason_valid CHECK (
        transaction_reason IS NULL OR transaction_reason IN ('purchase', 'renewal')
    ),
    ADD CONSTRAINT purchase_observations_cancellation_reason_valid CHECK (
        cancellation_reason IS NULL
        OR cancellation_reason IN ('customer', 'system', 'developer', 'replacement')
    ),
    ADD CONSTRAINT purchase_observations_offer_phase_kind_valid CHECK (
        product_kind = 'subscription'
        OR (
            offer_phase IS NULL
            AND transaction_reason IS NULL
            AND lineage_started_at IS NULL
            AND cancellation_reason IS NULL
        )
    );

CREATE INDEX purchase_observations_subscription_metrics_idx
    ON purchase_observations (project_id, application_id)
    WHERE product_kind = 'subscription';

---- create above / drop below ----

DROP INDEX purchase_observations_subscription_metrics_idx;

ALTER TABLE purchase_observations
    DROP CONSTRAINT purchase_observations_offer_phase_kind_valid,
    DROP CONSTRAINT purchase_observations_cancellation_reason_valid,
    DROP CONSTRAINT purchase_observations_transaction_reason_valid,
    DROP CONSTRAINT purchase_observations_offer_phase_valid,
    DROP COLUMN cancellation_reason,
    DROP COLUMN lineage_started_at,
    DROP COLUMN transaction_reason,
    DROP COLUMN offer_phase;
