// Package container manages per-organization Docker containers for isolated agent execution.
package container

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const containerCommandOutputMaxBytes = 16 << 20

type boundedOutput struct {
	buf       bytes.Buffer
	remaining int
	truncated bool
}

func newBoundedOutput(limit int) *boundedOutput {
	return &boundedOutput{remaining: limit}
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	original := len(p)
	if len(p) > w.remaining {
		p = p[:max(0, w.remaining)]
		w.truncated = true
	}
	if len(p) > 0 {
		_, _ = w.buf.Write(p)
		w.remaining -= len(p)
	}
	return original, nil
}

func (w *boundedOutput) String() string {
	if !w.truncated {
		return w.buf.String()
	}
	return w.buf.String() + "\n[output truncated at 16 MiB]\n"
}

// Config holds container configuration for an organization.
type Config struct {
	Enabled          bool   `json:"enabled"`
	Image            string `json:"image,omitempty"`  // Docker image (default: at-agent-runtime:latest)
	CPU              string `json:"cpu,omitempty"`    // CPU limit (e.g., "2")
	Memory           string `json:"memory,omitempty"` // Memory limit (e.g., "4g")
	Network          bool   `json:"network"`          // Allow network access
	PersistentVolume bool   `json:"persistent_volume,omitempty"`
	PreferRootless   bool   `json:"prefer_rootless,omitempty"` // warn (never fail) when the daemon is rootful
	ReadOnlyRoot     bool   `json:"read_only_root,omitempty"`
	DiskLimitBytes   int64  `json:"disk_limit_bytes,omitempty"`
	PidsLimit        int    `json:"pids_limit,omitempty"`
	// ProvisionTools starts the container from an image derived from Image that
	// adds the developer toolchain and a keep-alive entrypoint (see
	// runtime-image.go), so a stock distribution image can be used directly.
	ProvisionTools bool `json:"provision_tools,omitempty"`
}

// DefaultConfig returns the default container configuration.
func DefaultConfig() Config {
	return Config{
		Enabled:   false,
		Image:     "at-agent-runtime:latest",
		CPU:       "2",
		Memory:    "4g",
		Network:   true,
		PidsLimit: 256,
	}
}

// Manager manages per-organization containers.
type Manager struct {
	mu         sync.RWMutex
	containers map[string]*containerInfo // orgID -> container info

	imageMu sync.Mutex
	images  map[string]string // base image -> derived runtime image
}

type containerInfo struct {
	containerID string
	orgID       string
	config      Config
	createdAt   time.Time
	lastUsed    time.Time
	active      int
}

// New creates a new container manager.
func New() *Manager {
	return &Manager{
		containers: make(map[string]*containerInfo),
		images:     make(map[string]string),
	}
}

