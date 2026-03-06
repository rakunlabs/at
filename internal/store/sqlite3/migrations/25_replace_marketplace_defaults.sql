-- Remove old default marketplace sources.
DELETE FROM ${TABLE_PREFIX}marketplace_sources WHERE id IN ('default-clawhub', 'default-skillsmp', 'default-mcpmarket', 'default-skillfish');
