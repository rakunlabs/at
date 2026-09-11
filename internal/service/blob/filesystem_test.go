package blob

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func newFilesystemStore(t *testing.T, root string) *filesystemStore {
	t.Helper()
	store, err := newFilesystem(service.MediaFilesystemSettings{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func readStore(t *testing.T, store Store, key string) string {
	t.Helper()
	reader, _, err := store.Get(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFilesystemRoundTrip(t *testing.T) {
	root := t.TempDir()
	store := newFilesystemStore(t, root)
	ctx := t.Context()

	if err := store.Put(ctx, "owner-1/image.png", "image/png", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if got := readStore(t, store, "owner-1/image.png"); got != "first" {
		t.Fatalf("content %q", got)
	}
	// The content type is not recoverable from the filesystem; the database
	// record is authoritative.
	if _, contentType, err := store.Get(ctx, "owner-1/image.png"); err != nil || contentType != "" {
		t.Fatalf("content type %q %v", contentType, err)
	}

	// An overwrite replaces the payload atomically and leaves no temporary
	// file behind in the destination directory.
	if err := store.Put(ctx, "owner-1/image.png", "image/png", []byte("second")); err != nil {
		t.Fatal(err)
	}
	if got := readStore(t, store, "owner-1/image.png"); got != "second" {
		t.Fatalf("content after replace %q", got)
	}
	entries, err := os.ReadDir(filepath.Join(root, "owner-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "image.png" {
		t.Fatalf("directory entries %+v", entries)
	}

	if err := store.Delete(ctx, "owner-1/image.png"); err != nil {
		t.Fatal(err)
	}
	// Delete is idempotent so a retry after a partially applied deletion
	// converges instead of failing forever.
	if err := store.Delete(ctx, "owner-1/image.png"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Get(ctx, "owner-1/image.png"); err == nil {
		t.Fatal("expected error reading a deleted object")
	}
}

// A key is attacker-influenced data. None of these may write, read or delete
// anything outside the configured root.
func TestFilesystemContainment(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "media")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newFilesystemStore(t, root)
	ctx := t.Context()
	// A symlink inside the root pointing out of it must not become an escape
	// hatch: os.Root refuses to follow it out.
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{
		"../outside/evil.png",
		"../../outside/evil.png",
		"owner/../../outside/evil.png",
		"/etc/at-media-evil.png",
		"owner\\..\\evil.png",
		"escape/evil.png",
		"",
		".",
	} {
		t.Run("put "+key, func(t *testing.T) {
			if err := store.Put(ctx, key, "image/png", []byte("evil")); err == nil {
				t.Fatalf("Put(%q) succeeded", key)
			}
			if _, _, err := store.Get(ctx, key); err == nil {
				t.Fatalf("Get(%q) succeeded", key)
			}
			if err := store.Delete(ctx, key); err == nil {
				t.Fatalf("Delete(%q) succeeded", key)
			}
		})
	}

	if _, err := os.Stat(filepath.Join(outside, "evil.png")); !os.IsNotExist(err) {
		t.Fatalf("escaped the root: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(outside, "secret.txt")); err != nil || string(data) != "secret" {
		t.Fatalf("outside file changed: %q %v", data, err)
	}
}

func TestFilesystemCheck(t *testing.T) {
	root := filepath.Join(t.TempDir(), "created", "on", "demand")
	store := newFilesystemStore(t, root)
	if err := store.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	// The probe must not survive the check.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("probe left behind: %+v", entries)
	}

	// A root that cannot exist (its parent is a regular file) must surface as
	// a configuration error rather than a silent success.
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := newFilesystemStore(t, filepath.Join(blocked, "media"))
	err = broken.Check(t.Context())
	if err == nil || !strings.Contains(err.Error(), "create media storage root") {
		t.Fatalf("expected a root creation error, got %v", err)
	}
}

func TestFilesystemRequiresAbsoluteRoot(t *testing.T) {
	if _, err := newFilesystem(service.MediaFilesystemSettings{Root: "data/media"}); err == nil {
		t.Fatal("expected a relative root to be rejected")
	}
}
