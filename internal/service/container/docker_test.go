package container

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestDockerCreateRejectsFlagImages(t *testing.T) {
	d := newDockerDriver()
	for _, image := range []string{"--privileged", "debian 13", "debian\nx"} {
		if _, err := d.Create(context.Background(), "scope", Config{Enabled: true, Image: image}); err == nil || !strings.Contains(err.Error(), "invalid image") {
			t.Fatalf("image %q must be refused before docker runs, got %v", image, err)
		}
	}
}

func TestDockerConfigLabelTracksConfiguration(t *testing.T) {
	base := Config{Enabled: true, Image: "debian:13.7-slim", KeepAlive: true}
	changed := base
	changed.Memory = "8g"
	if first, again := dockerConfigLabel(base), dockerConfigLabel(base); first != again || first == dockerConfigLabel(changed) {
		t.Fatal("the config label must be stable and change with the configuration")
	}
}

func dockerTestShell(t *testing.T, m *Manager, scope string, cfg Config, script string) string {
	t.Helper()
	stdout, stderr, code, err := m.ExecArgs(context.Background(), scope, cfg, "", nil, "sh", "-c", script)
	if err != nil || code != 0 {
		t.Fatalf("%q failed: code=%d err=%v stderr=%s", script, code, err, stderr)
	}
	return stdout
}

// TestDockerStockImageSandbox runs against the host's Docker daemon with the
// network: a stock image must stay up, the user must be able to install
// packages with every capability dropped, and what they installed must
// survive a stop until the configuration changes. Skipped without Docker.
func TestDockerStockImageSandbox(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if err := exec.Command("docker", "image", "inspect", "debian:13.7-slim").Run(); err != nil {
		t.Skip("docker or debian:13.7-slim not available")
	}
	ctx := context.Background()
	m := New()
	scope := "test-stock-image-sandbox"
	cfg := Config{Enabled: true, Image: "debian:13.7-slim", Network: true, KeepAlive: true, RetainWhenIdle: true, PersistentVolume: true, PidsLimit: 128}
	t.Cleanup(func() { _ = m.RemoveScope(context.Background(), scope) })

	// Nothing is installed by AT: the stock image has no git.
	if out := dockerTestShell(t, m, scope, cfg, "command -v git || echo absent"); !strings.Contains(out, "absent") {
		t.Fatalf("AT must not provision tools, found: %s", out)
	}
	dockerTestShell(t, m, scope, cfg, "echo kept > /workspace/file")
	dockerTestShell(t, m, scope, cfg, "apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends git >/dev/null")

	term, err := m.AttachShell(ctx, scope, cfg, "/workspace", 80, 24)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := term.Resize(100, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if _, err := term.Write([]byte("echo at-$((40+2))\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	var seen strings.Builder
	buf := make([]byte, 4096)
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(seen.String(), "at-42") {
		if time.Now().After(deadline) {
			t.Fatalf("shell output never arrived: %q", seen.String())
		}
		n, err := term.Read(buf)
		seen.Write(buf[:n])
		if err != nil {
			t.Fatalf("read: %v (output %q)", err, seen.String())
		}
	}
	_ = term.Close()

	// Stopping keeps the container, so the install survives.
	if err := m.StopContainer(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if out := dockerTestShell(t, m, scope, cfg, "command -v git; cat /workspace/file"); !strings.Contains(out, "/usr/bin/git") || !strings.Contains(out, "kept") {
		t.Fatalf("a stopped sandbox must resume with its installs and files: %s", out)
	}

	// A configuration change replaces the container; only /workspace remains.
	changed := cfg
	changed.PidsLimit = 129
	if out := dockerTestShell(t, m, scope, changed, "command -v git || echo absent; cat /workspace/file"); !strings.Contains(out, "absent") || !strings.Contains(out, "kept") {
		t.Fatalf("a reconfigured sandbox must start fresh but keep /workspace: %s", out)
	}
}
