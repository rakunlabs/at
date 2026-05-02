package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
	"github.com/rakunlabs/query"
)

// janitorTaskStore is a minimal in-memory taskStore for janitor tests.
// Only GetTask is exercised; everything else is a stub.
type janitorTaskStore struct {
	tasks map[string]*service.Task
}

func (s *janitorTaskStore) GetTask(_ context.Context, id string) (*service.Task, error) {
	return s.tasks[id], nil
}

// Stubs for interface compliance.
func (s *janitorTaskStore) ListTasks(_ context.Context, _ *query.Query) (*service.ListResult[service.Task], error) {
	return nil, nil
}
func (s *janitorTaskStore) CreateTask(_ context.Context, _ service.Task) (*service.Task, error) {
	return nil, nil
}
func (s *janitorTaskStore) UpdateTask(_ context.Context, _ string, _ service.Task) (*service.Task, error) {
	return nil, nil
}
func (s *janitorTaskStore) DeleteTask(_ context.Context, _ string) error      { return nil }
func (s *janitorTaskStore) CheckoutTask(_ context.Context, _, _ string) error { return nil }
func (s *janitorTaskStore) ReleaseTask(_ context.Context, _ string) error     { return nil }
func (s *janitorTaskStore) ListTasksByAgent(_ context.Context, _ string) ([]service.Task, error) {
	return nil, nil
}
func (s *janitorTaskStore) ListTasksByGoal(_ context.Context, _ string) ([]service.Task, error) {
	return nil, nil
}
func (s *janitorTaskStore) ListChildTasks(_ context.Context, _ string) ([]service.Task, error) {
	return nil, nil
}
func (s *janitorTaskStore) UpdateTaskStatus(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

// TestSweepWorkspaceOnce verifies the janitor's three core decisions:
//  1. terminal-status task older than TTL → removed
//  2. terminal-status task younger than TTL → kept
//  3. non-terminal task (e.g. in_progress) → kept regardless of age
//  4. unknown task id (not in store) → kept (don't nuke stranger data)
//  5. .at-tool-output dump dir older than TTL by mtime → removed
//  6. .at-tool-output dump dir newer than TTL → kept
func TestSweepWorkspaceOnce(t *testing.T) {
	root := t.TempDir()
	ttl := 1 * time.Hour
	now := time.Now()

	// Build the on-disk tree.
	cases := []struct {
		dir    string
		create bool
	}{
		{"task-old-done", true},    // case 1
		{"task-young-done", true},  // case 2
		{"task-old-running", true}, // case 3
		{"task-unknown", true},     // case 4
	}
	for _, c := range cases {
		if c.create {
			mustMkdir(t, filepath.Join(root, c.dir))
			// Drop a marker file so dirSize reports something.
			mustWriteFile(t, filepath.Join(root, c.dir, "data.bin"), 100)
		}
	}

	// Tool-output dumps. Touch the parent + two child run dirs to fixed mtimes.
	dumpRoot := filepath.Join(root, ".at-tool-output")
	mustMkdir(t, dumpRoot)
	oldDump := filepath.Join(dumpRoot, "run-old")
	newDump := filepath.Join(dumpRoot, "run-young")
	mustMkdir(t, oldDump)
	mustMkdir(t, newDump)
	mustWriteFile(t, filepath.Join(oldDump, "tool-1.txt"), 50)
	mustWriteFile(t, filepath.Join(newDump, "tool-1.txt"), 50)
	// Set mtimes: old = 2h ago, new = 5min ago.
	old := now.Add(-2 * time.Hour)
	young := now.Add(-5 * time.Minute)
	if err := os.Chtimes(oldDump, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newDump, young, young); err != nil {
		t.Fatal(err)
	}

	// Wire a janitor server with the matching task store.
	store := &janitorTaskStore{tasks: map[string]*service.Task{
		"task-old-done": {
			ID:          "task-old-done",
			Status:      service.TaskStatusDone,
			CompletedAt: now.Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		},
		"task-young-done": {
			ID:          "task-young-done",
			Status:      service.TaskStatusDone,
			CompletedAt: now.Add(-5 * time.Minute).UTC().Format(time.RFC3339),
		},
		"task-old-running": {
			ID:        "task-old-running",
			Status:    service.TaskStatusInProgress,
			UpdatedAt: now.Add(-3 * time.Hour).UTC().Format(time.RFC3339),
		},
		// "task-unknown" intentionally omitted.
	}}

	s := &Server{
		taskStore: store,
		loopGov:   loopgov.New(loopgov.Config{WorkspaceRoot: root, WorkspaceTTL: ttl}, nil),
	}

	s.sweepWorkspaceOnce(context.Background(), root, ttl)

	// Verify outcomes.
	assertExists := func(name string, want bool) {
		t.Helper()
		_, err := os.Stat(filepath.Join(root, name))
		exists := err == nil
		if exists != want {
			t.Errorf("%s: exists=%v want=%v (err=%v)", name, exists, want, err)
		}
	}
	assertExists("task-old-done", false)   // case 1: removed
	assertExists("task-young-done", true)  // case 2: kept (TTL not elapsed)
	assertExists("task-old-running", true) // case 3: kept (not terminal)
	assertExists("task-unknown", true)     // case 4: kept (unknown id)

	// Tool-output dumps:
	if _, err := os.Stat(oldDump); !os.IsNotExist(err) {
		t.Errorf("old dump dir should be removed; stat err=%v", err)
	}
	if _, err := os.Stat(newDump); err != nil {
		t.Errorf("young dump dir should be kept; got err=%v", err)
	}
}

// TestSweepWorkspaceOnce_NoRoot ensures missing root is a debug log,
// not a panic. /tmp/at-tasks may not exist at the moment the janitor
// runs on a fresh server.
func TestSweepWorkspaceOnce_NoRoot(t *testing.T) {
	s := &Server{
		taskStore: &janitorTaskStore{tasks: map[string]*service.Task{}},
		loopGov:   loopgov.New(loopgov.Config{}, nil),
	}
	// Should not panic even when the dir does not exist.
	s.sweepWorkspaceOnce(context.Background(),
		"/tmp/janitor-test-does-not-exist-"+t.Name(), time.Hour)
}

// TestStartWorkspaceJanitor_DisabledByNegativeTTL pins the explicit
// opt-out behaviour. With WorkspaceTTL < 0, no sweep ever runs and
// the goroutine returns immediately.
func TestStartWorkspaceJanitor_DisabledByNegativeTTL(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "task-old")
	mustMkdir(t, stale)

	// We can't easily observe the goroutine's exit, but we can check
	// that startWorkspaceJanitor with a disabled config does not
	// remove the stale dir even after a beat.
	s := &Server{
		taskStore: &janitorTaskStore{tasks: map[string]*service.Task{
			"task-old": {
				ID:          "task-old",
				Status:      service.TaskStatusDone,
				CompletedAt: time.Now().Add(-100 * time.Hour).UTC().Format(time.RFC3339),
			},
		}},
		loopGov: loopgov.New(loopgov.Config{
			WorkspaceRoot: root,
			WorkspaceTTL:  -1, // explicit disable
		}, nil),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startWorkspaceJanitor(ctx)

	time.Sleep(100 * time.Millisecond) // give the (no-op) goroutine a chance
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("disabled janitor should not have removed task-old; err=%v", err)
	}
}

// TestPickTerminalTime walks through the precedence (Completed →
// Cancelled → Updated → zero).
func TestPickTerminalTime(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	earlier := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	tests := []struct {
		name string
		in   service.Task
		zero bool
	}{
		{"completed wins", service.Task{CompletedAt: now, CancelledAt: earlier, UpdatedAt: earlier}, false},
		{"cancelled when no completed", service.Task{CancelledAt: now, UpdatedAt: earlier}, false},
		{"falls back to updated", service.Task{UpdatedAt: now}, false},
		{"all empty", service.Task{}, true},
		{"unparseable string", service.Task{CompletedAt: "yesterday"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickTerminalTime(&tt.in)
			if got.IsZero() != tt.zero {
				t.Fatalf("got %v zero=%v want zero=%v", got, got.IsZero(), tt.zero)
			}
		})
	}
}

// ─── helpers ───

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWriteFile(t *testing.T, p string, n int) {
	t.Helper()
	buf := make([]byte, n)
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}
