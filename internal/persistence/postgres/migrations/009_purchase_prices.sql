-- Retain provider-signed transaction prices for non-accounting sandbox value analytics.
ALTER TABLE purchase_observations
    ADD COLUMN price_milliunits bigint,
    ADD COLUMN price_currency text,
    ADD CONSTRAINT purchase_observations_price_pair_valid CHECK (
        (price_milliunits IS NULL AND price_currency IS NULL)
        OR (price_milliunits IS NOT NULL AND price_currency IS NOT NULL)
    ),
    ADD CONSTRAINT purchase_observations_price_amount_valid CHECK (
        price_milliunits IS NULL OR price_milliunits >= 0
    ),
    ADD CONSTRAINT purchase_observations_price_currency_valid CHECK (
        price_currency IS NULL OR price_currency ~ '^[A-Z]{3}$'
    );

CREATE INDEX purchase_observations_sandbox_value_idx
    ON purchase_observations (project_id, occurred_at, application_id)
    INCLUDE (observed_at, price_milliunits, price_currency, lifecycle_state, ownership);

---- create above / drop below ----

DROP INDEX purchase_observations_sandbox_value_idx;

ALTER TABLE purchase_observations
    DROP CONSTRAINT purchase_observations_price_currency_valid,
    DROP CONSTRAINT purchase_observations_price_amount_valid,
    DROP CONSTRAINT purchase_observations_price_pair_valid,
    DROP COLUMN price_currency,
    DROP COLUMN price_milliunits;
