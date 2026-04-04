// Package container manages per-organization Docker containers for isolated agent execution.
package container

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Config holds container configuration for an organization.
type Config struct {
	Enabled bool   `json:"enabled"`
	Image   string `json:"image,omitempty"`   // Docker image (default: at-agent-runtime:latest)
	CPU     string `json:"cpu,omitempty"`     // CPU limit (e.g., "2")
	Memory  string `json:"memory,omitempty"`  // Memory limit (e.g., "4g")
	Network bool   `json:"network"`          // Allow network access
}

// DefaultConfig returns the default container configuration.
func DefaultConfig() Config {
	return Config{
		Enabled: false,
		Image:   "at-agent-runtime:latest",
		CPU:     "2",
		Memory:  "4g",
		Network: true,
	}
}

// Manager manages per-organization containers.
type Manager struct {
	mu         sync.RWMutex
	containers map[string]*containerInfo // orgID -> container info
}

type containerInfo struct {
	containerID string
	orgID       string
	config      Config
	createdAt   time.Time
	lastUsed    time.Time
}

// New creates a new container manager.
func New() *Manager {
	return &Manager{
		containers: make(map[string]*containerInfo),
	}
}

// EnsureContainer creates or returns an existing container for the given org.
func (m *Manager) EnsureContainer(ctx context.Context, orgID string, cfg Config) (string, error) {
	if !cfg.Enabled {
		return "", nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if container already exists and is running
	if info, ok := m.containers[orgID]; ok {
		if isContainerRunning(ctx, info.containerID) {
			info.lastUsed = time.Now()
			return info.containerID, nil
		}
		// Container exists but stopped — remove and recreate
		delete(m.containers, orgID)
	}

	// Create new container
	containerID, err := createContainer(ctx, orgID, cfg)
	if err != nil {
		return "", fmt.Errorf("create container for org %s: %w", orgID, err)
	}

	m.containers[orgID] = &containerInfo{
		containerID: containerID,
		orgID:       orgID,
		config:      cfg,
		createdAt:   time.Now(),
		lastUsed:    time.Now(),
	}

	slog.Info("container: created", "org_id", orgID, "container_id", containerID[:12])
	return containerID, nil
}

// Exec runs a command inside the org's container and returns stdout.
func (m *Manager) Exec(ctx context.Context, orgID string, cfg Config, command string, env map[string]string) (string, string, int, error) {
	containerID, err := m.EnsureContainer(ctx, orgID, cfg)
	if err != nil {
		return "", "", -1, err
	}
	if containerID == "" {
		return "", "", -1, fmt.Errorf("container not enabled for org %s", orgID)
	}

	args := []string{"exec"}

	// Pass environment variables
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}

	args = append(args, containerID, "bash", "-c", command)

	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", "", -1, fmt.Errorf("docker exec: %w", err)
		}
	}

	return stdout.String(), stderr.String(), exitCode, nil
}

// ExecPython runs a Python script inside the org's container.
func (m *Manager) ExecPython(ctx context.Context, orgID string, cfg Config, script string, env map[string]string) (string, string, int, error) {
	// Write script to a temp file inside the container and execute
	command := fmt.Sprintf("cat > /tmp/_agent_script.py << 'ENDSCRIPT'\n%s\nENDSCRIPT\npython3 /tmp/_agent_script.py", script)
	return m.Exec(ctx, orgID, cfg, command, env)
}

// StopContainer stops and removes a container for an org.
func (m *Manager) StopContainer(ctx context.Context, orgID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, ok := m.containers[orgID]
	if !ok {
		return nil
	}

	removeContainer(ctx, info.containerID)
	delete(m.containers, orgID)
	slog.Info("container: stopped", "org_id", orgID, "container_id", info.containerID[:12])
	return nil
}

// StopAll stops all containers (called on shutdown).
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for orgID, info := range m.containers {
		removeContainer(ctx, info.containerID)
		slog.Info("container: stopped", "org_id", orgID)
	}
	m.containers = make(map[string]*containerInfo)
}

// CleanupIdle stops containers that haven't been used for the given duration.
func (m *Manager) CleanupIdle(ctx context.Context, maxIdle time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for orgID, info := range m.containers {
		if now.Sub(info.lastUsed) > maxIdle {
			removeContainer(ctx, info.containerID)
			delete(m.containers, orgID)
			slog.Info("container: cleaned up idle", "org_id", orgID, "idle", now.Sub(info.lastUsed))
		}
	}
}

// ListContainers returns info about running containers.
func (m *Manager) ListContainers() map[string]map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]map[string]any, len(m.containers))
	for orgID, info := range m.containers {
		result[orgID] = map[string]any{
			"container_id": info.containerID[:12],
			"created_at":   info.createdAt.Format(time.RFC3339),
			"last_used":    info.lastUsed.Format(time.RFC3339),
			"image":        info.config.Image,
		}
	}
	return result
}

// ─── Docker helpers ───

func createContainer(ctx context.Context, orgID string, cfg Config) (string, error) {
	image := cfg.Image
	if image == "" {
		image = "at-agent-runtime:latest"
	}

	name := fmt.Sprintf("at-org-%s", orgID[:min(12, len(orgID))])

	args := []string{
		"run", "-d",
		"--name", name,
		"--label", "at.org.id=" + orgID,
		"--label", "at.managed=true",
	}

	// Resource limits
	if cfg.CPU != "" {
		args = append(args, "--cpus", cfg.CPU)
	}
	if cfg.Memory != "" {
		args = append(args, "--memory", cfg.Memory)
	}

	// Network
	if !cfg.Network {
		args = append(args, "--network", "none")
	}

	// Workspace volume
	args = append(args, "-v", fmt.Sprintf("/tmp/at-org-%s:/workspace", orgID[:min(12, len(orgID))]))

	args = append(args, image)

	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// If container name exists, try to start it
		if strings.Contains(stderr.String(), "Conflict") {
			startCmd := exec.CommandContext(ctx, "docker", "start", name)
			if startErr := startCmd.Run(); startErr == nil {
				// Get container ID
				idCmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.Id}}", name)
				var idOut bytes.Buffer
				idCmd.Stdout = &idOut
				if idErr := idCmd.Run(); idErr == nil {
					return strings.TrimSpace(idOut.String()), nil
				}
			}
		}
		return "", fmt.Errorf("docker run: %s: %w", stderr.String(), err)
	}

	return strings.TrimSpace(stdout.String()), nil
}

func isContainerRunning(ctx context.Context, containerID string) bool {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", containerID)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func removeContainer(ctx context.Context, containerID string) {
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
	_ = cmd.Run()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
