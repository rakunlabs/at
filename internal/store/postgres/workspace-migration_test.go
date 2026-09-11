package postgres

import (
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/muz"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func TestWorkspacePostgresLegacyOwnershipMigration(t *testing.T) {
	dsn := testDSN()
	pingTestPostgres(t, dsn)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.ToLower("t" + ulid.Make().String() + "_")
	p := &Postgres{db: db, goqu: goqu.New("postgres", db), tableAuthUsers: goqu.T(prefix + "auth_users"), tableAuthSessions: goqu.T(prefix + "auth_sessions")}
	t.Cleanup(func() { dropTestTables(t, p, prefix); db.Close() })
	legacy := fstest.MapFS{}
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		n, _, _ := strings.Cut(entry.Name(), "_")
		version, _ := strconv.Atoi(n)
		if version > 30 {
			continue
		}
		data, err := migrationFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		legacy["migrations/"+entry.Name()] = &fstest.MapFile{Data: data}
	}
	values := map[string]string{"TABLE_PREFIX": prefix}
	ledger := prefix + "migrations"
	m := muz.Migrate{Path: "migrations", FS: legacy, Extension: ".sql", Values: values}
	if err = m.Migrate(t.Context(), muz.NewPostgresDriver(db, ledger, slog.Default())); err != nil {
		t.Fatal(err)
	}
	for _, u := range []goqu.Record{{"id": "admin", "username": "admin", "password_hash": "hash", "admin": true}, {"id": "ordinary", "username": "ordinary", "password_hash": "hash", "admin": false}} {
		if _, err = p.goqu.Insert(p.tableAuthUsers).Rows(u).Executor().ExecContext(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	for table, row := range map[string]goqu.Record{
		"organizations": {"id": "org", "name": "Legacy", "created_at": now, "updated_at": now},
		"agents":        {"id": "agent", "name": "Legacy", "config": "{}", "created_at": now, "updated_at": now},
		"goals":         {"id": "goal", "name": "Legacy", "organization_id": "org", "created_at": now, "updated_at": now},
		"tasks":         {"id": "task", "title": "Legacy", "organization_id": "org", "goal_id": "goal", "assigned_agent_id": "agent", "created_at": now, "updated_at": now},
		"providers":     {"id": "provider", "key": "legacy", "config": `{"api_key":"preserved"}`},
	} {
		if _, err = p.goqu.Insert(p.workspaceTable(table)).Rows(row).Executor().ExecContext(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	// Apply the real remaining migration set, then restart it against its ledger.
	for range 2 {
		if err = MigrateDB(t.Context(), &config.Migrate{Datasource: dsn, Table: ledger, Values: values}); err != nil {
			t.Fatal(err)
		}
	}
	var members []service.WorkspaceMembership
	if err = p.goqu.From(p.workspaceTable("workspace_memberships")).ScanStructsContext(t.Context(), &members); err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].UserID != "admin" || members[0].Role != "owner" || members[0].WorkspaceID != "legacy-default" {
		t.Fatalf("incorrect backfill authority: %+v", members)
	}
	for table, id := range map[string]string{"organizations": "org", "agents": "agent", "goals": "goal", "tasks": "task", "providers": "provider"} {
		var workspace string
		found, err := p.goqu.From(p.workspaceTable(table)).Select("workspace_id").Where(goqu.Ex{"id": id}).ScanValContext(t.Context(), &workspace)
		if err != nil || !found || workspace != "legacy-default" {
			t.Fatalf("%s ownership lost: %q %v", table, workspace, err)
		}
	}
	var task struct {
		Organization string `db:"organization_id"`
		Goal         string `db:"goal_id"`
		Agent        string `db:"assigned_agent_id"`
	}
	if _, err = p.goqu.From(p.workspaceTable("tasks")).Where(goqu.Ex{"id": "task"}).ScanStructContext(t.Context(), &task); err != nil {
		t.Fatal(err)
	}
	if task.Organization != "org" || task.Goal != "goal" || task.Agent != "agent" {
		t.Fatalf("relationships changed: %+v", task)
	}
	var configJSON string
	if _, err = p.goqu.From(p.workspaceTable("providers")).Select("config").Where(goqu.Ex{"id": "provider"}).ScanValContext(t.Context(), &configJSON); err != nil || !strings.Contains(configJSON, "preserved") {
		t.Fatalf("provider changed: %s %v", configJSON, err)
	}
	// Migration 44 deletes the retired personal chat feature, data included.
	for _, table := range []string{"personal_messages", "personal_conversations"} {
		var oid sql.NullString
		if err = db.QueryRowContext(t.Context(), "SELECT to_regclass($1)::text", prefix+table).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if oid.Valid {
			t.Fatalf("%s survived the personal chat removal: %s", table, oid.String)
		}
	}
}
