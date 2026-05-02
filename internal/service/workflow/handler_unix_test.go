//go:build !windows

package workflow

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"
)

// TestExecuteBashHandler_KillsChildProcessGroupOnCancel proves the
// process-group kill path actually reaps children. We spawn a bash
// handler that backgrounds a long sleep, captures the child PID,
// cancels the context, and asserts the child is gone within 1s.
//
// Without setProcessGroup + killProcessGroup this test fails: the
// bash parent gets SIGKILL when ctx cancels, but the orphaned sleep
// keeps running until its 30s timer expires.
func TestExecuteBashHandler_KillsChildProcessGroupOnCancel(t *testing.T) {
	pidFile, err := os.CreateTemp("", "handler-pgid-test-*.pid")
	if err != nil {
		t.Fatal(err)
	}
	pidPath := pidFile.Name()
	pidFile.Close()
	defer os.Remove(pidPath)

	// Background `sleep 30` (the "child"), record its PID, then wait so
	// the parent shell is alive when we cancel ctx. The handler returns
	// (only) when SIGKILL hits the whole process group.
	handler := fmt.Sprintf(`
sleep 30 &
CHILD=$!
echo $CHILD > %q
wait $CHILD
`, pidPath)

	ctx, cancel := context.WithCancel(context.Background())

	// Run the handler in a goroutine so we can cancel from the test.
	done := make(chan error, 1)
	go func() {
		_, err := ExecuteBashHandler(ctx, handler, nil, nil, 5*time.Second)
		done <- err
	}()

	// Give the handler time to start, write the PID, and call sleep.
	deadline := time.Now().Add(2 * time.Second)
	var childPid int
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(pidPath)
		if s := strings.TrimSpace(string(data)); s != "" {
			if _, err := fmt.Sscanf(s, "%d", &childPid); err == nil && childPid > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPid == 0 {
		cancel()
		t.Fatal("child PID never written; handler did not start as expected")
	}

	// Sanity: child is alive right now.
	if !pidAlive(childPid) {
		cancel()
		t.Fatalf("child %d already dead before cancel — test setup wrong", childPid)
	}

	cancel()

	// Handler should return promptly after cancel.
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return after ctx cancel within 3s")
	}

	// The child sleep must be reaped within 1s (signal delivery is
	// near-instant; we leave slack for slow CI hosts).
	deadline = time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(childPid) {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	// If we got here the child outlived ctx cancel — the kill-group
	// machinery is broken.
	_ = syscall.Kill(childPid, syscall.SIGKILL) // best-effort cleanup
	t.Fatalf("orphan child %d still alive after ctx cancel; process-group kill failed", childPid)
}

// pidAlive returns true if the process exists. Sending signal 0 doesn't
// actually deliver anything — kernel just reports back whether the pid
// is valid. ESRCH = dead, EPERM = exists but not ours (not our case
// here since the test owns the child).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

// TestExecuteBashHandler_FFmpegSemaphoreSerializes proves the
// process-wide ffmpeg semaphore actually serializes overlapping
// invocations. We squeeze the semaphore down to weight=1 for the
// duration of the test, then fire two handlers that both contain the
// substring "ffmpeg" and each "encode" for ~300ms (sleep). If the cap
// works, the second handler's start must overlap with the first
// handler's end — total wall time ≈ 600ms (serial), not ≈ 300ms
// (parallel).
func TestExecuteBashHandler_FFmpegSemaphoreSerializes(t *testing.T) {
	// Swap in a weight=1 semaphore for this test only.
	orig := ffmpegSem
	ffmpegSem = semaphore.NewWeighted(1)
	defer func() { ffmpegSem = orig }()

	// The handlers contain "ffmpeg" in a comment so handlerNeedsFFmpegSlot
	// returns true without our needing the binary on the test host.
	mkHandler := func(label string) string {
		return fmt.Sprintf(`# fake ffmpeg invocation: ffmpeg -i in.mp4 out.mp4
sleep 0.3
echo %q
`, label)
	}

	ctx := context.Background()
	type result struct {
		out  string
		took time.Duration
		err  error
	}
	results := make(chan result, 2)
	start := time.Now()
	for i, label := range []string{"first", "second"} {
		i, label := i, label
		go func() {
			t0 := time.Now()
			out, err := ExecuteBashHandler(ctx, mkHandler(label), nil, nil, 5*time.Second)
			results <- result{out: out, took: time.Since(t0), err: err}
			_ = i
		}()
	}

	var got []result
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			got = append(got, r)
		case <-time.After(5 * time.Second):
			t.Fatal("handlers did not complete within 5s")
		}
	}
	total := time.Since(start)

	for _, r := range got {
		if r.err != nil {
			t.Fatalf("handler error: %v", r.err)
		}
	}

	// With weight=1 and per-handler sleep=300ms, total wall time must
	// be at least 550ms (give 50ms slack for goroutine scheduling).
	// Parallel execution would land near 300ms.
	if total < 550*time.Millisecond {
		t.Fatalf("ffmpeg semaphore did NOT serialize: total wall time %v "+
			"is too low; expected ≥ 550ms (serial of two 300ms sleeps)", total)
	}
}

// TestHandlerNeedsFFmpegSlot pins the substring matcher behaviour.
func TestHandlerNeedsFFmpegSlot(t *testing.T) {
	cases := map[string]bool{
		"":                             false,
		"echo hello":                   false,
		"ffmpeg -i x.mp4 y.mp4":        true,
		"ffprobe -v error file.mp4":    true,
		"# call ffmpeg later":          true, // matched in comment, deliberate
		"some_ffmpeg_wrapper_bin -i x": true, // also matches, deliberate
		"node script.js":               false,
	}
	for in, want := range cases {
		if got := handlerNeedsFFmpegSlot(in); got != want {
			t.Errorf("handlerNeedsFFmpegSlot(%q): got %v want %v", in, got, want)
		}
	}
}
