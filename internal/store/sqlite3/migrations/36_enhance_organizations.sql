ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN issue_prefix TEXT DEFAULT '';
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN issue_counter INTEGER DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN budget_monthly_cents INTEGER DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN spent_monthly_cents INTEGER DEFAULT 0;
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN budget_reset_at TEXT DEFAULT NULL;
ALTER TABLE ${TABLE_PREFIX}organizations ADD COLUMN require_board_approval_for_new_agents INTEGER DEFAULT 0;
