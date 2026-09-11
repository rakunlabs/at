ALTER TABLE ${TABLE_PREFIX}providers DROP CONSTRAINT IF EXISTS ${TABLE_PREFIX}providers_key_key;
CREATE UNIQUE INDEX ${TABLE_PREFIX}providers_workspace_key ON ${TABLE_PREFIX}providers(workspace_id,key);
CREATE TABLE ${TABLE_PREFIX}workspace_provider_grants (
    workspace_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}workspaces(id),
    provider_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}providers(id) ON DELETE CASCADE,
    model_patterns JSONB NOT NULL,
    PRIMARY KEY(workspace_id,provider_id)
);
CREATE INDEX ON ${TABLE_PREFIX}organizations(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}agents(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}goals(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}projects(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}tasks(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}issue_comments(workspace_id,task_id);
CREATE INDEX ON ${TABLE_PREFIX}labels(workspace_id,organization_id);
CREATE INDEX ON ${TABLE_PREFIX}task_labels(workspace_id,task_id,label_id);
CREATE INDEX ON ${TABLE_PREFIX}approvals(workspace_id,id);
CREATE INDEX ON ${TABLE_PREFIX}organization_agents(workspace_id,organization_id,agent_id);
