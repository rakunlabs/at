package sandboxruntime

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallIsAtomicAndRooted(t *testing.T) {
	root := t.TempDir()
	if err := Install(root, ".ssh/id_ed25519", 0o600, strings.NewReader("secret")); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, ".ssh/id_ed25519")
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../outside", "/absolute", "escape/key"} {
		if err := Install(root, rel, 0o600, strings.NewReader("secret")); err == nil {
			t.Fatalf("escape admitted: %s", rel)
		}
	}
	if err := Install(root, ".ssh/id_ed25519", 0o600, io.LimitReader(zeroReader{}, MaxInstallBytes+1)); err == nil {
		t.Fatal("oversized file admitted")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "secret" {
		t.Fatal("failed upload replaced the existing file")
	}
	if err := Install(root, "mode", 0o4777, strings.NewReader("secret")); err == nil {
		t.Fatal("setuid mode admitted")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestPlatformAndMalformedCommands(t *testing.T) {
	var out bytes.Buffer
	if err := Run([]string{"platform"}, nil, &out); err != nil || !strings.Contains(out.String(), "/") {
		t.Fatalf("platform: %q %v", out.String(), err)
	}
	for _, args := range [][]string{nil, {"unknown"}, {"install"}, {"exec", "", "{}"}, {"exec", "", "{bad}", "true"}, {"run", "bad", "", "{}", "true"}} {
		if err := Run(args, nil, io.Discard); err == nil {
			t.Fatalf("invalid command admitted: %v", args)
		}
	}
}
