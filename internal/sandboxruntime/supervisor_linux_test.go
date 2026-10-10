package sandboxruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

func buildLauncher(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "at-sandbox")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/at-sandbox")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build launcher: %v: %s", err, out)
	}
	return bin
}

func testRunID(t *testing.T) string {
	t.Helper()
	id := strings.ToLower(rand.Text())
	dir, _ := runDirectory(id)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return id
}

func TestSupervisorPreservesArgumentsEnvironmentAndExit(t *testing.T) {
	bin := buildLauncher(t)
	root := t.TempDir()
	env, _ := json.Marshal(map[string]string{"AT_TEST_VALUE": "; echo not-executed"})
	cmd := exec.Command(bin, "run", testRunID(t), root, string(env), "sh", "-c", `printf '%s\n%s\n%s' "$PWD" "$AT_TEST_VALUE" "$1"; exit 7`, "sh", "literal; value")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit status not preserved: %v %s", err, out)
	}
	if string(out) != root+"\n; echo not-executed\nliteral; value" {
		t.Fatalf("argv/env changed: %q", out)
	}
}

func TestSupervisorCancellationAndLateStart(t *testing.T) {
	bin := buildLauncher(t)
	id := testRunID(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	marker := filepath.Join(t.TempDir(), "started")
	cmd := exec.CommandContext(ctx, bin, "run", id, "", "{}", "sh", "-c", `sleep 60 & echo ready > "$1"; wait`, "sh", marker)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	dir, _ := runDirectory(id)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, markerErr := os.Stat(marker)
		_, identityErr := os.Stat(filepath.Join(dir, "process"))
		if markerErr == nil && identityErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervisor did not register its command")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if out, err := exec.CommandContext(ctx, bin, "cancel", id).CombinedOutput(); err != nil {
		t.Fatalf("cancel: %v %s", err, out)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("cancelled command succeeded")
	}
	late := testRunID(t)
	if err := cancelRun(late); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "must-not-exist")
	if err := exec.CommandContext(ctx, bin, "run", late, "", "{}", "touch", file).Run(); err == nil {
		t.Fatal("a late exec ignored cancellation")
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled late command had side effects")
	}
}

func TestSupervisorInteractiveTerminal(t *testing.T) {
	bin := buildLauncher(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "run-shell", testRunID(t), "", "{}")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer ptmx.Close()
	// Interactive bash must own the foreground TTY and accept commands.
	_, _ = ptmx.Write([]byte("echo at-terminal-ready\nexit\n"))
	var out bytes.Buffer
	readDone := make(chan struct{})
	go func() { _, _ = out.ReadFrom(ptmx); close(readDone) }()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("terminal shell: %v", err)
	}
	_ = ptmx.Close()
	<-readDone
	if !strings.Contains(out.String(), "at-terminal-ready") || strings.Contains(out.String(), "no job control") {
		t.Fatalf("terminal control broken: %s", out.String())
	}
}
