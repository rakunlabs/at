package server

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func runtimeTestContext(t *testing.T, root string, revoked *atomic.Bool) context.Context {
	t.Helper()
	ctx, err := service.BindExecution(t.Context(), service.ExecutionProvenance{RunID: "r", UserID: "u", WorkspaceID: "w", Source: "test"}, root, func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
		return service.ExecutionValidation{Allowed: !revoked.Load(), Policy: service.ExecutionPolicy{WorkspaceID: "w", Mode: service.ExecutionRestricted, AllowedTools: []string{"bash_execute", "batch_execute", "file_read", "file_write", "file_list"}, AllowedNodes: []string{"exec", "agent_call"}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestRuntimeLiveWorkspacePolicyGrantAndMembershipRevocation(t *testing.T) {
	store := postgrestest.New(t, nil)
	admin, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "runtime-admin", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "runtime-owner"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []*service.AuthUser{admin, owner} {
		if err := store.CreateAuthSession(t.Context(), service.AuthSession{Hash: u.ID, UserID: u.ID, Version: u.SessionVersion, Transport: "web", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := store.CreateWorkspace(service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID}), "runtime workspace", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminPrincipal, _, err := store.ResolveWorkspaceAccess(t.Context(), workspace.ID, admin.ID, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := service.WithAccessPrincipal(t.Context(), adminPrincipal)
	workspace.ExecutionEnabled = true
	if _, err := store.UpdateWorkspace(adminCtx, *workspace); err != nil {
		t.Fatal(err)
	}
	ownerPrincipal, _, err := store.ResolveWorkspaceAccess(t.Context(), workspace.ID, owner.ID, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := service.WithAccessPrincipal(t.Context(), ownerPrincipal)
	s := &Server{store: store, loopGov: loopgov.New(loopgov.Config{WorkspaceRoot: t.TempDir()}, nil)}
	body := `{"mode":"trusted_host","version":0,"allowed_tools":["bash_execute"],"granted_by":"forged"}`
	for _, tt := range []struct {
		ctx  context.Context
		want int
	}{{ownerCtx, 403}, {adminCtx, 200}} {
		req := httptest.NewRequest(http.MethodPut, "/execution-policy", strings.NewReader(body)).WithContext(tt.ctx)
		req.SetPathValue("workspace", workspace.ID)
		w := httptest.NewRecorder()
		s.RuntimeExecutionPolicyAPI(w, req)
		if w.Code != tt.want {
			t.Fatalf("policy write %d want %d: %s", w.Code, tt.want, w.Body.String())
		}
	}
	policy, err := store.GetExecutionPolicy(t.Context(), workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.GrantedBy != admin.ID {
		t.Fatal("policy accepted forged granting actor")
	}
	workerRequest := httptest.NewRequest(http.MethodPut, "/execution-policy", strings.NewReader(`{"mode":"isolated_worker","version":1}`)).WithContext(adminCtx)
	workerRequest.SetPathValue("workspace", workspace.ID)
	workerResponse := httptest.NewRecorder()
	s.RuntimeExecutionPolicyAPI(workerResponse, workerRequest)
	if workerResponse.Code != http.StatusNotImplemented {
		t.Fatalf("unsupported worker policy status %d: %s", workerResponse.Code, workerResponse.Body.String())
	}
	bound, err := s.bindRuntimePrincipal(ownerCtx, "test")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.dispatchBuiltinTool(bound, "bash_execute", map[string]any{"command": "printf trusted"})
	if err != nil || result != "trusted" {
		t.Fatalf("explicit platform grant did not enable owner execution: %q %v", result, err)
	}
	if err := store.SetWorkspaceMember(adminCtx, service.WorkspaceMembership{WorkspaceID: workspace.ID, UserID: admin.ID, Role: "owner", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceMember(adminCtx, service.WorkspaceMembership{WorkspaceID: workspace.ID, UserID: owner.ID, Role: "owner", Status: "revoked"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.dispatchBuiltinTool(bound, "bash_execute", map[string]any{"command": "printf revoked"}); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("revoked membership kept host authority: %v", err)
	}
}

func TestRuntimeMediaRangeRevocationAndScope(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "secret"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	ctx := runtimeTestContext(t, root, &revoked)
	s := &Server{}
	for _, tt := range []struct {
		path   string
		status int
	}{{"clip.mp4", 206}, {"../secret", 403}, {"escape/secret", 400}, {other + "/secret", 403}} {
		req := httptest.NewRequest(http.MethodGet, "/files/serve?path="+tt.path, nil).WithContext(ctx)
		req.Header.Set("Range", "bytes=2-5")
		w := httptest.NewRecorder()
		s.FileServeAPI(w, req)
		if w.Code != tt.status {
			t.Fatalf("%s: status %d: %s", tt.path, w.Code, w.Body.String())
		}
		if tt.status == 206 && (w.Body.String() != "2345" || w.Header().Get("Content-Range") != "bytes 2-5/10") {
			t.Fatalf("broken range: %s %v", w.Body.String(), w.Header())
		}
	}
	revoked.Store(true)
	req := httptest.NewRequest(http.MethodGet, "/files/serve?path=clip.mp4", nil).WithContext(ctx)
	req.Header.Set("Range", "bytes=0-1")
	w := httptest.NewRecorder()
	s.FileServeAPI(w, req)
	if w.Code != 403 {
		t.Fatalf("revoked Range returned %d", w.Code)
	}
}

func TestRuntimeUploadAndSymlinkWrite(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	var revoked atomic.Bool
	ctx := runtimeTestContext(t, root, &revoked)
	s := &Server{}
	for _, dir := range []string{"assets/uploads", "alias"} {
		if dir == "alias" {
			if err := os.Symlink(other, filepath.Join(root, "alias")); err != nil {
				t.Fatal(err)
			}
		}
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		f, err := form.CreateFormFile("file", "portrait.png")
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte("image"))
		form.WriteField("path", dir)
		form.Close()
		req := httptest.NewRequest(http.MethodPost, "/files/upload", &body).WithContext(ctx)
		req.Header.Set("Content-Type", form.FormDataContentType())
		w := httptest.NewRecorder()
		s.FileUploadAPI(w, req)
		if dir == "alias" {
			if w.Code < 400 {
				t.Fatal("symlink upload succeeded")
			}
			if _, err := os.Stat(filepath.Join(other, "portrait.png")); !os.IsNotExist(err) {
				t.Fatal("upload escaped root")
			}
		} else if w.Code != 200 {
			t.Fatalf("upload %d: %s", w.Code, w.Body.String())
		}
	}
}

func TestRuntimeBuiltinBashAndBatchDenied(t *testing.T) {
	var revoked atomic.Bool
	root := t.TempDir()
	ctx := runtimeTestContext(t, root, &revoked)
	s := &Server{}
	marker := filepath.Join(root, "executed")
	if _, err := s.dispatchBuiltinTool(ctx, "bash_execute", map[string]any{"command": "touch " + marker}); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("bash error %v", err)
	}
	result, err := s.dispatchBuiltinTool(ctx, "batch_execute", map[string]any{"tool_calls": []any{map[string]any{"name": "bash_execute", "arguments": map[string]any{"command": "touch " + marker}}}})
	if err == nil && !strings.Contains(result, "denied") {
		t.Fatalf("batch did not deny nested bash: %q %v", result, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("restricted shell executed")
	}
	if _, err := s.dispatchBuiltinTool(context.Background(), "file_read", map[string]any{"file_path": "/etc/passwd"}); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("anonymous file tool allowed")
	}
}
