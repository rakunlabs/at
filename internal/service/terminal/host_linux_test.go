package terminal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

func terminalReadUntil(t *testing.T, a *Attachment, marker string) string {
	t.Helper()
	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var text strings.Builder
		buffer := make([]byte, 8192)
		for text.Len() < 1024*1024 {
			n, err := a.File.Read(buffer)
			text.Write(buffer[:n])
			if strings.Contains(text.String(), marker) || err != nil {
				ch <- result{text.String(), err}
				return
			}
		}
		ch <- result{text.String(), fmt.Errorf("too much output")}
	}()
	select {
	case result := <-ch:
		if !strings.Contains(result.text, marker) {
			t.Fatalf("missing %q: %v: %q", marker, result.err, result.text)
		}
		return result.text
	case <-time.After(5 * time.Second):
		a.Close()
		t.Fatalf("timed out reading %q", marker)
		return ""
	}
}

// Real tmux and PTY coverage without requiring root or modifying a personal
// tmux server. The explicit socket lives only in the test's temporary directory.
func TestTerminalPTYReattach(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "tmux.sock")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", tmuxStartArgs(socket, "/bin/sh")...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "TERM=xterm-256color", "LANG=C.UTF-8"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	attach := func() *Attachment {
		a, err := attachSocket(ctx, socket, account.Username, 100, 35)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(a.Close)
		return a
	}
	a := attach()
	_, _ = a.File.Write([]byte("export AT_TEST_PERSIST=survived; printf 'first-%s\\n' ready\n"))
	terminalReadUntil(t, a, "first-ready")
	if err := a.Resize(90, 40); err != nil {
		t.Fatal(err)
	}
	// SIGWINCH is asynchronous; wait for tmux to propagate the new client size
	// to the inner pane before asking the shell's tty for its dimensions.
	deadline := time.Now().Add(2 * time.Second)
	paneTTY, err := exec.CommandContext(ctx, "tmux", "-S", socket, "display-message", "-p", "-t", "shell", "#{pane_tty}").Output()
	if err != nil {
		t.Fatal(err)
	}
	for {
		out, err := exec.CommandContext(ctx, "stty", "-F", strings.TrimSpace(string(paneTTY)), "size").Output()
		if err == nil && strings.TrimSpace(string(out)) == "40 90" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane did not resize: %q %v", out, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, _ = a.File.Write([]byte("stty size; printf 'size-%s\\n' ready\n"))
	if text := terminalReadUntil(t, a, "size-ready"); !strings.Contains(text, "40 90") {
		t.Fatalf("resize not applied: %q", text)
	}
	a.Close()
	if out, err := exec.CommandContext(ctx, "tmux", "-S", socket, "has-session", "-t", "shell").CombinedOutput(); err != nil {
		t.Fatalf("disconnect killed session: %s %v", out, err)
	}
	b := attach()
	_, _ = b.File.Write([]byte("printf 'persist-%s\\n' \"$AT_TEST_PERSIST\"\n"))
	terminalReadUntil(t, b, "persist-survived")
	for _, args := range [][]string{{"show-options", "-gv", "status"}, {"show-options", "-gv", "prefix"}} {
		out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...).Output()
		if err != nil || (strings.TrimSpace(string(out)) != "off" && strings.TrimSpace(string(out)) != "None") {
			t.Fatalf("tmux UI not hidden: %q %v", out, err)
		}
	}
}

func TestTerminalInvalidIDs(t *testing.T) {
	for _, id := range []string{"", "../../etc/passwd", "-x", "a;touch /tmp/oops", strings.Repeat("Z", 26)} {
		if err := Start(t.Context(), id, "root"); err == nil {
			t.Errorf("Start accepted %q", id)
		}
		if err := Stop(t.Context(), id); err == nil {
			t.Errorf("Stop accepted %q", id)
		}
	}
}

// Opt-in because this creates/stops a real transient system service. It never
// touches the deployed AT service. Run as root on a systemd test host.
func TestTerminalSystemdLifecycle(t *testing.T) {
	if os.Getenv("AT_TEST_HOST_TERMINAL") != "1" {
		t.Skip("set AT_TEST_HOST_TERMINAL=1 on a root/systemd test host")
	}
	if h := LocalHost(); !h.Available {
		t.Fatal(h.Reason)
	}
	id := ulid.Make().String()
	username := os.Getenv("AT_TEST_TERMINAL_USER")
	if username == "" {
		username = "root"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := Start(ctx, id, username); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := Stop(ctx, id); err != nil {
			t.Error(err)
		}
	})
	a, err := Attach(ctx, id, username, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, _ = a.File.Write([]byte("export AT_TEST_STATE=preserved; printf 'unit-%s\\n' ready\n"))
	terminalReadUntil(t, a, "unit-ready")
	a.Close()
	// Idempotent start is what another AT instance/restart sees.
	if err := Start(ctx, id, username); err != nil {
		t.Fatal(err)
	}
	b, err := Attach(ctx, id, username, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	_, _ = b.File.Write([]byte("printf 'state-%s\\n' \"$AT_TEST_STATE\"\n"))
	terminalReadUntil(t, b, "state-preserved")
	if err := Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if Alive(ctx, id) {
		t.Fatal("stopped terminal still alive")
	}
}
