package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateGitDeployKeyProducesUsableOpenSSHIdentity(t *testing.T) {
	privateKey, publicKey, err := generateGitDeployKey("test key")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(publicKey, "ssh-ed25519 ") || !strings.HasSuffix(publicKey, " at-test-key") {
		t.Fatalf("public key = %q", publicKey)
	}
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(privateKey), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("ssh-keygen", "-y", "-f", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen rejected generated key: %s: %v", out, err)
	}
	if !strings.HasPrefix(string(out), "ssh-ed25519 ") {
		t.Fatalf("derived public key = %q", out)
	}
}

func TestGitRepositoryEndpoint(t *testing.T) {
	tests := map[string]struct {
		host string
		port int
	}{
		"git@gitlab.com:team/skills.git":     {host: "gitlab.com", port: 22},
		"ssh://git@git.example.com:2222/a/b": {host: "git.example.com", port: 2222},
	}
	for raw, want := range tests {
		host, port, err := gitRepositoryEndpoint(raw)
		if err != nil || host != want.host || port != want.port {
			t.Errorf("gitRepositoryEndpoint(%q) = %q/%d, %v; want %q/%d", raw, host, port, err, want.host, want.port)
		}
	}
	if _, _, err := gitRepositoryEndpoint("https://gitlab.com/team/skills.git"); err == nil {
		t.Fatal("expected HTTPS URL rejection for SSH credential")
	}
}

func TestValidateGitHost(t *testing.T) {
	if host, port, err := validateGitHost("GitLab.COM", 0); err != nil || host != "gitlab.com" || port != 22 {
		t.Fatalf("validateGitHost = %q/%d, %v", host, port, err)
	}
	for _, bad := range []string{"", "bad host", "-bad.example"} {
		if _, _, err := validateGitHost(bad, 22); err == nil {
			t.Errorf("validateGitHost(%q) succeeded", bad)
		}
	}
}
