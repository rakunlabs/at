-- Add category and tags to skills
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}skills ADD COLUMN IF NOT EXISTS tags JSONB NOT NULL DEFAULT '[]';

-- Add category and tags to mcp_sets
ALTER TABLE ${TABLE_PREFIX}mcp_sets ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}mcp_sets ADD COLUMN IF NOT EXISTS tags JSONB NOT NULL DEFAULT '[]';

-- Index for filtering by category
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}skills_category ON ${TABLE_PREFIX}skills(category);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}mcp_sets_category ON ${TABLE_PREFIX}mcp_sets(category);
