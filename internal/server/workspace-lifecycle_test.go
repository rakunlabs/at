package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestWorkspacePermanentDeletionHTTP(t *testing.T) {
	p := postgrestest.New(t, nil)
	admin, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: testPasswordHash, Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	s := &Server{store: p, nativeAuth: a, loopGov: loopgov.New(loopgov.Config{WorkspaceRoot: root}, nil), config: config.Server{Workspace: &config.Workspace{Root: root}}}
	mux := ada.New()
	a.register(mux, "/at")
	s.registerWorkspaceRoutes(mux, "/at")
	cookie := nativeLoginCookie(t, mux, "admin")
	ctx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID})
	workspace, err := p.CreateWorkspace(ctx, "Delete me", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := p.CreateWorkspace(ctx, "Keep me", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{workspace.ID, other.ID} {
		dir := filepath.Join(root, "workspaces", id, "assets")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "result.txt"), []byte("result"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runCtx, cancelRun := context.WithCancel(t.Context())
	defer cancelRun()
	otherCtx, cancelOther := context.WithCancel(t.Context())
	defer cancelOther()
	chatCtx, cancelChat := context.WithCancel(t.Context())
	defer cancelChat()
	s.activeRuns.Store("delete-run", &activeRun{WorkspaceID: workspace.ID, Cancel: cancelRun})
	s.activeRuns.Store("keep-run", &activeRun{WorkspaceID: other.ID, Cancel: cancelOther})
	s.activeChatTurns.Store("chat", &activeChatTurn{WorkspaceID: workspace.ID, Cancel: cancelChat})
	path := "/at/api/v1/workspaces/" + workspace.ID + "/delete"
	selection := func(id string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("X-AT-Workspace-ID", id) }
	}
	w := nativeRequest(mux, "POST", path, `{"confirmation":"Delete me"}`, a.cfg.Origin, cookie, selection(other.ID))
	if w.Code != 404 {
		t.Fatalf("cross-workspace delete: %d %s", w.Code, w.Body)
	}
	w = nativeRequest(mux, "POST", path, `{"confirmation":"wrong"}`, a.cfg.Origin, cookie, selection(workspace.ID))
	if w.Code != 409 || runCtx.Err() != nil {
		t.Fatalf("confirmation: %d %s", w.Code, w.Body)
	}
	w = nativeRequest(mux, "PUT", "/at/auth/workspaces/preferences", `{"mode":"workspace","workspace_id":"`+workspace.ID+`"}`, a.cfg.Origin, cookie)
	if w.Code != 200 {
		t.Fatalf("preference: %d %s", w.Code, w.Body)
	}
	w = nativeRequest(mux, "POST", path, `{"confirmation":"Delete me"}`, a.cfg.Origin, cookie, selection(workspace.ID))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted":true`) || strings.Contains(w.Body.String(), "cleanup_warning") {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if runCtx.Err() == nil || chatCtx.Err() == nil || otherCtx.Err() != nil {
		t.Fatal("cancellation crossed workspace boundary")
	}
	if _, err := os.Stat(filepath.Join(root, "workspaces", workspace.ID)); !os.IsNotExist(err) {
		t.Fatalf("workspace files remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "workspaces", other.ID, "assets", "result.txt")); err != nil {
		t.Fatal("sibling files deleted")
	}
	w = nativeRequest(mux, "GET", "/at/auth/workspaces", "", "", cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), workspace.ID) || !strings.Contains(w.Body.String(), other.ID) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	w = nativeRequest(mux, "GET", "/at/auth/workspaces/preferences", "", "", cookie)
	if w.Code != 200 || strings.Contains(w.Body.String(), workspace.ID) {
		t.Fatalf("stale preference: %d %s", w.Code, w.Body)
	}
}
