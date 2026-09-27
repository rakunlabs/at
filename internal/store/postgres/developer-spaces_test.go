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
	if created.Config.Plan.SystemPrompt == "" || created.Config.Build.SystemPrompt == "" || created.Config.Review.SystemPrompt == "" {
		t.Fatalf("default profiles missing: %+v", created.Config)
	}
	if created.Config.Plan.Rules[len(created.Config.Plan.Rules)-1].Effect != "deny" || created.Config.Build.Rules[len(created.Config.Build.Rules)-1].Resource != "push" {
		t.Fatalf("unsafe default permissions: %+v", created.Config)
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

	created.MemoryLimit = "2Gi"
	updated, err := p.UpdateDeveloperSpace(ownerCtx, *created)
	if err != nil || updated.MemoryLimit != "2Gi" || updated.Status != service.DeveloperSpacePending {
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

func TestDeveloperSessionLifecycle(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	space, err := p.EnsureDeveloperSpace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateDeveloperSession(ctx, service.DeveloperSession{
		SpaceID: space.ID, ProjectPath: "../escape", Mode: service.DeveloperModeBuild, Provider: "openai",
	}); err == nil {
		t.Fatal("session accepted a path outside the space")
	}
	session, err := p.CreateDeveloperSession(ctx, service.DeveloperSession{
		SpaceID: space.ID, ProjectPath: "at/", Mode: service.DeveloperModeBuild,
		Title: "Implement storage", Provider: "openai", Model: "gpt",
	})
	if err != nil || session.Status != service.DeveloperSessionIdle || session.ProjectPath != "at" {
		t.Fatalf("session: %+v %v", session, err)
	}
	if renamed, err := p.RenameDeveloperSession(ctx, session.ID, "  Storage  "); err != nil || renamed.Title != "Storage" {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	if changed, err := p.UpdateDeveloperSessionSettings(ctx, session.ID, service.DeveloperSessionSettings{Mode: service.DeveloperModePlan, AgentID: "agent-1", Provider: "anthropic", Model: "claude"}); err != nil || changed.Mode != service.DeveloperModePlan || changed.Provider != "anthropic" || changed.AgentID != "agent-1" {
		t.Fatalf("settings: %+v %v", changed, err)
	}
	if _, err := p.AppendDeveloperSessionMessage(ctx, service.DeveloperSessionMessage{SessionID: session.ID, Role: "user", Content: "Implement it"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AppendDeveloperSessionMessage(ctx, service.DeveloperSessionMessage{SessionID: session.ID, Role: "assistant", Content: []service.ContentBlock{{Type: "text", Text: "Working"}}}); err != nil {
		t.Fatal(err)
	}
	messages, err := p.ListDeveloperSessionMessages(ctx, session.ID)
	if err != nil || len(messages) != 2 || messages[0].Content != "Implement it" {
		t.Fatalf("messages: %+v %v", messages, err)
	}
	if blocks, ok := messages[1].Content.([]service.ContentBlock); !ok || len(blocks) != 1 || blocks[0].Text != "Working" {
		t.Fatalf("assistant blocks were not restored canonically: %#v", messages[1].Content)
	}
	if _, err := p.BeginDeveloperSessionRun(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.BeginDeveloperSessionRun(ctx, session.ID); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("concurrent run: %v", err)
	}
	if _, err := p.UpdateDeveloperSessionSettings(ctx, session.ID, service.DeveloperSessionSettings{Mode: service.DeveloperModeBuild, Provider: "openai", Model: "gpt"}); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("settings changed during a run: %v", err)
	}
	if _, err := p.SetDeveloperSessionRuntime(ctx, session.ID, service.DeveloperSessionCompleted, ""); err != nil {
		t.Fatal(err)
	}
	pending, err := p.SaveDeveloperPendingTool(ctx, service.DeveloperPendingTool{SessionID: session.ID, Kind: "permission", TraceID: "trace-1", Step: 4, ToolCalls: []service.ToolCall{{ID: "call-1", Name: "run_command", Arguments: map[string]any{"command": "go"}}}})
	if err != nil || len(pending.ToolCalls) != 1 || pending.ToolCalls[0].Name != "run_command" || pending.Kind != "permission" || pending.TraceID != "trace-1" || pending.Step != 4 {
		t.Fatalf("pending tool: %+v %v", pending, err)
	}
	claimed, err := p.ClaimDeveloperPendingTool(ctx, session.ID)
	if err != nil || claimed == nil || len(claimed.ToolCalls) != 1 || claimed.State != "executing" {
		t.Fatalf("claimed pending tool: %+v %v", claimed, err)
	}
	if claimed, err := p.ClaimDeveloperPendingTool(ctx, session.ID); err != nil || claimed != nil {
		t.Fatalf("double claim: %+v %v", claimed, err)
	}
	if _, err := p.ResolveDeveloperPendingTool(ctx, session.ID, []service.ContentBlock{{Type: "tool_result", ToolUseID: "call-1", Content: "ok"}}); err != nil {
		t.Fatalf("resolve pending tool: %v", err)
	}
	if pending, err := p.GetDeveloperPendingTool(ctx, session.ID); err != nil || pending != nil {
		t.Fatalf("resolved pending tool: %+v %v", pending, err)
	}
	snapshot, err := p.SaveDeveloperSessionSnapshot(ctx, service.DeveloperSessionSnapshot{SessionID: session.ID, Step: 1, Phase: "before"})
	if err != nil || snapshot.Step != 1 || snapshot.Phase != "before" {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	snapshots, err := p.ListDeveloperSessionSnapshots(ctx, session.ID)
	if err != nil || len(snapshots) != 1 || snapshots[0].ID != snapshot.ID {
		t.Fatalf("snapshots: %+v %v", snapshots, err)
	}

	if records, err := p.ListDeveloperSessions(ctx, space.ID); err != nil || len(records) != 1 || records[0].ID != session.ID {
		t.Fatalf("session list: %+v %v", records, err)
	}
	if err := p.DeleteDeveloperSpace(ctx, space.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.GetDeveloperSession(ctx, session.ID); !errors.Is(err, service.ErrDeveloperSpaceNotFound) {
		t.Fatalf("session survived space deletion: %v", err)
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
// empty project path; the rest go with their space.
func TestDeveloperSpaceSingleMigration(t *testing.T) {
	p := newTestStore(t, nil)
	prefix := strings.TrimSuffix(fmt.Sprint(p.tableAuthUsers.GetTable()), "auth_users") + "dev83_"
	files, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	previous := fstest.MapFS{}
	for _, file := range files {
		var version int
		if _, err := fmt.Sscanf(file.Name(), "%d_", &version); err != nil || version >= 83 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		previous["migrations/"+file.Name()] = &fstest.MapFile{Data: body}
	}
	m := muz.Migrate{Path: "migrations", FS: previous, Extension: ".sql", Values: map[string]string{"TABLE_PREFIX": prefix}}
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
	m.FS = migrationFS
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