// EnsureContainer creates or returns an existing container for the given org.
func (m *Manager) EnsureContainer(ctx context.Context, orgID string, cfg Config) (string, error) {
	if !cfg.Enabled {
		return "", nil
	}

	// Resolved before m.mu: preparing a derived image can take a while and must
	// not block commands in other, already running scopes.
	image, err := m.resolveImage(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("create container for %s: %w", orgID, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if container already exists and is running
	if info, ok := m.containers[orgID]; ok {
		if info.config == cfg && isContainerRunning(ctx, info.containerID) {
			info.lastUsed = time.Now()
			return info.containerID, nil
		}
		// Container exists but stopped — remove and recreate
		removeContainer(ctx, info.containerID)
		delete(m.containers, orgID)
	}

	// Create new container
	containerID, err := createContainer(ctx, orgID, image, cfg)
	if err != nil {
		if cfg.ProvisionTools {
			// The derived image may have been pruned; re-check it next time.
			m.forgetImage(baseImage(cfg))
		}
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
	if cfg.DiskLimitBytes > 0 {
		used, err := workspaceUsage(ctx, containerID)
		if err != nil {
			return "", "", -1, fmt.Errorf("check workspace quota: %w", err)
		}
		if used > cfg.DiskLimitBytes {
			return "", "", -1, fmt.Errorf("workspace quota exceeded: %d of %d bytes", used, cfg.DiskLimitBytes)
		}
	}
	m.markActive(orgID, 1)
	defer m.markActive(orgID, -1)

	args := []string{"exec"}

	// Pass environment variables
	for k, v := range env {
		args = append(args, "-e", k+"="+v)
	}

	args = append(args, containerID, "bash", "-c", command)

	cmd := exec.CommandContext(ctx, "docker", args...)
	stdout, stderr := newBoundedOutput(containerCommandOutputMaxBytes), newBoundedOutput(containerCommandOutputMaxBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", "", -1, fmt.Errorf("docker exec: %w", err)
		}
	}

	if cfg.DiskLimitBytes > 0 {
		if used, usageErr := workspaceUsage(ctx, containerID); usageErr == nil && used > cfg.DiskLimitBytes {
			return stdout.String(), stderr.String(), exitCode, fmt.Errorf("workspace quota exceeded after command: %d of %d bytes", used, cfg.DiskLimitBytes)
		}
	}
	return stdout.String(), stderr.String(), exitCode, nil
}

// ExecArgs executes one binary without a shell. Coding-space Git operations use
// this path so remote URLs, refs and commit messages never become shell syntax.
func (m *Manager) ExecArgs(ctx context.Context, scopeID string, cfg Config, workDir string, env map[string]string, command string, commandArgs ...string) (string, string, int, error) {
	return m.ExecArgsInput(ctx, scopeID, cfg, workDir, env, nil, command, commandArgs...)
}

// ExecArgsInput is ExecArgs with stdin attached. File contents travel this way
// rather than as an argument: a single argv string is limited to 128 KiB by the
// kernel, which silently capped every write that used base64 arguments.
func (m *Manager) ExecArgsInput(ctx context.Context, scopeID string, cfg Config, workDir string, env map[string]string, stdin io.Reader, command string, commandArgs ...string) (string, string, int, error) {
	if command == "" {
		return "", "", -1, fmt.Errorf("command is required")
	}
	if workDir != "" && workDir != "/workspace" && !strings.HasPrefix(workDir, "/workspace/") {
		return "", "", -1, fmt.Errorf("work directory must stay inside /workspace")
	}
	containerID, err := m.EnsureContainer(ctx, scopeID, cfg)
	if err != nil {
		return "", "", -1, err
	}
	if containerID == "" {
		return "", "", -1, fmt.Errorf("container not enabled for scope %s", scopeID)
	}
	if cfg.DiskLimitBytes > 0 {
		used, err := workspaceUsage(ctx, containerID)
		if err != nil {
			return "", "", -1, fmt.Errorf("check workspace quota: %w", err)
		}
		if used > cfg.DiskLimitBytes {
			return "", "", -1, fmt.Errorf("workspace quota exceeded: %d of %d bytes", used, cfg.DiskLimitBytes)
		}
	}
	m.markActive(scopeID, 1)
	defer m.markActive(scopeID, -1)

	args := []string{"exec"}
	if stdin != nil {
		args = append(args, "-i")
	}
	for key, value := range env {
		args = append(args, "-e", key+"="+value)
	}
	if workDir != "" {
		args = append(args, "-w", workDir)
	}
	args = append(args, containerID, command)
	args = append(args, commandArgs...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	stdout, stderr := newBoundedOutput(containerCommandOutputMaxBytes), newBoundedOutput(containerCommandOutputMaxBytes)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", "", -1, fmt.Errorf("docker exec: %w", err)
		}
	}
	if cfg.DiskLimitBytes > 0 {
		if used, usageErr := workspaceUsage(ctx, containerID); usageErr == nil && used > cfg.DiskLimitBytes {
			return stdout.String(), stderr.String(), exitCode, fmt.Errorf("workspace quota exceeded after command: %d of %d bytes", used, cfg.DiskLimitBytes)
		}
	}
	return stdout.String(), stderr.String(), exitCode, nil
}

func (m *Manager) markActive(scopeID string, delta int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if info := m.containers[scopeID]; info != nil {
		info.active += delta
		if info.active < 0 {
			info.active = 0
		}
		info.lastUsed = time.Now()
	}
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

// RemoveScope removes the managed container and its persistent workspace
// volume. Unlike StopContainer, this is destructive and is used only when the
// owning developer space is deleted.
func (m *Manager) RemoveScope(ctx context.Context, scopeID string) error {
	m.mu.Lock()
	if info := m.containers[scopeID]; info != nil {
		removeContainer(ctx, info.containerID)
		delete(m.containers, scopeID)
	}
	m.mu.Unlock()

	scopeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(scopeID)))[:20]
	name := "at-scope-" + scopeHash
	if output, err := exec.CommandContext(ctx, "docker", "rm", "-f", name).CombinedOutput(); err != nil && !strings.Contains(string(output), "No such container") {
		return fmt.Errorf("remove managed container: %s: %w", strings.TrimSpace(string(output)), err)
	}
	volume := "at-space-" + scopeHash
	if output, err := exec.CommandContext(ctx, "docker", "volume", "rm", volume).CombinedOutput(); err != nil && !strings.Contains(string(output), "No such volume") {
		return fmt.Errorf("remove managed volume: %s: %w", strings.TrimSpace(string(output)), err)
	}
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
		if info.active == 0 && now.Sub(info.lastUsed) > maxIdle {
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
			"active":       info.active,
		}
	}
	return result
}

