package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/container"
)

func TestDeveloperToolProject(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		fallback string
		want     string
		bad      bool
	}{
		{name: "conversation project", args: map[string]any{}, fallback: "vault", want: "vault"},
		{name: "explicit wins", args: map[string]any{"project": "notes"}, fallback: "vault", want: "notes"},
		{name: "explicit empty is the space root", args: map[string]any{"project": ""}, fallback: "vault", want: ""},
		{name: "nested folder refused", args: map[string]any{"project": "a/b"}, bad: true},
		{name: "escape refused", args: map[string]any{"project": "../etc"}, bad: true},
		{name: "absolute refused", args: map[string]any{}, fallback: "/etc", bad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := developerToolProject(tt.args, tt.fallback)
			if tt.bad {
				if err == nil {
					t.Fatalf("accepted %q", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestDeveloperToolDefinitions(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range developerToolDefs {
		if !strings.HasPrefix(tool.Name, "dev_") {
			t.Errorf("%s: Developer Space tools carry the dev_ prefix so they never shadow host built-ins", tool.Name)
		}
		if isKnownBuiltinTool(tool.Name) {
			t.Errorf("%s collides with a built-in tool", tool.Name)
		}
		if seen[tool.Name] {
			t.Errorf("duplicate %s", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Description == "" || tool.InputSchema == nil {
			t.Errorf("%s has no description or schema", tool.Name)
		}
	}
	for name := range developerToolShell {
		if !seen[name] {
			t.Errorf("confirmation list names unknown tool %s", name)
		}
	}
	for name := range developerToolEdits {
		if !seen[name] {
			t.Errorf("edit list names unknown tool %s", name)
		}
	}
	if seen["dev_git_push"] {
		t.Error("pushing must stay a confirmed action on the Developer Spaces page")
	}
}

func TestDeveloperToolResultText(t *testing.T) {
	if got := developerToolResultText(developerToolResult{Error: "boom", Output: "partial"}); got != "Error: boom\npartial" {
		t.Fatalf("error text = %q", got)
	}
	if got := developerToolResultText(developerToolResult{}); got != "(no output)" {
		t.Fatalf("empty text = %q", got)
	}
}

func TestDeveloperToolCallRequiresAccount(t *testing.T) {
	s := &Server{}
	body := strings.NewReader(`{"name":"dev_projects","arguments":{}}`)
	w := httptest.NewRecorder()
	s.DeveloperToolCallAPI(w, httptest.NewRequest(http.MethodPost, "/api/v1/developer-space/tools/call", body))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no store: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.DeveloperToolCallAPI(w, httptest.NewRequest(http.MethodPost, "/api/v1/developer-space/tools/call", strings.NewReader(`{"name":"bash_execute"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("host built-in accepted: %d %s", w.Code, w.Body.String())
	}
}

// TestDeveloperCommitUsesAccountIdentityInDocker exercises the knowledge-base
// flow against a real container: edit a file with the tools, see its diff, and
// commit it as the run's account when the repository names nobody.
func TestDeveloperCommitUsesAccountIdentityInDocker(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	const image = "alpine:3.22"
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Skip("docker or " + image + " not available")
	}
	ctx := context.Background()
	s := &Server{containerManager: container.New()}
	h := &developerRuntimeHandle{
		space: &service.DeveloperSpace{},
		scope: "test-developer-commit",
		cfg:   container.Config{Enabled: true, Image: image, Network: true, KeepAlive: true, PersistentVolume: true, PidsLimit: 128, CapAdd: container.PackageManagerCapabilities},
	}
	t.Cleanup(func() { _ = s.containerManager.RemoveScope(context.Background(), h.scope) })
	if _, _, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, "/workspace", nil, "sh", "-c",
		"apk add --no-cache -q git && mkdir -p /workspace/vault && cd /workspace/vault && git init -q && printf '# Notes\\n' > a.md && git add . && git -c user.name=seed -c user.email=seed@x commit -qm init"); err != nil || code != 0 {
		t.Skipf("cannot prepare the container (network?): code %d %v", code, err)
	}

	if _, err := s.developerEdit(ctx, h, "vault", map[string]any{"path": "a.md", "old_text": "# Notes", "new_text": "# Notes\n\nDecided: ship it."}); err != nil {
		t.Fatal(err)
	}
	diff, truncated := s.developerChangedDiff(ctx, h, "vault", []string{"a.md"})
	if truncated || !strings.Contains(diff, "+Decided: ship it.") {
		t.Fatalf("diff = %q", diff)
	}

	// The account identity is what developerCommitIdentity would return for a
	// bound run; it is injected only when the repository names nobody.
	env := map[string]string{"GIT_AUTHOR_NAME": "Ada Lovelace", "GIT_AUTHOR_EMAIL": "ada@example.com", "GIT_COMMITTER_NAME": "Ada Lovelace", "GIT_COMMITTER_EMAIL": "ada@example.com"}
	if out, stderr, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, "/workspace/vault", env, "sh", "-c", "git add a.md && git commit -qm update && git log -1 --format='%an <%ae>'"); err != nil || code != 0 || strings.TrimSpace(out) != "Ada Lovelace <ada@example.com>" {
		t.Fatalf("commit author = %q %s %v", out, stderr, err)
	}
	if got := s.developerCommitIdentity(ctx, h, "/workspace/vault"); got != nil {
		// No account is bound in this test, so nothing can be claimed.
		t.Fatalf("identity without a bound account: %v", got)
	}
	raw, _ := json.Marshal(developerToolResult{Project: "vault", Changed: []string{"a.md"}, Diff: diff})
	if !strings.Contains(string(raw), `"changed":["a.md"]`) {
		t.Fatalf("result json = %s", raw)
	}
}
