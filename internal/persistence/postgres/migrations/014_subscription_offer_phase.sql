-- Retain the provider-signed offer phase of subscription periods for subscription metrics.
-- NULL means the phase was not recorded: rows stored before this migration, providers whose
-- adapter does not map an offer field, and every non-subscription observation.
ALTER TABLE purchase_observations
    ADD COLUMN offer_phase text,
    ADD CONSTRAINT purchase_observations_offer_phase_valid CHECK (
        offer_phase IS NULL
        OR offer_phase IN ('base', 'free_trial', 'discounted', 'proration', 'unknown')
    ),
    ADD CONSTRAINT purchase_observations_offer_phase_kind_valid CHECK (
        offer_phase IS NULL OR product_kind = 'subscription'
    );

CREATE INDEX purchase_observations_subscription_metrics_idx
    ON purchase_observations (project_id, application_id)
    WHERE product_kind = 'subscription';

---- create above / drop below ----

DROP INDEX purchase_observations_subscription_metrics_idx;

ALTER TABLE purchase_observations
    DROP CONSTRAINT purchase_observations_offer_phase_kind_valid,
    DROP CONSTRAINT purchase_observations_offer_phase_valid,
    DROP COLUMN offer_phase;