// ─── Docker helpers ───

func createContainer(ctx context.Context, orgID, image string, cfg Config) (string, error) {
	if image == "" {
		image = baseImage(cfg)
	}

	if cfg.PreferRootless {
		// Rootless Docker is recommended, not required: a rootful daemon still
		// gets the dropped capabilities, no-new-privileges and quotas below.
		rootless, err := dockerIsRootless(ctx)
		switch {
		case err != nil:
			slog.Warn("container: could not determine whether docker is rootless", "scope", orgID, "error", err.Error())
		case !rootless:
			slog.Warn("container: docker is not running in rootless mode; continuing with a rootful daemon (rootless Docker is recommended for stronger isolation)", "scope", orgID)
		}
	}

	scopeHash := fmt.Sprintf("%x", sha256.Sum256([]byte(orgID)))[:20]
	name := "at-scope-" + scopeHash

	args := []string{
		"run", "-d",
		"--name", name,
		"--label", "at.scope.hash=" + scopeHash,
		"--label", "at.managed=true",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
	}
	pids := cfg.PidsLimit
	if pids <= 0 {
		pids = 256
	}
	args = append(args, "--pids-limit", fmt.Sprintf("%d", pids))

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
	if cfg.ReadOnlyRoot {
		args = append(args, "--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,size=256m")
	}

	// Docker-managed volumes avoid caller-selected host mounts. Developer spaces
	// keep theirs across idle container removal; transient scopes get tmpfs.
	if cfg.PersistentVolume {
		args = append(args, "-v", "at-space-"+scopeHash+":/workspace")
	} else {
		tmpfs := "/workspace:rw,nosuid,nodev"
		if cfg.DiskLimitBytes > 0 {
			tmpfs += fmt.Sprintf(",size=%d", cfg.DiskLimitBytes)
		}
		args = append(args, "--tmpfs", tmpfs)
	}

	args = append(args, image)

	run := func() (string, string, error) {
		cmd := exec.CommandContext(ctx, "docker", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.String(), stderr.String(), err
	}

	stdout, stderr, err := run()
	if err != nil && strings.Contains(stderr, "Conflict") {
		// A container from an earlier run of this process still holds the name.
		// Reuse it only when it was started from the same image; otherwise an
		// image change would be ignored until someone removed it by hand.
		var existing bytes.Buffer
		inspect := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.Id}} {{.Config.Image}}", name)
		inspect.Stdout = &existing
		if inspect.Run() == nil {
			id, existingImage, _ := strings.Cut(strings.TrimSpace(existing.String()), " ")
			if existingImage == image {
				if exec.CommandContext(ctx, "docker", "start", name).Run() == nil {
					return id, nil
				}
			}
			removeContainer(ctx, name)
			stdout, stderr, err = run()
		}
	}
	if err != nil {
		return "", fmt.Errorf("docker run: %s: %w", strings.TrimSpace(stderr), err)
	}

	return strings.TrimSpace(stdout), nil
}

func dockerIsRootless(ctx context.Context) (bool, error) {
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .SecurityOptions}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("inspect docker security options: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.Contains(strings.ToLower(string(out)), "rootless"), nil
}

func workspaceUsage(ctx context.Context, containerID string) (int64, error) {
	cmd := exec.CommandContext(ctx, "docker", "exec", containerID, "du", "-sk", "/workspace")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("docker exec du: %s: %w", strings.TrimSpace(string(out)), err)
	}
	var kib int64
	if _, err := fmt.Sscan(string(out), &kib); err != nil {
		return 0, fmt.Errorf("parse workspace usage: %w", err)
	}
	return kib * 1024, nil
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
