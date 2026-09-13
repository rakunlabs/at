DO '
DECLARE constraint_name TEXT;
BEGIN
    FOR constraint_name IN
        SELECT c.conname FROM pg_constraint c
        JOIN pg_attribute a ON a.attrelid = c.conrelid AND c.conkey = ARRAY[a.attnum]
        WHERE c.conrelid = ''${TABLE_PREFIX}execution_service_bindings''::regclass
          AND c.contype = ''c'' AND a.attname = ''kind''
    LOOP
        EXECUTE format(''ALTER TABLE ${TABLE_PREFIX}execution_service_bindings DROP CONSTRAINT %I'', constraint_name);
    END LOOP;
END';
ALTER TABLE ${TABLE_PREFIX}execution_service_bindings
    ADD CONSTRAINT ${TABLE_PREFIX}exec_binding_kind
    CHECK (kind IN ('bot', 'trigger', 'mcp'));
