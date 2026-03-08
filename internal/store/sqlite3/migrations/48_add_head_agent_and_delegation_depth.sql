ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN head_agent_id TEXT DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN max_delegation_depth INTEGER DEFAULT 10;
