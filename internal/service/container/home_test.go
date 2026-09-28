package container

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestValidHomePath(t *testing.T) {
	tests := []struct {
		path string
		ok   bool
	}{
		{"/root", true},
		{"/home/dev", true},
		{"/opt/home", true},
		{"", false},
		{"/", false},
		{"root", false},
		{"/root/", false},
		{"/home/../etc", false},
		{"/workspace", false},
		{"/workspace/home", false},
		{"/etc", false},
		{"/usr/local/home", false},
		{"/proc/1", false},
		{"/tmp", false},
		{"/home/a:b", false},
		{"/home/a,b", false},
		{"/home/a\nb", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := ValidHomePath(tt.path); got != tt.ok {
				t.Fatalf("ValidHomePath(%q) = %v, want %v", tt.path, got, tt.ok)
			}
		})
	}
}

func TestHomeChangesConfigLabel(t *testing.T) {
	base := Config{Enabled: true, Image: "debian"}
	withHome := base
	withHome.HomeScope, withHome.HomePath = "developer-home:u", "/root"
	moved := withHome
	moved.HomePath = "/home/dev"
	if dockerConfigLabel(base) == dockerConfigLabel(withHome) || dockerConfigLabel(withHome) == dockerConfigLabel(moved) {
		t.Fatal("enabling or moving the home must recreate the container, since mounts cannot change on a running one")
	}
	if dockerHomeVolumeName("developer-home:u") == dockerVolumeName("developer-home:u") {
		t.Fatal("home and workspace volumes must not share a name")
	}
}

// TestDockerPersistentHome runs against the host's Docker daemon: the home is
// $HOME, survives a container rebuild, is shared by one owner's scopes, keeps
// uploaded file modes, and is gone after RemoveHome. Skipped without Docker.
func TestDockerPersistentHome(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if err := exec.Command("docker", "image", "inspect", "debian:13.7-slim").Run(); err != nil {
		t.Skip("docker or debian:13.7-slim not available")
	}
	ctx := context.Background()
	m := New()
	home := "test-persistent-home"
	a, b := "test-home-scope-a", "test-home-scope-b"
	cfg := Config{Enabled: true, Image: "debian:13.7-slim", KeepAlive: true, RetainWhenIdle: true, PersistentVolume: true, PidsLimit: 64, HomeScope: home, HomePath: "/root"}
	t.Cleanup(func() {
		_ = m.RemoveHome(context.Background(), home, a, b)
		_ = m.RemoveScope(context.Background(), a)
		_ = m.RemoveScope(context.Background(), b)
	})

	if out := dockerTestShell(t, m, a, cfg, `echo "$HOME"`); strings.TrimSpace(out) != "/root" {
		t.Fatalf("HOME must be the mount point, got %q", out)
	}
	if err := m.CopyFile(ctx, a, cfg, "/root/.ssh/id_test", []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := dockerTestShell(t, m, a, cfg, "stat -c %a /root/.ssh/id_test; cat /root/.ssh/id_test"); !strings.Contains(out, "600") || !strings.Contains(out, "secret") {
		t.Fatalf("uploaded file must keep its mode and content: %s", out)
	}

	// A rebuild (configuration change) keeps the home.
	changed := cfg
	changed.PidsLimit = 65
	if out := dockerTestShell(t, m, a, changed, "cat /root/.ssh/id_test"); !strings.Contains(out, "secret") {
		t.Fatalf("the home must survive a container rebuild: %s", out)
	}
	// Another scope of the same owner sees the same home, at its own path.
	moved := cfg
	moved.HomePath = "/home/dev"
	if out := dockerTestShell(t, m, b, moved, `echo "$HOME"; cat "$HOME/.ssh/id_test"`); !strings.Contains(out, "/home/dev") || !strings.Contains(out, "secret") {
		t.Fatalf("one owner's scopes must share the home: %s", out)
	}

	if err := m.RemoveHome(ctx, home, a, b); err != nil {
		t.Fatal(err)
	}
	if out := dockerTestShell(t, m, a, changed, "cat /root/.ssh/id_test 2>/dev/null || echo gone"); !strings.Contains(out, "gone") {
		t.Fatalf("RemoveHome must delete the home: %s", out)
	}
}
