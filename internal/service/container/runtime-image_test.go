package container

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestRuntimeImageTagIsStablePerBase(t *testing.T) {
	a, b := RuntimeImageTag("debian:13.7-slim"), RuntimeImageTag("debian:13.7-slim")
	if a != b || !strings.HasPrefix(a, "at-developer-runtime:") {
		t.Fatalf("tag is not stable: %q vs %q", a, b)
	}
	if RuntimeImageTag("alpine:3") == a {
		t.Fatal("different bases must not share a derived image")
	}
}

func TestResolveImageRejectsInjection(t *testing.T) {
	m := New()
	for _, image := range []string{"debian\nRUN id", "debian AS x", "--privileged"} {
		if _, err := m.resolveImage(context.Background(), Config{Image: image, ProvisionTools: true}); err == nil {
			t.Fatalf("image %q was accepted", image)
		}
	}
	if got, err := m.resolveImage(context.Background(), Config{Image: "x:1"}); err != nil || got != "x:1" {
		t.Fatalf("unprovisioned image must pass through, got %q, %v", got, err)
	}
}

// TestProvisionedDebianRuntime needs a Docker daemon and network access; it
// is skipped otherwise.
func TestProvisionedDebianRuntime(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if err := exec.Command("docker", "image", "inspect", "debian:13.7-slim").Run(); err != nil {
		t.Skip("docker or debian:13.7-slim not available")
	}
	ctx := context.Background()
	m := New()
	scope := "test-provisioned-runtime"
	cfg := Config{Enabled: true, Image: "debian:13.7-slim", ProvisionTools: true, PidsLimit: 64}
	t.Cleanup(func() { _ = m.RemoveScope(context.Background(), scope) })
	stdout, stderr, code, err := m.ExecArgs(ctx, scope, cfg, "", nil, "sh", "-c", "for c in bash git python3 timeout; do command -v \"$c\"; done")
	if err != nil || code != 0 {
		t.Fatalf("exec failed: code=%d err=%v stderr=%s", code, err, stderr)
	}
	for _, tool := range []string{"bash", "git", "python3", "timeout"} {
		if !strings.Contains(stdout, tool) {
			t.Fatalf("%s missing from provisioned image: %s", tool, stdout)
		}
	}
}
