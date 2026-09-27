package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDriver records calls and simulates sandboxes in memory.
type fakeDriver struct {
	mu       sync.Mutex
	seq      int
	running  map[string]bool
	created  []string
	removed  []string
	purged   []string
	stopped  []string
	execs    []ExecRequest
	usage    int64 // bytes reported by `du -sk /workspace`
	exitCode int
	failExec error
}

func newFakeDriver() *fakeDriver { return &fakeDriver{running: map[string]bool{}} }

func (f *fakeDriver) Name() string { return "fake" }

func (f *fakeDriver) Create(_ context.Context, scope string, _ Config) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	handle := fmt.Sprintf("%s-%d", scope, f.seq)
	f.running[handle] = true
	f.created = append(f.created, handle)
	return handle, nil
}

func (f *fakeDriver) Running(_ context.Context, handle string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[handle]
}

func (f *fakeDriver) Exec(_ context.Context, _ string, req ExecRequest) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(req.Argv) > 0 && req.Argv[0] == "du" {
		_, _ = fmt.Fprintf(req.Stdout, "%d\t/workspace\n", f.usage/1024)
		return 0, nil
	}
	f.execs = append(f.execs, req)
	if f.failExec != nil {
		return -1, f.failExec
	}
	if req.Stdin != nil {
		data, _ := io.ReadAll(req.Stdin)
		_, _ = req.Stdout.Write(data)
	} else {
		_, _ = io.WriteString(req.Stdout, strings.Join(req.Argv, " "))
	}
	return f.exitCode, nil
}

func (f *fakeDriver) Attach(context.Context, string, string, uint16, uint16) (Terminal, error) {
	return &fakeTerminal{}, nil
}

func (f *fakeDriver) Remove(_ context.Context, handle string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, handle)
	f.removed = append(f.removed, handle)
	return nil
}

func (f *fakeDriver) Stop(_ context.Context, handle string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.running, handle)
	f.stopped = append(f.stopped, handle)
	return nil
}

func (f *fakeDriver) Purge(_ context.Context, scope string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purged = append(f.purged, scope)
	return nil
}

type fakeTerminal struct {
	closed bool
}

func (t *fakeTerminal) Read([]byte) (int, error)    { return 0, io.EOF }
func (t *fakeTerminal) Write(p []byte) (int, error) { return len(p), nil }
func (t *fakeTerminal) Resize(uint16, uint16) error { return nil }
func (t *fakeTerminal) Close() error                { t.closed = true; return nil }

var enabled = Config{Enabled: true, Image: "img:1"}

func TestManagerReusesAndRecreatesSandboxes(t *testing.T) {
	ctx := context.Background()
	driver := newFakeDriver()
	m := NewWithDriver(driver)

	first, err := m.EnsureContainer(ctx, "scope", enabled)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := m.EnsureContainer(ctx, "scope", enabled); again != first {
		t.Fatalf("a running sandbox with the same config must be reused: %q vs %q", again, first)
	}

	changed := enabled
	changed.Image = "img:2"
	second, _ := m.EnsureContainer(ctx, "scope", changed)
	if second == first || len(driver.removed) != 1 || driver.removed[0] != first {
		t.Fatalf("a config change must replace the sandbox: created=%v removed=%v", driver.created, driver.removed)
	}

	driver.mu.Lock()
	delete(driver.running, second) // the sandbox died on its own
	driver.mu.Unlock()
	if third, _ := m.EnsureContainer(ctx, "scope", changed); third == second {
		t.Fatal("a sandbox that stopped running must be recreated")
	}

	if id, err := m.EnsureContainer(ctx, "other", Config{}); err != nil || id != "" {
		t.Fatalf("a disabled config must not create anything: %q, %v", id, err)
	}
}

func TestManagerRetainsSandboxesWhenAsked(t *testing.T) {
	ctx := context.Background()
	driver := newFakeDriver()
	m := NewWithDriver(driver)
	cfg := enabled
	cfg.RetainWhenIdle = true

	handle, _ := m.EnsureContainer(ctx, "keep", cfg)
	if err := m.StopContainer(ctx, "keep"); err != nil {
		t.Fatal(err)
	}
	if len(driver.stopped) != 1 || driver.stopped[0] != handle || len(driver.removed) != 0 {
		t.Fatalf("a retained sandbox must be stopped, not removed: stopped=%v removed=%v", driver.stopped, driver.removed)
	}

	m.mu.Lock()
	m.containers["keep"] = &containerInfo{containerID: handle, config: cfg, lastUsed: time.Now().Add(-time.Hour)}
	m.mu.Unlock()
	m.CleanupIdle(ctx, time.Minute)
	if len(driver.removed) != 0 {
		t.Fatalf("idle cleanup must stop a retained sandbox, removed=%v", driver.removed)
	}

	// A sandbox that stopped with the same configuration is left for the
	// driver to resume; only a configuration change deletes it.
	driver.mu.Lock()
	driver.running[handle] = false
	driver.mu.Unlock()
	m.mu.Lock()
	m.containers["keep"] = &containerInfo{containerID: handle, config: cfg}
	m.mu.Unlock()
	if _, err := m.EnsureContainer(ctx, "keep", cfg); err != nil {
		t.Fatal(err)
	}
	if len(driver.removed) != 0 {
		t.Fatalf("an unchanged stopped sandbox must not be deleted, removed=%v", driver.removed)
	}

	plain, _ := m.EnsureContainer(ctx, "plain", enabled)
	_ = m.StopContainer(ctx, "plain")
	if len(driver.removed) != 1 || driver.removed[0] != plain {
		t.Fatalf("a sandbox without RetainWhenIdle must be removed, removed=%v", driver.removed)
	}
}

