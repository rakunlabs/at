package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service/executiontest"
)

func preserveAssetsDir(t *testing.T) {
	t.Helper()
	assetsDirMu.Lock()
	path, err := assetsDirPath, assetsDirErr
	assetsDirMu.Unlock()
	t.Cleanup(func() {
		assetsDirMu.Lock()
		assetsDirPath, assetsDirErr = path, err
		assetsDirMu.Unlock()
	})
}

func TestConfigureAssetsDir(t *testing.T) {
	preserveAssetsDir(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	assetsDirMu.Lock()
	assetsDirPath, assetsDirErr = "", nil
	assetsDirMu.Unlock()
	if got := AssetsDir(); got != filepath.Join(cwd, "data", "assets") {
		t.Fatalf("unconfigured AssetsDir() = %q", got)
	}
	for _, tt := range []struct {
		name, root, want string
	}{
		{"absolute", filepath.Join(cwd, "mounted"), filepath.Join(cwd, "mounted", "assets")},
		{"relative", "workspace", filepath.Join(cwd, "workspace", "assets")},
		{"unset after custom", "", filepath.Join(cwd, "data", "assets")},
		{"blank", " \t", filepath.Join(cwd, "data", "assets")},
		{"reconfigure", "another", filepath.Join(cwd, "another", "assets")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := ConfigureAssetsDir(tt.root); err != nil {
				t.Fatal(err)
			}
			if got := AssetsDir(); got != tt.want || !filepath.IsAbs(got) {
				t.Fatalf("AssetsDir() = %q, want %q", got, tt.want)
			}
			if _, err := os.Stat(tt.want); !os.IsNotExist(err) {
				t.Fatalf("configuration must not create directories: %v", err)
			}
		})
	}
}

func TestEnsureAssetsDirSharedConsumers(t *testing.T) {
	preserveAssetsDir(t)
	root := t.TempDir()
	if err := ConfigureAssetsDir(root); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "assets")
	got, err := EnsureAssetsDirReady()
	if err != nil || got != want {
		t.Fatalf("EnsureAssetsDirReady() = %q, %v", got, err)
	}
	// Explicit compatibility admission selects the same root as legacy media.
	if AssetsDir() != want || EnsureAssetsDir() != want {
		t.Fatal("consumers disagree on the library root")
	}
	out, err := ExecuteBashHandler(executiontest.WithRoot(t, root), `printf '%s' "$AT_ASSETS_DIR"`, nil, nil, 5*time.Second)
	if err != nil || strings.TrimSpace(out) != want {
		t.Fatalf("handler assets = %q, %v; want %q", out, err, want)
	}
	for _, sub := range []string{"", "avatars", "voices", "uploads", "series"} {
		entries, err := os.ReadDir(filepath.Join(want, sub))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".at-startup-probe-") {
				t.Errorf("probe leaked in %q", sub)
			}
		}
	}
}

func TestEnsureAssetsDirCreateFailure(t *testing.T) {
	preserveAssetsDir(t)
	for _, sub := range []string{"", "assets/uploads"} {
		t.Run(sub, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "workspace")
			blocker := filepath.Join(root, sub)
			if err := os.MkdirAll(filepath.Dir(blocker), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(blocker, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := ConfigureAssetsDir(root); err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(root, "assets")
			if got, err := EnsureAssetsDirReady(); err == nil || got != want {
				t.Fatalf("EnsureAssetsDirReady() = %q, %v; want path and error", got, err)
			}
			if EnsureAssetsDir() != want || AssetsDir() != want {
				t.Fatal("failure must not fall back to another path")
			}
			if data, err := os.ReadFile(blocker); err != nil || string(data) != "keep" {
				t.Fatalf("existing file changed: %q, %v", data, err)
			}
		})
	}
}

func TestAssetsDirConcurrentConfiguration(t *testing.T) {
	preserveAssetsDir(t)
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := ConfigureAssetsDir(a); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				for _, path := range []string{a, b} {
					if err := ConfigureAssetsDir(path); err != nil {
						t.Error(err)
					}
					got := AssetsDir()
					if got != filepath.Join(a, "assets") && got != filepath.Join(b, "assets") {
						t.Errorf("unexpected path %q", got)
					}
					if _, err := EnsureAssetsDirReady(); err != nil {
						t.Error(err)
					}
				}
			}
		})
	}
	wg.Wait()
}
