//go:build linux

package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"
	"golang.org/x/sys/unix"
)

// OpenExecutionFile additionally forbids symlinks INSIDE the workspace, since
// an alias could otherwise bypass a path-pattern grant even with os.Root's
// escape protection. openat2 resolves every component atomically beneath the
// held root descriptor. Unsupported kernels fail closed, never fall back.
func OpenExecutionFile(root *os.Root, name string, flags int, mode os.FileMode) (*os.File, error) {
	dir, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open root descriptor: %w", err)
	}
	defer dir.Close()
	var creationMode uint64
	if flags&os.O_CREATE != 0 {
		creationMode = uint64(mode.Perm())
	}
	fd, err := unix.Openat2(int(dir.Fd()), name, &unix.OpenHow{
		Flags: uint64(flags | unix.O_CLOEXEC | unix.O_NONBLOCK), Mode: creationMode,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, fmt.Errorf("open authorized file: %w", err)
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat authorized file: %w", err)
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		f.Close()
		return nil, ErrExecutionDenied
	}
	return f, nil
}

// ReplaceExecutionFile atomically replaces a file through one symlink-free
// parent descriptor. Rename replaces the final directory entry, never follows
// it, so an attacker cannot redirect the write between validation and commit.
func ReplaceExecutionFile(root *os.Root, name string, src io.Reader) (int64, error) {
	if !filepath.IsLocal(name) || filepath.Clean(name) == "." {
		return 0, ErrExecutionDenied
	}
	dir, err := OpenExecutionFile(root, filepath.Dir(name), os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	temp := ".at-upload-" + ulid.Make().String()
	fd, err := unix.Openat(int(dir.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return 0, fmt.Errorf("create rooted upload: %w", err)
	}
	defer unix.Unlinkat(int(dir.Fd()), temp, 0)
	f := os.NewFile(uintptr(fd), temp)
	n, err := io.Copy(f, src)
	closeErr := f.Close()
	if err != nil {
		return n, err
	}
	if closeErr != nil {
		return n, closeErr
	}
	if err := unix.Renameat(int(dir.Fd()), temp, int(dir.Fd()), filepath.Base(name)); err != nil {
		return n, fmt.Errorf("commit rooted upload: %w", err)
	}
	return n, nil
}

func MkdirExecutionAll(root *os.Root, name string, mode os.FileMode) error {
	if !filepath.IsLocal(name) {
		return ErrExecutionDenied
	}
	dir, err := OpenExecutionFile(root, ".", os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer func() { dir.Close() }()
	for _, part := range strings.Split(filepath.Clean(name), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		err := unix.Mkdirat(int(dir.Fd()), part, uint32(mode.Perm()))
		if err != nil && err != unix.EEXIST {
			return fmt.Errorf("create rooted directory: %w", err)
		}
		fd, err := unix.Openat(int(dir.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return fmt.Errorf("open rooted directory: %w", err)
		}
		dir.Close()
		dir = os.NewFile(uintptr(fd), part)
	}
	return nil
}

func RemoveExecutionFile(root *os.Root, name string) error {
	if !filepath.IsLocal(name) || filepath.Clean(name) == "." {
		return ErrExecutionDenied
	}
	dir, err := OpenExecutionFile(root, filepath.Dir(name), os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer dir.Close()
	var info unix.Stat_t
	if err := unix.Fstatat(int(dir.Fd()), filepath.Base(name), &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("stat rooted deletion: %w", err)
	}
	flags := 0
	if info.Mode&unix.S_IFMT == unix.S_IFDIR {
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(int(dir.Fd()), filepath.Base(name), flags); err != nil {
		return fmt.Errorf("remove rooted file: %w", err)
	}
	return nil
}

func RemoveExecutionTree(ctx context.Context, root *os.Root, name string) error {
	if !filepath.IsLocal(name) || filepath.Clean(name) == "." {
		return ErrExecutionDenied
	}
	dir, err := OpenExecutionFile(root, filepath.Dir(name), os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer dir.Close()
	return removeExecutionTreeAt(ctx, int(dir.Fd()), filepath.Base(name))
}
func removeExecutionTreeAt(ctx context.Context, parent int, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == unix.ENOTDIR || err == unix.ELOOP {
		return unix.Unlinkat(parent, name, 0)
	}
	if err == unix.ENOENT {
		return nil
	}
	if err != nil {
		return err
	}
	dir := os.NewFile(uintptr(fd), name)
	defer dir.Close()
	for {
		entries, err := dir.ReadDir(128)
		if err != nil && err != io.EOF {
			return err
		}
		for _, entry := range entries {
			if err := removeExecutionTreeAt(ctx, fd, entry.Name()); err != nil {
				return err
			}
		}
		if err == io.EOF {
			break
		}
	}
	return unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
}
