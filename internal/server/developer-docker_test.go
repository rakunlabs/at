package server

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service/container"
)

// TestDeveloperBashAndUndoInDocker runs the bash tool and the undo snapshots
// against a real container: shell syntax works, timeouts kill the command,
// and restoring a snapshot brings back edited and deleted files and removes
// created ones, without touching the project's own Git history.
func TestDeveloperBashAndUndoInDocker(t *testing.T) {
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
		scope: "test-developer-bash-undo",
		cfg:   container.Config{Enabled: true, Image: image, Network: true, KeepAlive: true, PersistentVolume: true, PidsLimit: 128, CapAdd: container.PackageManagerCapabilities},
	}
	t.Cleanup(func() { _ = s.containerManager.RemoveScope(context.Background(), h.scope) })
	bash := func(command string, timeoutMs int) (string, error) {
		args := map[string]any{"command": command}
		if timeoutMs > 0 {
			args["timeout"] = float64(timeoutMs)
		}
		return s.runDeveloperBash(ctx, h, "proj", args)
	}
	if _, _, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, "/workspace", nil, "sh", "-c", "apk add --no-cache -q git && mkdir -p /workspace/proj"); err != nil || code != 0 {
		t.Skipf("cannot prepare the container (network?): code %d %v", code, err)
	}

	// Without bash in the image the tool falls back to sh; pipes, && and
	// redirection are shell syntax, and stderr is merged in order.
	out, err := bash("echo one > a.txt && cat a.txt | tr a-z A-Z; echo err >&2; pwd", 0)
	if err != nil || out != "ONE\nerr\n/workspace/proj\n" {
		t.Fatalf("bash output = %q, %v", out, err)
	}
	if out, err := bash("echo partial; exit 3", 0); err == nil || !strings.Contains(err.Error(), "code 3") || !strings.Contains(out, "partial") {
		t.Fatalf("exit code not reported: %q %v", out, err)
	}
	if _, err := bash("sleep 30", 1000); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("timeout not enforced: %v", err)
	}

	// The project has its own repository; undo must not touch it.
	if _, err := bash("git init -q && git -c user.email=a@b -c user.name=a commit -q --allow-empty -m init && printf 'node_modules/\\n' > .gitignore && mkdir -p node_modules && echo dep > node_modules/x && echo keep > kept.txt && echo gone > deleted.txt", 0); err != nil {
		t.Fatal(err)
	}
	before := s.takeDeveloperSnapshot(ctx, h, "proj")
	if !developerTreeHash.MatchString(before) {
		t.Fatalf("snapshot = %q", before)
	}
	if _, err := bash("echo changed > kept.txt && rm deleted.txt && echo new > created.txt && echo dep2 > node_modules/x", 0); err != nil {
		t.Fatal(err)
	}
	changed, err := s.restoreDeveloperSnapshot(ctx, h, "proj", before)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(changed, ",") == "" {
		t.Fatal("restore reported no changed files")
	}
	out, _ = bash("cat kept.txt deleted.txt; ls created.txt 2>/dev/null || echo no-created; cat node_modules/x; git log --oneline | wc -l; git status --porcelain", 0)
	want := "keep\ngone\nno-created\ndep2\n1\n?? .gitignore\n?? a.txt\n?? deleted.txt\n?? kept.txt\n"
	if out != want {
		t.Fatalf("after undo:\n%s\nwant:\n%s", out, want)
	}
	if _, err := s.restoreDeveloperSnapshot(ctx, h, "proj", "not-a-hash"); err == nil {
		t.Fatal("an invalid snapshot was accepted")
	}
}
