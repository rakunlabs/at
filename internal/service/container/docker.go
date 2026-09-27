package container

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/creack/pty"
)

// dockerDriver runs sandboxes through the docker CLI of the AT host, so
// whatever DOCKER_HOST / docker context points at (a local, rootless or
// remote daemon) is the backend.
type dockerDriver struct{}

// NewDockerDriver returns the Docker CLI driver.
func NewDockerDriver() Driver {
	return newDockerDriver()
}

func newDockerDriver() *dockerDriver {
	return &dockerDriver{}
}

func (d *dockerDriver) Name() string { return "docker" }

func scopeHash(scope string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(scope)))[:20]
}

func dockerContainerName(scope string) string { return "at-scope-" + scopeHash(scope) }
func dockerVolumeName(scope string) string    { return "at-space-" + scopeHash(scope) }

// dockerConfigLabel records the configuration a container was created with,
// so a leftover container is resumed only when nothing has changed since.
func dockerConfigLabel(cfg Config) string {
	encoded, _ := json.Marshal(cfg)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:8])
}

func (d *dockerDriver) Create(ctx context.Context, scope string, cfg Config) (string, error) {
	image := cfg.Image
	if image == "" {
		image = DefaultConfig().Image
	}
	if strings.HasPrefix(image, "-") || strings.ContainsAny(image, " \t\r\n") {
		return "", fmt.Errorf("invalid image reference %q", image)
	}

	if cfg.PreferRootless {
		// Rootless Docker is recommended, not required: a rootful daemon still
		// gets the dropped capabilities, no-new-privileges and quotas below.
		rootless, err := dockerIsRootless(ctx)
		switch {
		case err != nil:
			slog.Warn("container: could not determine whether docker is rootless", "scope", scope, "error", err.Error())
		case !rootless:
			slog.Warn("container: docker is not running in rootless mode; continuing with a rootful daemon (rootless Docker is recommended for stronger isolation)", "scope", scope)
		}
	}

	name := dockerContainerName(scope)
	configLabel := dockerConfigLabel(cfg)
	args := []string{
		"run", "-d",
		"--name", name,
		"--label", "at.scope.hash=" + scopeHash(scope),
		"--label", "at.managed=true",
		"--label", "at.config=" + configLabel,
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
		args = append(args, "-v", dockerVolumeName(scope)+":/workspace")
	} else {
		tmpfs := "/workspace:rw,nosuid,nodev"
		if cfg.DiskLimitBytes > 0 {
			tmpfs += fmt.Sprintf(",size=%d", cfg.DiskLimitBytes)
		}
		args = append(args, "--tmpfs", tmpfs)
	}

	if cfg.KeepAlive {
		// Stock images exit at once under -d (debian's default command is an
		// interactive bash); every command reaches the container through exec.
		args = append(args, "--entrypoint", "sleep", image, "infinity")
	} else {
		args = append(args, image)
	}

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
		// The scope already has a container: a stopped one kept on purpose, or
		// a leftover from an earlier process. Resume it only when it was created
		// with this exact configuration; otherwise a change (a new image, other
		// limits) would be ignored until someone removed it by hand.
		var existing bytes.Buffer
		inspect := exec.CommandContext(ctx, "docker", "inspect", "-f", `{{.Id}} {{index .Config.Labels "at.config"}}`, name)
		inspect.Stdout = &existing
		if inspect.Run() == nil {
			id, existingLabel, _ := strings.Cut(strings.TrimSpace(existing.String()), " ")
			if existingLabel == configLabel && exec.CommandContext(ctx, "docker", "start", name).Run() == nil {
				d.allowPackageInstalls(ctx, id)
				return id, nil
			}
			_ = d.Remove(ctx, name)
			stdout, stderr, err = run()
		}
	}
	if err != nil {
		return "", fmt.Errorf("docker run: %s: %w", strings.TrimSpace(stderr), err)
	}
	id := strings.TrimSpace(stdout)
	d.allowPackageInstalls(ctx, id)
	return id, nil
}