func TestManagerExecPaths(t *testing.T) {
	ctx := context.Background()
	driver := newFakeDriver()
	m := NewWithDriver(driver)

	stdout, _, code, err := m.Exec(ctx, "scope", enabled, "echo hi", map[string]string{"K": "V"})
	if err != nil || code != 0 || stdout != "bash -c echo hi" {
		t.Fatalf("Exec = %q, %d, %v", stdout, code, err)
	}
	if driver.execs[0].Env["K"] != "V" {
		t.Fatal("environment was not passed to the driver")
	}

	stdout, _, _, err = m.ExecArgsInput(ctx, "scope", enabled, "/workspace/p", nil, strings.NewReader("payload"), "cat")
	if err != nil || stdout != "payload" || driver.execs[1].WorkDir != "/workspace/p" {
		t.Fatalf("ExecArgsInput = %q, %v, %+v", stdout, err, driver.execs[1])
	}

	for _, dir := range []string{"/etc", "/workspacex", "relative"} {
		if _, _, _, err := m.ExecArgs(ctx, "scope", enabled, dir, nil, "ls"); err == nil {
			t.Fatalf("work directory %q must be refused", dir)
		}
	}
	if _, _, _, err := m.ExecArgs(ctx, "scope", enabled, "", nil, ""); err == nil {
		t.Fatal("an empty command must be refused")
	}

	driver.exitCode = 3
	if _, _, code, err := m.ExecArgs(ctx, "scope", enabled, "", nil, "false"); err != nil || code != 3 {
		t.Fatalf("a non-zero exit is a result, not an error: %d, %v", code, err)
	}
	driver.failExec = errors.New("backend down")
	if _, _, code, err := m.ExecArgs(ctx, "scope", enabled, "", nil, "true"); err == nil || code != -1 {
		t.Fatalf("a driver failure must surface: %d, %v", code, err)
	}
}

func TestManagerEnforcesWorkspaceQuota(t *testing.T) {
	ctx := context.Background()
	driver := newFakeDriver()
	m := NewWithDriver(driver)
	cfg := enabled
	cfg.DiskLimitBytes = 10 << 20

	driver.usage = 1 << 20
	if _, _, _, err := m.ExecArgs(ctx, "scope", cfg, "", nil, "true"); err != nil {
		t.Fatalf("under quota: %v", err)
	}
	driver.usage = 11 << 20
	_, _, _, err := m.ExecArgs(ctx, "scope", cfg, "", nil, "true")
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("over quota must be refused, got %v", err)
	}
}

func TestManagerLifecycle(t *testing.T) {
	ctx := context.Background()
	driver := newFakeDriver()
	m := NewWithDriver(driver)

	handle, _ := m.EnsureContainer(ctx, "scope", enabled)
	if err := m.RemoveScope(ctx, "scope"); err != nil {
		t.Fatal(err)
	}
	if len(driver.purged) != 1 || driver.purged[0] != "scope" || driver.Running(ctx, handle) {
		t.Fatalf("RemoveScope must remove and purge: removed=%v purged=%v", driver.removed, driver.purged)
	}

	busy, _ := m.EnsureContainer(ctx, "busy", enabled)
	idle, _ := m.EnsureContainer(ctx, "idle", enabled)
	term, err := m.AttachShell(ctx, "busy", enabled, "/workspace", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	for _, info := range m.containers {
		info.lastUsed = time.Now().Add(-time.Hour)
	}
	m.mu.Unlock()
	m.CleanupIdle(ctx, 30*time.Minute)
	if !driver.Running(ctx, busy) || driver.Running(ctx, idle) {
		t.Fatal("idle cleanup must spare an attached shell and remove the idle sandbox")
	}

	if err := term.Close(); err != nil {
		t.Fatal(err)
	}
	_ = term.Close() // idempotent
	m.mu.RLock()
	active := m.containers["busy"].active
	m.mu.RUnlock()
	if active != 0 {
		t.Fatalf("closing the terminal must release the scope, active=%d", active)
	}
	if _, err := m.AttachShell(ctx, "busy", enabled, "/root", 80, 24); err == nil {
		t.Fatal("a shell outside /workspace must be refused")
	}

	m.StopAll(ctx)
	if driver.Running(ctx, busy) || len(m.ListContainers()) != 0 {
		t.Fatal("StopAll must remove every sandbox")
	}
}
