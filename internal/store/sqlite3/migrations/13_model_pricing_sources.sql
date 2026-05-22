-- Source metadata for model pricing sync/import workflows.
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN source TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN source_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN source_model TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN source_url TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN source_prompt_price_per_1m REAL NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN source_completion_price_per_1m REAL NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN source_cache_read_price_per_1m REAL NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN source_cache_write_price_per_1m REAL NOT NULL DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN manual_override INTEGER NOT NULL DEFAULT 1;
ALTER TABLE ${TABLE_PREFIX}model_pricing ADD COLUMN last_synced_at TEXT NOT NULL DEFAULT '';
