package postgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func preferenceSession(t *testing.T, p *Postgres, u *service.AuthUser) context.Context {
	t.Helper()
	sid := "session-" + u.ID
	if err := p.CreateAuthSession(t.Context(), service.AuthSession{Hash: sid, UserID: u.ID, Version: u.SessionVersion, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: u.ID, SessionID: sid})
}

func TestWorkspaceLifecyclePreferencesAndDeletion(t *testing.T) {
	p, ctx, workspace, admin := workspaceFixture(t)
	other, err := p.CreateWorkspace(ctx, "Keep", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	self := preferenceSession(t, p, admin)
	list, err := p.ListWorkspaces(self)
	if err != nil || list[0].ID != "legacy-default" {
		t.Fatalf("default order: %+v %v", list, err)
	}
	initial, err := p.GetWorkspacePreferences(self)
	if err != nil || initial.Mode != "default" {
		t.Fatalf("initial: %+v %v", initial, err)
	}
	if _, err := p.SaveWorkspacePreferences(self, service.WorkspacePreferences{Mode: "workspace", WorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	if err := p.SelectWorkspace(self, workspace.ID); err != nil {
		t.Fatal(err)
	}
	user := workspaceUser(t, p, "preference-user")
	userCtx := preferenceSession(t, p, user)
	if _, err := p.SaveWorkspacePreferences(userCtx, service.WorkspacePreferences{Mode: "workspace", WorkspaceID: workspace.ID}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("foreign preference admitted: %v", err)
	}
	if err := p.SelectWorkspace(userCtx, workspace.ID); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("foreign selection admitted: %v", err)
	}
	v, err := p.GetWorkspacePreferences(userCtx)
	if err != nil || v.Mode != "default" || v.LastWorkspaceID != "" {
		t.Fatalf("account preference leak: %+v %v", v, err)
	}
	viewer := workspaceMember(t, p, ctx, workspace.ID, user, "viewer")
	if _, err := p.DeleteWorkspace(viewer, workspace.Name); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("viewer deleted workspace: %v", err)
	}
	if _, err := p.DeleteWorkspace(ctx, "wrong name"); !errors.Is(err, service.ErrWorkspaceConflict) {
		t.Fatalf("confirmation bypass: %v", err)
	}
	defaultActor, _, err := p.ResolveWorkspaceAccess(ctx, "legacy-default", admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DeleteWorkspace(service.WithAccessPrincipal(ctx, defaultActor), "Default workspace"); !errors.Is(err, service.ErrWorkspaceConflict) {
		t.Fatalf("default deletion: %v", err)
	}

	// Exercise real dependent records and leave a sibling workspace untouched.
	for _, id := range []string{workspace.ID, other.ID} {
		for _, seed := range []struct {
			table string
			row   goqu.Record
		}{
			{"providers", goqu.Record{"id": "provider-" + id, "key": "provider", "workspace_id": id}},
			{"workflows", goqu.Record{"id": "workflow-" + id, "name": "Workflow", "workspace_id": id}},
			{"tokens", goqu.Record{"id": "token-" + id, "name": "Token", "token_hash": "hash-" + id, "token_prefix": "prefix", "workspace_id": id}},
			{"token_usage", goqu.Record{"token_id": "token-" + id, "model": "model", "workspace_id": id}},
			{"chat_sessions", goqu.Record{"id": "chat-" + id, "agent_id": "agent", "created_at": time.Now(), "updated_at": time.Now(), "workspace_id": id}},
			{"chat_messages", goqu.Record{"id": "message-" + id, "session_id": "chat-" + id, "role": "assistant", "created_at": time.Now(), "workspace_id": id}},
			{"media_objects", goqu.Record{"id": "media-" + id, "workspace_id": id, "owner_user_id": admin.ID, "backend": "filesystem", "storage_key": id + "/media.png", "content_type": "image/png", "size_bytes": 1, "checksum": "sum"}},
			{"execution_policies", goqu.Record{"workspace_id": id, "version": 1, "data": "{}"}},
		} {
			if _, err := p.goqu.Insert(p.workspaceTable(seed.table)).Rows(seed.row).Executor().ExecContext(ctx); err != nil {
				t.Fatalf("seed %s: %v", seed.table, err)
			}
		}
	}
	deleted, err := p.DeleteWorkspace(ctx, workspace.Name)
	if err != nil || deleted.WorkspaceID != workspace.ID {
		t.Fatalf("delete: %+v %v", deleted, err)
	}
	if len(deleted.MediaObjects) != 1 || deleted.MediaObjects[0].WorkspaceID != workspace.ID {
		t.Fatalf("deleted media cleanup handoff: %+v", deleted.MediaObjects)
	}
	for _, table := range workspaceDeletionTables {
		n, err := p.goqu.From(p.workspaceTable(table)).Where(goqu.Ex{"workspace_id": workspace.ID}).CountContext(ctx)
		if err != nil || n != 0 {
			t.Fatalf("leftover %s: %d %v", table, n, err)
		}
	}
	if n, _ := p.goqu.From(p.workspaceTable("chat_messages")).Where(goqu.Ex{"workspace_id": other.ID}).CountContext(ctx); n != 1 {
		t.Fatal("sibling conversation deleted")
	}
	if _, _, err := p.ResolveWorkspaceAccess(self, workspace.ID, admin.ID, "session-"+admin.ID); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("deleted workspace still accessible: %v", err)
	}
	v, err = p.GetWorkspacePreferences(self)
	if err != nil || v.WorkspaceID != "" || v.LastWorkspaceID != "" {
		t.Fatalf("deleted preference not cleared: %+v %v", v, err)
	}
}

func TestWorkspaceDeletionInventoryCoversSchema(t *testing.T) {
	p, _, _, _ := workspaceFixture(t)
	prefix := strings.TrimSuffix(p.workspaceTable("workspaces").GetTable(), "workspaces")
	var tables []string
	if err := p.goqu.From(goqu.T("columns").Schema("information_schema")).Select("table_name").Where(goqu.Ex{"column_name": "workspace_id"}, goqu.C("table_name").Like(prefix+"%")).ScanValsContext(t.Context(), &tables); err != nil {
		t.Fatal(err)
	}
	if len(tables) < 40 {
		t.Fatalf("schema inventory unexpectedly small: %d", len(tables))
	}
	for _, table := range tables {
		suffix := strings.TrimPrefix(table, prefix)
		if suffix != "workspace_preferences" && !slices.Contains(workspaceDeletionTables, suffix) {
			t.Errorf("workspace table missing from deletion: %s", suffix)
		}
	}
}
