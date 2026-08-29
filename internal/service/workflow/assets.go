package workflow

import (
	"os"
	"path/filepath"
	"sync"
)

// assetsDirOnce caches the resolved assets root so every bash handler
// invocation reuses the same absolute path.
var (
	assetsDirOnce sync.Once
	assetsDirPath string
)

// AssetsDir returns the persistent asset library root, injected into bash
// skill handlers as AT_ASSETS_DIR. Unlike per-task workspaces (which live
// under loopgov.WorkspaceRoot and are swept by the workspace janitor),
// assets are durable: avatar images, cloned-voice manifests, and other
// reusable media that must survive task completion live here.
//
// The root is ./data/assets resolved to an absolute path — the same ./data
// directory Docker users bind-mount for persistence. Subdirectories
// (avatars/, voices/, uploads/, series/) are created by EnsureAssetsDir and
// lazily by the tools that use them.
func AssetsDir() string {
	assetsDirOnce.Do(func() {
		p := filepath.Join("data", "assets")
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		assetsDirPath = p
	})
	return assetsDirPath
}

// EnsureAssetsDir creates the assets root and its conventional
// subdirectories (and returns the root). Best-effort: on error the path is
// still returned so callers can surface the failure when they actually try
// to write.
func EnsureAssetsDir() string {
	dir := AssetsDir()
	_ = os.MkdirAll(dir, 0o755)
	for _, sub := range []string{"avatars", "voices", "uploads", "series"} {
		_ = os.MkdirAll(filepath.Join(dir, sub), 0o755)
	}
	return dir
}
