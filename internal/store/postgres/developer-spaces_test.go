package postgres

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/rakunlabs/muz"

	"github.com/rakunlabs/at/internal/service"
)

func TestDeveloperSpacesAreUserAndWorkspaceScoped(t *testing.T) {
	p, ownerCtx, workspace, _ := workspaceFixture(t)
	created, err := p.EnsureDeveloperSpace(ownerCtx)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkspaceID != workspace.ID || created.OwnerUserID == "" || created.Status != service.DeveloperSpacePending {
		t.Fatalf("created: %+v", created)
	}
	if created.Config.DisableFormatter {
		t.Fatalf("formatter must default on: %+v", created.Config)
	}

	again, err := p.EnsureDeveloperSpace(ownerCtx)
	if err != nil || again.ID != created.ID {
		t.Fatalf("second ensure created another space: %+v %v", again, err)
	}

	other := workspaceUser(t, p, "developer-other")
	otherCtx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: other.ID, WorkspaceID: workspace.ID})
	if _, err := p.GetDeveloperSpace(otherCtx, created.ID); !errors.Is(err, service.ErrDeveloperSpaceNotFound) {
		t.Fatalf("foreign get: %v", err)
	}
	if err := p.DeleteDeveloperSpace(otherCtx, created.ID); !errors.Is(err, service.ErrDeveloperSpaceNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	otherSpace, err := p.EnsureDeveloperSpace(otherCtx)
	if err != nil || otherSpace.ID == created.ID {
		t.Fatalf("other user's space: %+v %v", otherSpace, err)
	}

	created.MemoryLimit = "2g"
	updated, err := p.UpdateDeveloperSpace(ownerCtx, *created)
	if err != nil || updated.MemoryLimit != "2g" || updated.Status != service.DeveloperSpacePending {
		t.Fatalf("updated: %+v %v", updated, err)
	}
	if err := p.DeleteDeveloperSpace(ownerCtx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetDeveloperSpace(ownerCtx, created.ID); !errors.Is(err, service.ErrDeveloperSpaceNotFound) {
		t.Fatalf("read deleted: %v", err)
	}
}

func TestDeveloperSpaceRequiresPrincipal(t *testing.T) {
	p := newTestStore(t, nil)
	if _, err := p.EnsureDeveloperSpace(t.Context()); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("ensure without principal: %v", err)
	}
}

func TestDeveloperPathAndRemoteValidation(t *testing.T) {
	for _, candidate := range []string{"/etc", "../x", "a/../b", "a//b", "a\\b", "./a"} {
		if _, err := service.CleanDeveloperPath(candidate); err == nil {
			t.Errorf("CleanDeveloperPath(%q) accepted", candidate)
		}
	}
	if got, err := service.CleanDeveloperPath(" at/internal/ "); err != nil || got != "at/internal" {
		t.Fatalf("clean: %q %v", got, err)
	}
	for name, remote := range map[string]string{
		"host path":   "/srv/repo",
		"file URL":    "file:///srv/repo",
		"credentials": "https://token@example.test/repo.git",
		"option":      "--upload-pack=evil",
	} {
		t.Run(name, func(t *testing.T) {
			if err := service.ValidDeveloperRemote(remote); err == nil {
				t.Fatalf("accepted %q", remote)
			}
		})
	}
	if err := service.ValidDeveloperRemote("git@example.test:acme/repo.git"); err != nil {
		t.Fatalf("ssh shorthand: %v", err)
	}
}

