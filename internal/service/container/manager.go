// Package container manages isolated sandboxes for agent execution and
// developer spaces. The Manager is backend-neutral; a Driver (docker.go)
// talks to whatever actually runs them.
package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"reflect"
	"slices"
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
	// KeepAlive replaces the image's entrypoint with `sleep infinity`, so a
	// stock image whose default command exits (debian, ubuntu, alpine…) stays
	// up for exec and terminals. Commands are always run through exec.
	KeepAlive bool `json:"keep_alive,omitempty"`
	// RetainWhenIdle asks the backend to Stop rather than Remove. Persistent
	// volumes survive; root-filesystem retention depends on its capabilities.
	RetainWhenIdle bool `json:"retain_when_idle,omitempty"`
	// CapAdd re-adds capabilities after every capability is dropped. Names
	// are Linux capability names without the CAP_ prefix.
	CapAdd []string `json:"cap_add,omitempty"`
	// HomeScope names a persistent home volume shared by every sandbox of the
	// same owner; HomePath is where it is mounted and becomes $HOME. Both are
	// empty when no home is configured, which keeps the configuration label of
	// existing sandboxes unchanged.
	HomeScope string `json:"home_scope,omitempty"`
	HomePath  string `json:"home_path,omitempty"`
}

// PackageManagerCapabilities is the subset of Docker's default capability
// set that root inside a sandbox needs to install packages: maintainer
// scripts create users and groups (writing /etc/shadow and /etc/gshadow),
// chown and chmod files they did not create, and apt/dpkg drop to helper
// users. Without them apt downloads but dpkg fails in postinst (for example
// openssh-client's `groupadd _ssh`). no-new-privileges stays in force.
var PackageManagerCapabilities = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "KILL", "SETGID", "SETUID"}

