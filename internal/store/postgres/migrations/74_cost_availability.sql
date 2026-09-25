-- Distinguish a genuinely zero-cost call from one whose usage or model pricing
-- could not be resolved. Historical positive costs are known; historical zeroes
-- stay unknown because the old schema could not tell free pricing from missing
-- usage/pricing.
ALTER TABLE ${TABLE_PREFIX}cost_events
    ADD COLUMN cost_available BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE ${TABLE_PREFIX}cost_events
SET cost_available = TRUE
WHERE cost_cents > 0;