// aptSandboxConfig lets apt run inside a container with every capability
// dropped. apt normally downloads as the unprivileged `_apt` user, and
// switching to it needs CAP_SETUID, which the container does not have; the
// download then dies with "Method http has died unexpectedly". Downloading as
// root inside the sandbox is the same trust level as the shell the user
// already has, and it keeps the capability set empty. apk and dnf need no
// equivalent.
const aptSandboxConfig = `if [ -d /etc/apt/apt.conf.d ]; then printf 'APT::Sandbox::User "root";\n' > /etc/apt/apt.conf.d/99at-sandbox; fi`

func (d *dockerDriver) allowPackageInstalls(ctx context.Context, handle string) {
	output, err := exec.CommandContext(ctx, "docker", "exec", "-u", "0", handle, "sh", "-c", aptSandboxConfig).CombinedOutput()
	if err != nil {
		slog.Warn("container: could not configure apt for the sandbox", "container_id", shortHandle(handle), "error", strings.TrimSpace(string(output)))
	}
}

func (d *dockerDriver) Running(ctx context.Context, handle string) bool {
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", handle).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func (d *dockerDriver) Exec(ctx context.Context, handle string, req ExecRequest) (int, error) {
	if len(req.Argv) == 0 {
		return -1, errors.New("command is required")
	}
	args := []string{"exec"}
	if req.Stdin != nil {
		args = append(args, "-i")
	}
	for key, value := range req.Env {
		args = append(args, "-e", key+"="+value)
	}
	if req.WorkDir != "" {
		args = append(args, "-w", req.WorkDir)
	}
	args = append(args, handle)
	args = append(args, req.Argv...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = req.Stdin
	cmd.Stdout = req.Stdout
	cmd.Stderr = req.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return -1, fmt.Errorf("docker exec: %w", err)
	}
	return 0, nil
}

func (d *dockerDriver) Attach(ctx context.Context, handle string, workDir string, cols, rows uint16) (Terminal, error) {
	// Prefer bash, but a shell must open in images that ship only sh (alpine).
	cmd := exec.CommandContext(ctx, "docker", "exec", "-it", "-w", workDir, handle,
		"sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash -l; fi; exec sh -l")
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, fmt.Errorf("attach container shell: %w", err)
	}
	return &ptyTerminal{file: file, cmd: cmd}, nil
}

func (d *dockerDriver) Stop(ctx context.Context, handle string) error {
	output, err := exec.CommandContext(ctx, "docker", "stop", "-t", "5", handle).CombinedOutput()
	if err != nil && !strings.Contains(string(output), "No such container") {
		return fmt.Errorf("stop managed container: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (d *dockerDriver) Remove(ctx context.Context, handle string) error {
	output, err := exec.CommandContext(ctx, "docker", "rm", "-f", handle).CombinedOutput()
	if err != nil && !strings.Contains(string(output), "No such container") {
		return fmt.Errorf("remove managed container: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func (d *dockerDriver) Purge(ctx context.Context, scope string) error {
	if err := d.Remove(ctx, dockerContainerName(scope)); err != nil {
		return err
	}
	if output, err := exec.CommandContext(ctx, "docker", "volume", "rm", dockerVolumeName(scope)).CombinedOutput(); err != nil && !strings.Contains(string(output), "No such volume") {
		return fmt.Errorf("remove managed volume: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func dockerIsRootless(ctx context.Context) (bool, error) {
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{json .SecurityOptions}}").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("inspect docker security options: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return strings.Contains(strings.ToLower(string(out)), "rootless"), nil
}

// ptyTerminal is a Terminal backed by a local PTY driving a CLI process.
type ptyTerminal struct {
	file *os.File
	cmd  *exec.Cmd
	once sync.Once
	err  error
}

func (t *ptyTerminal) Read(p []byte) (int, error)  { return t.file.Read(p) }
func (t *ptyTerminal) Write(p []byte) (int, error) { return t.file.Write(p) }

func (t *ptyTerminal) Resize(cols, rows uint16) error {
	return pty.Setsize(t.file, &pty.Winsize{Cols: cols, Rows: rows})
}

func (t *ptyTerminal) Close() error {
	t.once.Do(func() {
		_ = t.file.Close()
		if t.cmd.Process != nil {
			_ = t.cmd.Process.Kill()
		}
		t.err = t.cmd.Wait()
	})
	return t.err
}
