package postgres

import (
	"strings"
	"testing"
)

func TestSkillExecutionMigrationUsesTablePrefix(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/71_skill_execution.sql")
	if err != nil {
		t.Fatal(err)
	}

	const prefix = "test_"
	script := strings.ReplaceAll(string(body), "${TABLE_PREFIX}", prefix)
	if strings.Contains(script, "ALTER TABLE skills") {
		t.Fatal("migration targets the unprefixed skills relation")
	}
	if got := strings.Count(script, "ALTER TABLE "+prefix+"skills"); got != 3 {
		t.Fatalf("expected all three statements to target the prefixed skills relation, got %d", got)
	}
	if !strings.Contains(script, "CONSTRAINT "+prefix+"skills_execution_context_check") {
		t.Fatal("migration does not prefix the skill execution constraint")
	}
}

func TestSkillExecutionModeMigrationDropsStoredBackground(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/76_skill_execution_mode.sql")
	if err != nil {
		t.Fatal(err)
	}

	const prefix = "test_"
	script := strings.ReplaceAll(string(body), "${TABLE_PREFIX}", prefix)
	if got := strings.Count(script, "ALTER TABLE "+prefix+"skills"); got != 3 {
		t.Fatalf("expected all three statements to target the prefixed skills relation, got %d", got)
	}
	if !strings.Contains(script, "DROP COLUMN IF EXISTS execution_background") {
		t.Fatal("migration does not remove the obsolete skill-level background setting")
	}
}
