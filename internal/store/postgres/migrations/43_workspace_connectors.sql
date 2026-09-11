ALTER TABLE ${TABLE_PREFIX}connectors DROP CONSTRAINT ${TABLE_PREFIX}connectors_pkey;
ALTER TABLE ${TABLE_PREFIX}connectors ADD PRIMARY KEY(workspace_id,slug);
