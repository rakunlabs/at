-- +migrate Up
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN canvas_layout TEXT NOT NULL DEFAULT '{}';

-- +migrate Down
-- SQLite does not support DROP COLUMN before 3.35; leaving column in place.
