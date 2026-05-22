-- Cache-aware pricing and token spend budgets.
ALTER TABLE ${TABLE_PREFIX}tokens ADD COLUMN spend_limit_cents REAL DEFAULT NULL;

ALTER TABLE ${TABLE_PREFIX}token_usage ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}token_usage ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;

ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN cache_read_price_per_1m REAL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN cache_write_price_per_1m REAL DEFAULT 0;

ALTER TABLE ${TABLE_PREFIX}cost_events ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}cost_events ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;
