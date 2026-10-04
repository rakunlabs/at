package workflow

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// Workflow steps exchange files by reference, never by embedding bytes in the
// JSON payload. A file lives in the run workspace (runs/<run_id>, the same
// directory exec nodes receive as AT_WORK_DIR) and is addressed through the
// execution root, so containment and file admission match the file tools.

// RunFile is the JSON shape one step hands to another for a stored file.
// Path is relative to the run workspace (what a person types in an Email
// attachment field); WorkspacePath is relative to the execution root and
// stays valid when the reference crosses into a differently scoped run.
type RunFile struct {
	Path          string `json:"path"`
	WorkspacePath string `json:"workspace_path"`
	Name          string `json:"name"`
	ContentType   string `json:"content_type"`
	SizeBytes     int64  `json:"size_bytes"`
}

// ToMap returns the reference as the generic map nodes pass around.
func (f RunFile) ToMap() map[string]any {
	return map[string]any{
		"path":           f.Path,
		"workspace_path": f.WorkspacePath,
		"name":           f.Name,
		"content_type":   f.ContentType,
		"size_bytes":     f.SizeBytes,
	}
}

// RunWorkspaceDir returns the execution-root-relative directory of this run.
func RunWorkspaceDir(ctx context.Context) (string, error) {
	provenance, _, ok := service.ExecutionFromContext(ctx)
	if !ok || provenance.RunID == "" || !filepath.IsLocal(provenance.RunID) {
		return "", service.ErrExecutionDenied
	}
	return path.Join("runs", provenance.RunID), nil
}

// CleanRunPath refuses absolute paths and traversal instead of normalising
// them, so a template that renders "../x" is an error rather than a new place.
func CleanRunPath(raw string) (string, error) {
	raw = strings.TrimSpace(filepath.ToSlash(raw))
	if raw == "" {
		return "", fmt.Errorf("file path is empty")
	}
	if strings.HasPrefix(raw, "/") || strings.Contains(raw, "\\") {
		return "", fmt.Errorf("file path %q must be relative to the run workspace", raw)
	}
	for _, part := range strings.Split(raw, "/") {
		if part == ".." {
			return "", fmt.Errorf("file path %q must not contain '..'", raw)
		}
	}
	cleaned := path.Clean(raw)
	if cleaned == "." {
		return "", fmt.Errorf("file path %q names the run workspace itself", raw)
	}
	return cleaned, nil
}

// WriteRunFile streams r into the run workspace, refusing more than limit
// bytes. A partially written file is removed when the copy fails.
func WriteRunFile(ctx context.Context, relPath string, r io.Reader, limit int64, contentType string) (RunFile, error) {
	relPath, err := CleanRunPath(relPath)
	if err != nil {
		return RunFile{}, err
	}
	runDir, err := RunWorkspaceDir(ctx)
	if err != nil {
		return RunFile{}, err
	}
	workspacePath := path.Join(runDir, relPath)
	root, name, err := service.OpenExecutionRoot(ctx, workspacePath, true)
	if err != nil {
		return RunFile{}, err
	}
	defer root.Close()

	if dir := filepath.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return RunFile{}, fmt.Errorf("create directory: %w", err)
		}
	}
	f, err := root.Create(name)
	if err != nil {
		return RunFile{}, fmt.Errorf("create file: %w", err)
	}
	written, copyErr := io.Copy(f, io.LimitReader(r, limit+1))
	closeErr := f.Close()
	if copyErr == nil && written > limit {
		copyErr = fmt.Errorf("file exceeds %d bytes", limit)
	}
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = root.Remove(name)
		return RunFile{}, fmt.Errorf("write %s: %w", relPath, copyErr)
	}

	return RunFile{
		Path:          relPath,
		WorkspacePath: workspacePath,
		Name:          path.Base(relPath),
		ContentType:   FileContentType(contentType, relPath, nil),
		SizeBytes:     written,
	}, nil
}

// FileContentType prefers an explicit type, then the file extension, then a
// sniff of the leading bytes.
func FileContentType(explicit, name string, head []byte) string {
	if mediaType, _, err := mime.ParseMediaType(explicit); err == nil && mediaType != "" {
		return mediaType
	}
	if byExt := mime.TypeByExtension(path.Ext(name)); byExt != "" {
		if mediaType, _, err := mime.ParseMediaType(byExt); err == nil {
			return mediaType
		}
	}
	if len(head) > 0 {
		if mediaType, _, err := mime.ParseMediaType(http.DetectContentType(head)); err == nil {
			return mediaType
		}
	}
	return "application/octet-stream"
}

// SafeFileName reduces a caller-supplied name to its base name, or "" when
// nothing usable remains.
func SafeFileName(name string) string {
	name = strings.TrimSpace(filepath.Base(filepath.ToSlash(strings.ReplaceAll(name, "\\", "/"))))
	if name == "" || name == "." || name == "/" || name == ".." {
		return ""
	}
	return name
}
