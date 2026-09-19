package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	mcpDirMu   sync.Mutex
	mcpDirPath string
	mcpDirErr  error
)

// ConfigureMCPDir selects the process-wide MCP program library before starting
// consumers. A blank workspace root preserves the shipped ./data/mcps location.
// Like ConfigureAssetsDir, this only resolves the path; it never moves existing
// files or creates directories.
func ConfigureMCPDir(workspaceRoot string) error {
	mcpDirMu.Lock()
	defer mcpDirMu.Unlock()
	return configureMCPDir(workspaceRoot)
}

func configureMCPDir(workspaceRoot string) error {
	p := filepath.Join("data", "mcps")
	if strings.TrimSpace(workspaceRoot) != "" {
		p = filepath.Join(workspaceRoot, "mcps")
	}
	abs, err := filepath.Abs(p)
	mcpDirPath, mcpDirErr = p, nil
	if err != nil {
		mcpDirErr = fmt.Errorf("resolve mcps directory %q: %w", p, err)
	} else {
		mcpDirPath = abs
	}
	return mcpDirErr
}

func resolvedMCPDir() (string, error) {
	mcpDirMu.Lock()
	defer mcpDirMu.Unlock()
	if mcpDirPath == "" {
		_ = configureMCPDir("")
	}
	return mcpDirPath, mcpDirErr
}

// MCPDir returns the persistent MCP program library root. Operators mount it
// (via server.workspace.root) so binaries and config files that stdio MCP
// upstreams reference survive restarts. Unlike per-task workspaces it is NOT
// swept by the workspace janitor ("mcps" is a reserved entry).
//
// The root is <server.workspace.root>/mcps when explicitly configured,
// otherwise ./data/mcps, resolved to an absolute path.
func MCPDir() string {
	dir, _ := resolvedMCPDir()
	return dir
}

// EnsureMCPDirReady creates and checks the MCP library without hiding
// failures. Probe files have unique names and are removed; existing files are
// untouched.
func EnsureMCPDirReady() (string, error) {
	dir, err := resolvedMCPDir()
	if err != nil {
		return dir, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return dir, fmt.Errorf("create mcps directory %q: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, ".at-startup-probe-*")
	if err != nil {
		return dir, fmt.Errorf("mcps directory %q is not writable: %w", dir, err)
	}
	closeErr := probe.Close()
	removeErr := os.Remove(probe.Name())
	if closeErr != nil {
		return dir, fmt.Errorf("close mcps probe in %q: %w", dir, closeErr)
	}
	if removeErr != nil {
		return dir, fmt.Errorf("remove mcps probe in %q: %w", dir, removeErr)
	}
	return dir, nil
}
