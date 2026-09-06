package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	assetsDirMu   sync.Mutex
	assetsDirPath string
	assetsDirErr  error
)

// ConfigureAssetsDir selects the process-wide library before starting consumers.
// A blank workspace root preserves the shipped ./data/assets location. This
// only resolves the path; it never moves existing assets or creates directories.
func ConfigureAssetsDir(workspaceRoot string) error {
	assetsDirMu.Lock()
	defer assetsDirMu.Unlock()
	return configureAssetsDir(workspaceRoot)
}

func configureAssetsDir(workspaceRoot string) error {
	p := filepath.Join("data", "assets")
	if strings.TrimSpace(workspaceRoot) != "" {
		p = filepath.Join(workspaceRoot, "assets")
	}
	abs, err := filepath.Abs(p)
	assetsDirPath, assetsDirErr = p, nil
	if err != nil {
		assetsDirErr = fmt.Errorf("resolve assets directory %q: %w", p, err)
	} else {
		assetsDirPath = abs
	}
	return assetsDirErr
}

func resolvedAssetsDir() (string, error) {
	assetsDirMu.Lock()
	defer assetsDirMu.Unlock()
	if assetsDirPath == "" {
		_ = configureAssetsDir("")
	}
	return assetsDirPath, assetsDirErr
}

// AssetsDir returns the persistent asset library root, injected into bash
// skill handlers as AT_ASSETS_DIR. Unlike per-task workspaces (which live
// under loopgov.WorkspaceRoot and are swept by the workspace janitor),
// assets are durable: avatar images, cloned-voice manifests, and other
// reusable media that must survive task completion live here.
//
// The root is <server.workspace.root>/assets when explicitly configured,
// otherwise ./data/assets, resolved to an absolute path. Subdirectories
// (avatars/, voices/, uploads/, series/) are created by EnsureAssetsDir and
// lazily by the tools that use them.
func AssetsDir() string {
	dir, _ := resolvedAssetsDir()
	return dir
}

// EnsureAssetsDir creates the assets root and its conventional
// subdirectories (and returns the root). Best-effort: on error the path is
// still returned so callers can surface the failure when they actually try
// to write.
func EnsureAssetsDir() string {
	dir, _ := EnsureAssetsDirReady()
	return dir
}

// EnsureAssetsDirReady creates and checks the library without hiding failures.
// Probe files have unique names and are removed; existing media is untouched.
func EnsureAssetsDirReady() (string, error) {
	dir, err := resolvedAssetsDir()
	if err != nil {
		return dir, err
	}
	for _, sub := range []string{"", "avatars", "voices", "uploads", "series"} {
		path := filepath.Join(dir, sub)
		if err := os.MkdirAll(path, 0o755); err != nil {
			return dir, fmt.Errorf("create assets directory %q: %w", path, err)
		}
		probe, err := os.CreateTemp(path, ".at-startup-probe-*")
		if err != nil {
			return dir, fmt.Errorf("assets directory %q is not writable: %w", path, err)
		}
		closeErr := probe.Close()
		removeErr := os.Remove(probe.Name())
		if closeErr != nil {
			return dir, fmt.Errorf("close assets probe in %q: %w", path, closeErr)
		}
		if removeErr != nil {
			return dir, fmt.Errorf("remove assets probe in %q: %w", path, removeErr)
		}
	}
	return dir, nil
}
