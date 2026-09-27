package devfs_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service/container"
)

// TestHelperInStockImage installs a freshly built helper into a stock debian
// container — which has no python3 — through the real Docker driver, and runs
// file operations with it, including a symlink escape. Skipped without Docker.
func TestHelperInStockImage(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if err := exec.Command("docker", "image", "inspect", "debian:13.7-slim").Run(); err != nil {
		t.Skip("docker or debian:13.7-slim not available")
	}
	helper := filepath.Join(t.TempDir(), "at-devfs")
	build := exec.Command("go", "build", "-trimpath", "-o", helper, "github.com/rakunlabs/at/cmd/at-devfs")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, out)
	}
	data, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	m := container.New()
	scope := "test-devfs-helper"
	cfg := container.Config{Enabled: true, Image: "debian:13.7-slim", KeepAlive: true, PersistentVolume: true, PidsLimit: 64}
	t.Cleanup(func() { _ = m.RemoveScope(context.Background(), scope) })

	sh := func(script string) string {
		t.Helper()
		stdout, stderr, code, err := m.ExecArgs(ctx, scope, cfg, "", nil, "sh", "-c", script)
		if err != nil || code != 0 {
			t.Fatalf("%q: code=%d err=%v stderr=%s", script, code, err, stderr)
		}
		return stdout
	}
	if out := sh("command -v python3 || echo absent"); !strings.Contains(out, "absent") {
		t.Fatalf("the stock image is expected to lack python3: %s", out)
	}
	sh("mkdir -p /workspace/proj/src && printf 'hi\\n' > /workspace/proj/src/a.txt && ln -s /etc /workspace/proj/etc")

	dest := "/usr/local/bin/at-devfs"
	ok, err := m.EnsureFile(ctx, scope, cfg, dest, 0o755, func(platform string) []byte {
		if platform != "linux/"+runtime.GOARCH {
			t.Errorf("unexpected platform %q", platform)
		}
		return data
	})
	if err != nil || !ok {
		t.Fatalf("install helper: %v, %v", ok, err)
	}

	call := func(stdin string, argv ...string) (int, map[string]any) {
		t.Helper()
		var in *strings.Reader
		if stdin != "" {
			in = strings.NewReader(stdin)
		}
		var stdout, stderr string
		var code int
		if in != nil {
			stdout, stderr, code, err = m.ExecArgsInput(ctx, scope, cfg, "/workspace", nil, in, dest, argv...)
		} else {
			stdout, stderr, code, err = m.ExecArgs(ctx, scope, cfg, "/workspace", nil, dest, argv...)
		}
		if err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		var out map[string]any
		if jerr := json.Unmarshal([]byte(stdout), &out); jerr != nil {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", argv, code, stdout, stderr)
		}
		return code, out
	}

	if code, out := call("", "list", "proj", ""); code != 0 || len(out["entries"].([]any)) != 2 {
		t.Fatalf("list: %v", out)
	}
	if code, out := call("", "read", "proj", "src/a.txt"); code != 0 || out["content"] != "hi\n" {
		t.Fatalf("read: %v", out)
	}
	if code, out := call("written\n", "write", "proj", "src/b.txt", ""); code != 0 {
		t.Fatalf("write: %v", out)
	}
	if out := sh("cat /workspace/proj/src/b.txt"); out != "written\n" {
		t.Fatalf("written content: %q", out)
	}
	if code, out := call("", "read", "proj", "etc/hostname"); code != 3 || !strings.HasPrefix(out["error"].(string), "path escapes the project") {
		t.Fatalf("a symlink out of the project must be refused: %v", out)
	}

	// The helper lives outside /workspace, so it is not part of the user's files.
	if out := sh("ls -A /workspace"); strings.TrimSpace(out) != "proj" {
		t.Fatalf("/workspace must hold only user files: %q", out)
	}
}