// Equal reports whether two configurations describe the same sandbox.
func (c Config) Equal(other Config) bool {
	return reflect.DeepEqual(c, other)
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

// Manager tracks live sandboxes per scope and delegates running them to a
// Driver.
type Manager struct {
	driver     Driver
	mu         sync.RWMutex
	containers map[string]*containerInfo // scope -> container info
	// installMu serializes helper installs so a concurrent call never execs a
	// half-copied file. Only first use of a sandbox waits on it.
	installMu sync.Mutex
}

type containerInfo struct {
	containerID string
	orgID       string
	config      Config
	createdAt   time.Time
	lastUsed    time.Time
	active      int
	stopping    bool
	drainFailed bool
	uses        map[*sandboxUse]struct{}
	// files records helper installs into this sandbox by destination path:
	// true once installed, false when it cannot be (no build for the
	// platform, or the driver refused), so neither is retried per call.
	files map[string]bool
}

// New creates a container manager backed by the Docker CLI.
func New() *Manager {
	return NewWithDriver(NewDockerDriver())
}

// NewWithDriver creates a container manager backed by driver.
func NewWithDriver(driver Driver) *Manager {
	return &Manager{
		driver:     driver,
		containers: make(map[string]*containerInfo),
	}
}

// Driver returns the backend this manager runs sandboxes on.
func (m *Manager) Driver() Driver { return m.driver }

// EnsureContainer creates or returns an existing container for the given org.
func (m *Manager) EnsureContainer(ctx context.Context, orgID string, cfg Config) (string, error) {
	if !cfg.Enabled {
		return "", nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureContainer(ctx, orgID, cfg)
}

// ensureContainer is called with mu held. Registering activity under the same
// lock closes the gap between provisioning and idle/explicit removal.
func (m *Manager) ensureContainer(ctx context.Context, orgID string, cfg Config) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Check if container already exists and is running
	if info, ok := m.containers[orgID]; ok {
		if info.stopping || info.drainFailed {
			return "", fmt.Errorf("sandbox for scope %s is stopping", orgID)
		}
		if info.active > 0 && !info.config.Equal(cfg) {
			return "", fmt.Errorf("sandbox for scope %s is busy; finish active work before reconfiguration", orgID)
		}
		if info.config.Equal(cfg) && m.driver.Running(ctx, info.containerID) {
			info.lastUsed = time.Now()
			return info.containerID, nil
		}
		// Reconfigured sandboxes are replaced. A stopped one with the same
		// configuration is left for Create to reconcile; root retention is
		// backend-dependent. A failed health probe must not discard activity.
		if !info.config.Equal(cfg) {
			if err := m.driver.Remove(ctx, info.containerID); err != nil {
				return "", fmt.Errorf("replace sandbox for scope %s: %w", orgID, err)
			}
			delete(m.containers, orgID)
		}
		if info.active > 0 {
			return "", fmt.Errorf("sandbox for scope %s has active work but failed its health probe", orgID)
		}
	}

	containerID, err := m.driver.Create(ctx, orgID, cfg)
	if err != nil {
		return "", fmt.Errorf("create container for org %s: %w", orgID, err)
	}
	if info := m.containers[orgID]; info != nil && info.containerID == containerID && info.config.Equal(cfg) {
		info.lastUsed = time.Now()
		return containerID, nil
	}

	m.containers[orgID] = &containerInfo{
		containerID: containerID,
		orgID:       orgID,
		config:      cfg,
		createdAt:   time.Now(),
		lastUsed:    time.Now(),
	}

	slog.Info("container: created", "driver", m.driver.Name(), "org_id", orgID, "container_id", shortHandle(containerID))
	return containerID, nil
}

// Exec runs a shell command inside the org's container and returns stdout.
func (m *Manager) Exec(ctx context.Context, orgID string, cfg Config, command string, env map[string]string) (string, string, int, error) {
	return m.run(ctx, orgID, cfg, ExecRequest{Argv: []string{"bash", "-c", command}, Env: env})
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
	if workDir != "" && !insideWorkspace(workDir) {
		return "", "", -1, fmt.Errorf("work directory must stay inside /workspace")
	}
	argv := append([]string{command}, commandArgs...)
	return m.run(ctx, scopeID, cfg, ExecRequest{Argv: argv, Env: env, WorkDir: workDir, Stdin: stdin})
}

func (m *Manager) run(ctx context.Context, scopeID string, cfg Config, req ExecRequest) (string, string, int, error) {
	ctx, containerID, finish, err := m.beginUse(ctx, scopeID, cfg)
	if err != nil {
		return "", "", -1, err
	}
	defer finish()
	if containerID == "" {
		return "", "", -1, fmt.Errorf("container not enabled for scope %s", scopeID)
	}
	if cfg.DiskLimitBytes > 0 {
		used, err := m.workspaceUsage(ctx, containerID)
		if err != nil {
			return "", "", -1, fmt.Errorf("check workspace quota: %w", err)
		}
		if used > cfg.DiskLimitBytes {
			return "", "", -1, fmt.Errorf("workspace quota exceeded: %d of %d bytes", used, cfg.DiskLimitBytes)
		}
	}

	stdout, stderr := newBoundedOutput(containerCommandOutputMaxBytes), newBoundedOutput(containerCommandOutputMaxBytes)
	req.Stdout, req.Stderr = stdout, stderr
	exitCode, err := m.driver.Exec(ctx, containerID, req)
	if err != nil {
		return "", "", -1, err
	}

	if cfg.DiskLimitBytes > 0 {
		if used, usageErr := m.workspaceUsage(ctx, containerID); usageErr == nil && used > cfg.DiskLimitBytes {
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

// EnsureFile installs a helper file into the scope's sandbox once per sandbox
// and reports whether it is available at dest. source picks the content for
// the sandbox's platform and returns nil when it has none. It reports false
// without an error when the driver cannot install files or no content matches
// the platform, so the caller can fall back to something else.
func (m *Manager) EnsureFile(ctx context.Context, scopeID string, cfg Config, dest string, mode fs.FileMode, source func(platform string) []byte) (bool, error) {
	installer, ok := m.driver.(FileInstaller)
	if !ok {
		return false, nil
	}
	ctx, handle, finish, err := m.beginUse(ctx, scopeID, cfg)
	if err != nil {
		return false, err
	}
	defer finish()
	if handle == "" {
		return false, fmt.Errorf("container not enabled for scope %s", scopeID)
	}
	if state, known := m.fileState(scopeID, handle, dest); known {
		return state, nil
	}

	m.installMu.Lock()
	defer m.installMu.Unlock()
	if state, known := m.fileState(scopeID, handle, dest); known {
		return state, nil
	}

	installed := false
	platform, err := installer.Platform(ctx, handle)
	switch {
	case err != nil:
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		slog.Warn("container: could not read sandbox platform", "driver", m.driver.Name(), "container_id", shortHandle(handle), "error", err.Error())
	default:
		data := source(platform)
		if data == nil {
			slog.Info("container: no helper for sandbox platform", "path", dest, "platform", platform)
			break
		}
		if err := installer.InstallFile(ctx, handle, dest, data, mode); err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			slog.Warn("container: helper install failed", "driver", m.driver.Name(), "container_id", shortHandle(handle), "path", dest, "error", err.Error())
			break
		}
		installed = true
	}

	m.mu.Lock()
	if info := m.containers[scopeID]; info != nil && info.containerID == handle {
		if info.files == nil {
			info.files = map[string]bool{}
		}
		info.files[dest] = installed
	}
	m.mu.Unlock()
	return installed, nil
}

func (m *Manager) fileState(scopeID, handle, dest string) (installed, known bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info := m.containers[scopeID]
	if info == nil || info.containerID != handle {
		return false, false
	}
	installed, known = info.files[dest]
	return installed, known
}

// ExecPython runs a Python script inside the org's container.
func (m *Manager) ExecPython(ctx context.Context, orgID string, cfg Config, script string, env map[string]string) (string, string, int, error) {
	// Write script to a temp file inside the container and execute
	command := fmt.Sprintf("cat > /tmp/_agent_script.py << 'ENDSCRIPT'\n%s\nENDSCRIPT\npython3 /tmp/_agent_script.py", script)
	return m.Exec(ctx, orgID, cfg, command, env)
}

// StopContainer stops a container for an org. It is deleted unless its
// configuration asks to retain it.
func (m *Manager) StopContainer(ctx context.Context, orgID string) error {
	return m.endScope(ctx, orgID, false)
}

// RemoveScope removes the managed container and its persistent workspace
// volume. Unlike StopContainer, this is destructive and is used only when the
// owning developer space is deleted.
func (m *Manager) RemoveScope(ctx context.Context, scopeID string) error {
	return m.endScope(ctx, scopeID, true)
}

// Shutdown drains backend-owned work before stopping workloads and releasing
// control resources. Failed cleanup retains the local tracking for diagnosis.
func (m *Manager) Shutdown(ctx context.Context) error {
	if driver, ok := m.driver.(ShutdownDriver); ok {
		if err := driver.Shutdown(ctx); err != nil {
			return fmt.Errorf("shut down sandbox backend: %w", err)
		}
		m.mu.Lock()
		clear(m.containers)
		m.mu.Unlock()
		return nil
	}
	m.StopAll(ctx)
	m.mu.RLock()
	remaining := len(m.containers)
	m.mu.RUnlock()
	if remaining != 0 {
		return fmt.Errorf("sandbox shutdown left %d workloads; see backend cleanup errors", remaining)
	}
	return m.Close()
}

// StopAll stops locally tracked containers. Backends with exclusive ownership
// should use Shutdown so remote streams drain before workloads are removed.
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.RLock()
	scopes := make([]string, 0, len(m.containers))
	for scope := range m.containers {
		scopes = append(scopes, scope)
	}
	m.mu.RUnlock()
	for _, orgID := range scopes {
		if err := m.StopContainer(ctx, orgID); err != nil {
			slog.Warn("container: shutdown cleanup failed", "driver", m.driver.Name(), "org_id", orgID, "error", err.Error())
			continue
		}
		slog.Info("container: stopped", "org_id", orgID)
	}
}

// CleanupIdle stops containers that haven't been used for the given duration.
func (m *Manager) CleanupIdle(ctx context.Context, maxIdle time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for orgID, info := range m.containers {
		if !info.stopping && !info.drainFailed && info.active == 0 && now.Sub(info.lastUsed) > maxIdle {
			if err := m.release(ctx, info); err != nil {
				slog.Warn("container: idle cleanup failed", "driver", m.driver.Name(), "org_id", orgID, "error", err.Error())
				continue
			}
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
			"container_id": shortHandle(info.containerID),
			"created_at":   info.createdAt.Format(time.RFC3339),
			"last_used":    info.lastUsed.Format(time.RFC3339),
			"image":        info.config.Image,
			"active":       info.active,
		}
	}
	return result
}

// release ends a sandbox that is no longer needed: stopped when its
// configuration retains it, deleted otherwise.
func (m *Manager) release(ctx context.Context, info *containerInfo) error {
	if !info.config.RetainWhenIdle {
		return m.driver.Remove(ctx, info.containerID)
	}
	return m.driver.Stop(ctx, info.containerID)
}

// workspaceUsage measures /workspace from inside the sandbox, so it works the
// same on every backend.
func (m *Manager) workspaceUsage(ctx context.Context, handle string) (int64, error) {
	out := newBoundedOutput(4096)
	code, err := m.driver.Exec(ctx, handle, ExecRequest{Argv: []string{"du", "-sk", "/workspace"}, Stdout: out, Stderr: out})
	if err != nil {
		return 0, fmt.Errorf("measure workspace: %w", err)
	}
	if code != 0 {
		return 0, fmt.Errorf("measure workspace: du exited %d: %s", code, strings.TrimSpace(out.String()))
	}
	var kib int64
	if _, err := fmt.Sscan(out.String(), &kib); err != nil {
		return 0, fmt.Errorf("parse workspace usage: %w", err)
	}
	return kib * 1024, nil
}

func insideWorkspace(dir string) bool {
	return dir == "/workspace" || strings.HasPrefix(dir, "/workspace/")
}

// DefaultHomePath is where a persistent home is mounted when the owner has not
// chosen a location. It is root's home because the stock images sandboxes run
// have no other user.
const DefaultHomePath = "/root"

// homeReservedPaths cannot hold a home: mounting over them would break the
// image or collide with the workspace volume.
var homeReservedPaths = []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib32", "/lib64", "/libx32", "/proc", "/run", "/sbin", "/sys", "/tmp", "/usr", "/var", "/workspace"}

// ValidHomePath reports whether p may be the mount point of a persistent home:
// a clean absolute directory that is neither the root nor inside a system
// directory or the workspace.
func ValidHomePath(p string) bool {
	if p == "" || len(p) > 256 || !strings.HasPrefix(p, "/") || p == "/" || path.Clean(p) != p {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || r == ':' || r == ',' || r == '\\' {
			return false
		}
	}
	for _, reserved := range homeReservedPaths {
		if p == reserved || strings.HasPrefix(p, reserved+"/") {
			return false
		}
	}
	return true
}

// HomeRemover is implemented by drivers that keep a persistent home outside
// the scope's own storage.
type HomeRemover interface {
	// RemoveHome deletes every sandbox that mounts homeScope's home, then the
	// home itself. A missing home is not an error.
	RemoveHome(ctx context.Context, homeScope string) error
}

// RemoveHome deletes a persistent home. All local scopes mounting that home
// are forgotten only after success. The optional scopes also cover callers
// whose local configuration predates the home setting.
func (m *Manager) RemoveHome(ctx context.Context, homeScope string, scopes ...string) error {
	remover, ok := m.driver.(HomeRemover)
	if !ok {
		return fmt.Errorf("the %s runtime does not support persistent homes", m.driver.Name())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for scope, info := range m.containers {
		if (info.config.HomeScope == homeScope || slices.Contains(scopes, scope)) && (info.active > 0 || info.stopping || info.drainFailed) {
			return errors.New("persistent home is in use; stop its spaces before resetting it")
		}
	}
	if err := remover.RemoveHome(ctx, homeScope); err != nil {
		return fmt.Errorf("remove persistent home: %w", err)
	}
	for scope, info := range m.containers {
		if info.config.HomeScope == homeScope {
			delete(m.containers, scope)
		}
	}
	for _, scope := range scopes {
		delete(m.containers, scope)
	}
	return nil
}

// CopyFile writes data to an absolute path inside the scope's sandbox without
// running anything in it. Unlike command execution it is not limited to
// /workspace, so it is used only for destinations the caller has validated.
func (m *Manager) CopyFile(ctx context.Context, scopeID string, cfg Config, dest string, data []byte, mode fs.FileMode) error {
	installer, ok := m.driver.(FileInstaller)
	if !ok {
		return fmt.Errorf("the %s runtime cannot copy files", m.driver.Name())
	}
	ctx, handle, finish, err := m.beginUse(ctx, scopeID, cfg)
	if err != nil {
		return err
	}
	defer finish()
	if handle == "" {
		return fmt.Errorf("container not enabled for scope %s", scopeID)
	}
	return installer.InstallFile(ctx, handle, dest, data, mode)
}

func shortHandle(handle string) string {
	if len(handle) > 12 {
		return handle[:12]
	}
	return handle
}
