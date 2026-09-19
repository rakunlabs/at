package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// writeFakeMCP writes a minimal MCP stdio server: it answers the initialize
// handshake (always request ID 1 on a fresh client) and then keeps reading
// until stdin closes.
func writeFakeMCP(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stdio MCP fake server requires /bin/sh")
	}
	path := filepath.Join(t.TempDir(), "fake-mcp.sh")
	script := `#!/bin/sh
while read line; do
  case "$line" in
    *'"method":"initialize"'*) printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"fake","version":"1.0"}}}\n' ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake mcp: %v", err)
	}
	return path
}

func newTestManager(t *testing.T) *StdioProcessManager {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	m := NewStdioProcessManager(ctx)
	t.Cleanup(func() {
		m.Close()
		cancel()
	})
	return m
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStdioManagerReuseAndEnvChange(t *testing.T) {
	script := writeFakeMCP(t)
	m := newTestManager(t)
	upstream := MCPUpstream{Command: script, Env: map[string]string{"A": "1"}}

	if _, err := m.GetOrCreate(upstream); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	st := m.StatusFor(upstream)
	if st == nil || !st.Alive || st.PID <= 0 {
		t.Fatalf("StatusFor after start = %+v, want alive with pid", st)
	}
	firstPID := st.PID

	// Same env: reuse the process.
	if _, err := m.GetOrCreate(upstream); err != nil {
		t.Fatalf("GetOrCreate reuse: %v", err)
	}
	if st := m.StatusFor(upstream); st == nil || st.PID != firstPID {
		t.Fatalf("same env should reuse process: got %+v, want pid %d", st, firstPID)
	}

	// Changed env: same cache key (command+args), but the old process runs
	// with stale env — it must be replaced.
	changed := MCPUpstream{Command: script, Env: map[string]string{"A": "2"}}
	if _, err := m.GetOrCreate(changed); err != nil {
		t.Fatalf("GetOrCreate changed env: %v", err)
	}
	st = m.StatusFor(changed)
	if st == nil || !st.Alive || st.PID == firstPID {
		t.Fatalf("changed env should respawn: got %+v, old pid %d", st, firstPID)
	}

	if got := len(m.Statuses()); got != 1 {
		t.Fatalf("Statuses() len = %d, want 1", got)
	}
}

func TestStdioManagerCrashDetection(t *testing.T) {
	script := writeFakeMCP(t)
	m := newTestManager(t)
	upstream := MCPUpstream{Command: script}

	if _, err := m.GetOrCreate(upstream); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	st := m.StatusFor(upstream)
	if st == nil || !st.Alive {
		t.Fatalf("StatusFor = %+v, want alive", st)
	}

	// Kill the child behind the manager's back. The reaper goroutine must
	// notice: previously Alive() only flipped after an explicit Close(), so
	// a self-crashed process kept being handed out.
	if err := syscall.Kill(st.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill: %v", err)
	}
	waitFor(t, "crash detection", func() bool {
		st := m.StatusFor(upstream)
		return st != nil && !st.Alive
	})

	// Next acquisition spawns a fresh process.
	if _, err := m.GetOrCreate(upstream); err != nil {
		t.Fatalf("GetOrCreate after crash: %v", err)
	}
	fresh := m.StatusFor(upstream)
	if fresh == nil || !fresh.Alive || fresh.PID == st.PID {
		t.Fatalf("after crash want fresh process, got %+v (old pid %d)", fresh, st.PID)
	}
}

func TestStdioManagerStopAndRestart(t *testing.T) {
	script := writeFakeMCP(t)
	m := newTestManager(t)
	upstream := MCPUpstream{Command: script}

	if m.Stop(upstream) {
		t.Fatal("Stop before start should report false")
	}

	if _, err := m.GetOrCreate(upstream); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	first := m.StatusFor(upstream)
	if first == nil || !first.Alive {
		t.Fatalf("StatusFor = %+v, want alive", first)
	}

	if !m.Stop(upstream) {
		t.Fatal("Stop should report true for a managed process")
	}
	if st := m.StatusFor(upstream); st != nil {
		t.Fatalf("StatusFor after Stop = %+v, want nil", st)
	}

	st, err := m.Restart(upstream)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if st == nil || !st.Alive || st.PID == first.PID {
		t.Fatalf("Restart status = %+v, want fresh alive process (old pid %d)", st, first.PID)
	}

	// Restart with a broken command surfaces the failure immediately.
	if _, err := m.Restart(MCPUpstream{Command: filepath.Join(t.TempDir(), "missing-binary")}); err == nil {
		t.Fatal("Restart of a missing binary should fail")
	}
}