// Migration 83 collapses each account's spaces into its oldest one and drops
// the repository/worktree records. Sessions in the kept space survive with an
// empty project path; the rest go with their space. Migration 114 later
// drops sessions entirely, so this replays the set only up to 113.
func TestDeveloperSpaceSingleMigration(t *testing.T) {
	p := newTestStore(t, nil)
	prefix := strings.TrimSuffix(fmt.Sprint(p.tableAuthUsers.GetTable()), "auth_users") + "dev83_"
	m := muz.Migrate{Path: "migrations", FS: migrationsBefore(t, 83), Extension: ".sql", Values: map[string]string{"TABLE_PREFIX": prefix}}
	driver := muz.NewPostgresDriver(p.db, prefix+"migrations", slog.Default())
	if err := m.Migrate(t.Context(), driver); err != nil {
		t.Fatal(err)
	}
	seed := fmt.Sprintf(`
INSERT INTO %[1]sauth_users(id,username,password_hash) VALUES ('u','user','hash');
INSERT INTO %[1]sdeveloper_spaces(id,workspace_id,owner_user_id,name,created_at) VALUES
  ('old','legacy-default','u','First', now() - interval '2 days'),
  ('new','legacy-default','u','Second', now() - interval '1 day');
INSERT INTO %[1]sdeveloper_repositories(id,space_id,workspace_id,owner_user_id,name,remote_url) VALUES ('r','old','legacy-default','u','repo','https://example.test/r.git');
INSERT INTO %[1]sdeveloper_worktrees(id,repository_id,space_id,workspace_id,owner_user_id,name,branch) VALUES ('w','r','old','legacy-default','u','main','main');
INSERT INTO %[1]sdeveloper_sessions(id,space_id,repository_id,worktree_id,workspace_id,owner_user_id,mode) VALUES
  ('kept','old','r','w','legacy-default','u','build'),
  ('dropped','new',NULL,NULL,'legacy-default','u','plan');`, prefix)
	if _, err := p.db.ExecContext(t.Context(), seed); err != nil {
		t.Fatal(err)
	}
	m.FS = migrationsBefore(t, 114)
	if err := m.Migrate(t.Context(), driver); err != nil {
		t.Fatal(err)
	}
	var spaces []string
	if err := p.goqu.From(prefix+"developer_spaces").Select("id").ScanValsContext(t.Context(), &spaces); err != nil || len(spaces) != 1 || spaces[0] != "old" {
		t.Fatalf("spaces after migration: %v %v", spaces, err)
	}
	var sessions []struct {
		ID          string `db:"id"`
		ProjectPath string `db:"project_path"`
	}
	if err := p.goqu.From(prefix+"developer_sessions").Select("id", "project_path").ScanStructsContext(t.Context(), &sessions); err != nil || len(sessions) != 1 || sessions[0].ID != "kept" || sessions[0].ProjectPath != "" {
		t.Fatalf("sessions after migration: %+v %v", sessions, err)
	}
	if _, err := p.db.ExecContext(t.Context(), fmt.Sprintf("INSERT INTO %sdeveloper_spaces(id,workspace_id,owner_user_id,name) VALUES ('dup','legacy-default','u','x')", prefix)); err == nil {
		t.Fatal("a second space for the same account was accepted")
	}
}

func migrationsBefore(t *testing.T, limit int) fstest.MapFS {
	t.Helper()
	files, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, file := range files {
		var version int
		if _, err := fmt.Sscanf(file.Name(), "%d_", &version); err != nil || version >= limit {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		out["migrations/"+file.Name()] = &fstest.MapFile{Data: body}
	}
	return out
}

// Migration 114 removes the Developer Space AI chat: session tables and their
// snapshot catalog rows go, spaces stay, and only the formatter switch
// survives in the space config.
func TestDeveloperSpaceChatRemovalMigration(t *testing.T) {
	p := newTestStore(t, nil)
	prefix := strings.TrimSuffix(fmt.Sprint(p.tableAuthUsers.GetTable()), "auth_users") + "dev114_"
	m := muz.Migrate{Path: "migrations", FS: migrationsBefore(t, 114), Extension: ".sql", Values: map[string]string{"TABLE_PREFIX": prefix}}
	driver := muz.NewPostgresDriver(p.db, prefix+"migrations", slog.Default())
	if err := m.Migrate(t.Context(), driver); err != nil {
		t.Fatal(err)
	}
	seed := fmt.Sprintf(`
INSERT INTO %[1]sauth_users(id,username,password_hash) VALUES ('u','user','hash');
INSERT INTO %[1]sdeveloper_spaces(id,workspace_id,owner_user_id,name,config) VALUES
  ('a','legacy-default','u','A','{"build":{"system_prompt":"x"},"disable_formatter":true}');
INSERT INTO %[1]sdeveloper_sessions(id,space_id,workspace_id,owner_user_id,mode) VALUES ('s','a','legacy-default','u','build');
INSERT INTO %[1]sstorage_objects(id,workspace_id,namespace,path,backend,storage_key,content_type,size_bytes,checksum) VALUES
  ('o1','legacy-default','snapshots','developer-sessions/s/000001-before.json','filesystem','k1','application/json',1,''),
  ('o2','legacy-default','media','developer-sessions/keep.png','filesystem','k2','image/png',1,'');`, prefix)
	if _, err := p.db.ExecContext(t.Context(), seed); err != nil {
		t.Fatal(err)
	}
	m.FS = migrationFS
	if err := m.Migrate(t.Context(), driver); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := p.db.QueryRowContext(t.Context(), "SELECT to_regclass($1) IS NOT NULL", prefix+"developer_sessions").Scan(&exists); err != nil || exists {
		t.Fatalf("developer_sessions still exists: %v %v", exists, err)
	}
	var config string
	if err := p.db.QueryRowContext(t.Context(), fmt.Sprintf("SELECT config::text FROM %sdeveloper_spaces WHERE id = 'a'", prefix)).Scan(&config); err != nil || config != `{"disable_formatter": true}` {
		t.Fatalf("space config = %q %v", config, err)
	}
	var objects []string
	if err := p.goqu.From(prefix+"storage_objects").Select("id").ScanValsContext(t.Context(), &objects); err != nil || len(objects) != 1 || objects[0] != "o2" {
		t.Fatalf("storage objects after migration: %v %v", objects, err)
	}
}
