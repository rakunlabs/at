package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenExecutionRoot returns an owned handle for one action. All subsequent I/O
// must use this handle, not an absolute path reconstructed after authorization.
// os.Root enforces containment while following symlinks, including during races.
func OpenExecutionRoot(ctx context.Context, raw string, write bool) (*os.Root, string, error) {
	_, base, ok := ExecutionFromContext(ctx)
	if !ok {
		return nil, "", ErrExecutionDenied
	}
	name := "files.read"
	if write {
		name = "files.write"
	}
	if raw == "" {
		raw = "."
	}
	if filepath.IsAbs(raw) {
		rel, err := filepath.Rel(base, raw)
		if err != nil || !filepath.IsLocal(rel) {
			if err := CheckExecution(ctx, ExecutionAction{Kind: "file", Name: "files.host", Path: raw}); err != nil {
				return nil, "", err
			}
			base, raw = string(filepath.Separator), strings.TrimPrefix(filepath.Clean(raw), string(filepath.Separator))
		} else {
			raw = rel
		}
	}
	// Reject traversal rather than silently normalising an attempted escape.
	if !filepath.IsLocal(raw) {
		return nil, "", ErrExecutionDenied
	}
	for _, part := range strings.Split(filepath.ToSlash(raw), "/") {
		if part == ".." {
			return nil, "", ErrExecutionDenied
		}
	}
	raw = filepath.Clean(raw)
	if err := CheckExecution(ctx, ExecutionAction{Kind: "file", Name: name, Path: raw}); err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", fmt.Errorf("open execution root: %w", err)
	}
	return root, raw, nil
}

// ExecutionAssetsDir does not mutate the process-wide legacy asset setting.
func ExecutionAssetsDir(ctx context.Context) (string, error) {
	_, base, ok := ExecutionFromContext(ctx)
	if !ok {
		return "", ErrExecutionDenied
	}
	return filepath.Join(base, "assets"), nil
}
