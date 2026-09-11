//go:build !linux

package service

import (
	"context"
	"fmt"
	"io"
	"os"
)

func OpenExecutionFile(root *os.Root, name string, flags int, mode os.FileMode) (*os.File, error) {
	return nil, fmt.Errorf("symlink-free workspace file access requires Linux openat2: %w", ErrExecutionDenied)
}

func ReplaceExecutionFile(root *os.Root, name string, src io.Reader) (int64, error) {
	return 0, ErrExecutionDenied
}

func RemoveExecutionTree(ctx context.Context, root *os.Root, name string) error {
	return ErrExecutionDenied
}

func MkdirExecutionAll(root *os.Root, name string, mode os.FileMode) error { return ErrExecutionDenied }
func RemoveExecutionFile(root *os.Root, name string) error                 { return ErrExecutionDenied }
