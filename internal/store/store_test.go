package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureParentDir(t *testing.T) {
	base := t.TempDir()

	tests := []struct {
		name       string
		datasource string
		wantDir    string // path, relative to `base`, expected to exist afterwards
	}{
		{"nested path creates dir", filepath.Join(base, "data", "at.db"), "data"},
		{"deep nested", filepath.Join(base, "a", "b", "c", "db.sqlite"), filepath.Join("a", "b", "c")},
		{"file uri prefix", "file:" + filepath.Join(base, "uri", "at.db"), "uri"},
		{"file uri with query", "file:" + filepath.Join(base, "query", "at.db") + "?cache=shared", "query"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ensureParentDir(tt.datasource); err != nil {
				t.Fatalf("ensureParentDir: %v", err)
			}
			full := filepath.Join(base, tt.wantDir)
			info, err := os.Stat(full)
			if err != nil {
				t.Fatalf("stat %q: %v", full, err)
			}
			if !info.IsDir() {
				t.Fatalf("%q is not a directory", full)
			}
		})
	}
}

func TestEnsureParentDir_NoOps(t *testing.T) {
	// These must not error and must not create anything.
	cases := []string{"", ":memory:", "at.db"}
	for _, ds := range cases {
		if err := ensureParentDir(ds); err != nil {
			t.Errorf("ensureParentDir(%q) returned error: %v", ds, err)
		}
	}
}
