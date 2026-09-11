package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// filesystemProbePrefix names the transient file Check writes. The prefix is
// dotted so a stray probe left behind by a killed process is obviously not
// user media.
const filesystemProbePrefix = ".at-media-probe-"

// filesystemStore writes blobs under one absolute root. Every path operation
// goes through os.Root, which enforces containment while following symlinks
// even under a concurrent rename, exactly like
// internal/service/execution-files.go does for workspace files. A crafted key
// therefore cannot be made to resolve outside the root.
type filesystemStore struct {
	root string
}

func newFilesystem(s service.MediaFilesystemSettings) (*filesystemStore, error) {
	if !filepath.IsAbs(s.Root) {
		return nil, errors.New("filesystem media storage root must be an absolute path")
	}
	return &filesystemStore{root: filepath.Clean(s.Root)}, nil
}

// open returns a rooted handle, creating the root on first use: the
// administrator configures a path in the UI and has no other way to create it.
func (f *filesystemStore) open() (*os.Root, error) {
	if err := os.MkdirAll(f.root, 0o700); err != nil {
		return nil, fmt.Errorf("create media storage root: %w", err)
	}
	root, err := os.OpenRoot(f.root)
	if err != nil {
		return nil, fmt.Errorf("open media storage root: %w", err)
	}
	return root, nil
}

func (f *filesystemStore) Put(_ context.Context, key, _ string, data []byte) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	return filesystemWrite(root, key, data)
}

// filesystemWrite is atomic: the payload lands in a unique temporary file in
// the destination directory and is renamed over the target, so a reader never
// observes a partially written image and a failed write leaves no stub.
func filesystemWrite(root *os.Root, key string, data []byte) error {
	dir := path.Dir(key)
	if dir != "." {
		if err := root.MkdirAll(filepath.FromSlash(dir), 0o700); err != nil {
			return fmt.Errorf("create media directory: %w", err)
		}
	}
	temp := path.Join(dir, filesystemProbePrefix+ulid.Make().String())
	file, err := root.OpenFile(filepath.FromSlash(temp), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create media temporary file: %w", err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		root.Remove(filepath.FromSlash(temp)) //nolint:errcheck // best effort
		return fmt.Errorf("write media object: %w", err)
	}
	if err := root.Rename(filepath.FromSlash(temp), filepath.FromSlash(key)); err != nil {
		root.Remove(filepath.FromSlash(temp)) //nolint:errcheck // best effort
		return fmt.Errorf("commit media object: %w", err)
	}
	return nil
}

func (f *filesystemStore) Get(_ context.Context, key string) (io.ReadCloser, string, error) {
	if err := ValidateKey(key); err != nil {
		return nil, "", err
	}
	root, err := f.open()
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	file, err := root.Open(filepath.FromSlash(key))
	if err != nil {
		return nil, "", fmt.Errorf("open media object: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		if err != nil {
			return nil, "", fmt.Errorf("stat media object: %w", err)
		}
		return nil, "", fmt.Errorf("media object %q is not a regular file", key)
	}
	// The content type is not recoverable from the filesystem; the caller uses
	// the type recorded in the database at upload time.
	return file, "", nil
}

func (f *filesystemStore) Delete(_ context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(filepath.FromSlash(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete media object: %w", err)
	}
	return nil
}

// Check writes, reads back and removes a probe file. Statting the directory
// would not prove writability: the common misconfiguration is a root owned by
// another user or mounted read-only.
func (f *filesystemStore) Check(_ context.Context) error {
	root, err := f.open()
	if err != nil {
		return err
	}
	defer root.Close()
	name := filesystemProbePrefix + ulid.Make().String()
	want := []byte("at-media-probe")
	if err := filesystemWrite(root, name, want); err != nil {
		return err
	}
	defer root.Remove(name) //nolint:errcheck // best effort
	got, err := root.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read media storage probe: %w", err)
	}
	if !bytes.Equal(got, want) {
		return errors.New("media storage probe read back different content")
	}
	return nil
}
