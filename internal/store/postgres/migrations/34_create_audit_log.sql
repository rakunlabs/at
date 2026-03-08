CREATE TABLE IF NOT EXISTS ${TABLE_PREFIX}audit_log (
    id TEXT PRIMARY KEY,
    organization_id TEXT DEFAULT '',
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    details JSONB,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}audit_resource ON ${TABLE_PREFIX}audit_log(resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}audit_actor ON ${TABLE_PREFIX}audit_log(actor_id);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}audit_created ON ${TABLE_PREFIX}audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_${TABLE_PREFIX}audit_org ON ${TABLE_PREFIX}audit_log(organization_id);
