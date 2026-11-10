package server

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service/container"
)

// TestDeveloperBashInDocker runs the bash tool against a real container:
// shell syntax works and timeouts kill the command.
func TestDeveloperBashInDocker(t *testing.T) {
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
		scope: "test-developer-bash",
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
	if _, _, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, "/workspace", nil, "sh", "-c", "mkdir -p /workspace/proj"); err != nil || code != 0 {
		t.Skipf("cannot prepare the container: code %d %v", code, err)
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
}

// Patch, background jobs and formatting against a real container (alpine, no
// bash, so the sh fallbacks are exercised).
func TestDeveloperPatchJobsAndFormatInDocker(t *testing.T) {
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
		scope: "test-developer-patch-jobs",
		cfg:   container.Config{Enabled: true, Image: image, Network: true, KeepAlive: true, PersistentVolume: true, PidsLimit: 128, CapAdd: container.PackageManagerCapabilities},
	}
	t.Cleanup(func() { _ = s.containerManager.RemoveScope(context.Background(), h.scope) })
	sh := func(command string) string {
		out, err := s.runDeveloperBash(ctx, h, "proj", map[string]any{"command": command})
		if err != nil {
			t.Fatalf("%s: %v (%s)", command, err, out)
		}
		return out
	}
	if _, _, code, err := s.containerManager.ExecArgs(ctx, h.scope, h.cfg, "/workspace", nil, "sh", "-c", "mkdir -p /workspace/proj/sub && printf 'a\\nb\\nc\\n' > /workspace/proj/x.txt && echo bye > /workspace/proj/old.txt"); err != nil || code != 0 {
		t.Skipf("cannot prepare the container: code %d %v", code, err)
	}

	// A failing hunk leaves every file untouched.
	if _, _, err := s.applyDeveloperPatch(ctx, h, "proj", "*** Begin Patch\n*** Add File: new.txt\n+n\n*** Update File: x.txt\n-zzz\n+y\n*** End Patch"); err == nil {
		t.Fatal("expected the bad hunk to fail")
	}
	if out := sh("ls new.txt 2>/dev/null || echo absent"); out != "absent\n" {
		t.Fatalf("partial patch applied: %q", out)
	}
	summary, changed, err := s.applyDeveloperPatch(ctx, h, "proj", "*** Begin Patch\n*** Add File: new.txt\n+n\n*** Update File: x.txt\n*** Move to: moved/x.txt\n a\n-b\n+B\n*** Delete File: old.txt\n*** End Patch")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "R x.txt -> moved/x.txt") || strings.Join(changed, ",") != "new.txt,moved/x.txt" {
		t.Fatalf("summary=%q changed=%v", summary, changed)
	}
	if out := sh("cat new.txt moved/x.txt; ls x.txt old.txt 2>/dev/null || echo gone"); out != "n\na\nB\nc\ngone\n" {
		t.Fatalf("after patch: %q", out)
	}

	// Background job: starts, is listed, its output is readable, it finishes.
	started, err := s.startDeveloperBackgroundJob(ctx, h, "proj", "echo started; sleep 1; echo done")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.SplitN(strings.TrimPrefix(started, "Started background job "), ".", 2)[0]
	if !developerJobIDPattern.MatchString(id) {
		t.Fatalf("job id %q from %q", id, started)
	}
	deadline := time.Now().Add(15 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out, err = s.developerJobOutput(ctx, h, map[string]any{"job_id": id})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "finished") {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !strings.Contains(out, "finished") || !strings.Contains(out, "started\ndone\n[exit 0]") {
		t.Fatalf("job output: %q", out)
	}
	if list, _ := s.developerJobOutput(ctx, h, map[string]any{}); !strings.Contains(list, id) {
		t.Fatalf("job list: %q", list)
	}
	long, err := s.startDeveloperBackgroundJob(ctx, h, "proj", "sleep 60")
	if err != nil {
		t.Fatal(err)
	}
	longID := strings.SplitN(strings.TrimPrefix(long, "Started background job "), ".", 2)[0]
	if stopped, err := s.killDeveloperJob(ctx, h, map[string]any{"job_id": longID}); err != nil || !strings.Contains(stopped, "stopped") {
		t.Fatalf("kill: %q %v", stopped, err)
	}

	// Formatting: no formatter installed means nothing is rewritten.
	if got := s.formatDeveloperFiles(ctx, h, "proj", []string{"new.txt", "main.go"}); len(got) != 0 {
		t.Fatalf("formatted without formatters: %v", got)
	}
	// A fake gofmt on PATH proves the hook runs and reports rewrites.
	sh(`printf '#!/bin/sh\nshift\necho formatted > "$1"\n' > /usr/local/bin/gofmt && chmod +x /usr/local/bin/gofmt && echo 'package x' > main.go`)
	if got := s.formatDeveloperFiles(ctx, h, "proj", []string{"main.go"}); len(got) != 1 || got[0] != "main.go" {
		t.Fatalf("formatted = %v", got)
	}
	if out := sh("cat main.go"); out != "formatted\n" {
		t.Fatalf("main.go = %q", out)
	}
}
