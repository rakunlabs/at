package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

type runtimeCleanupFixture struct {
	service.ProviderStorer
	tasks map[string]*service.Task
	calls int
}

func (f *runtimeCleanupFixture) GetExecutionCleanupTask(ctx context.Context, workspace, id string) (*service.Task, error) {
	if !service.HasExecutionMaintenance(ctx) {
		return nil, service.ErrExecutionDenied
	}
	f.calls++
	return f.tasks[workspace+"/"+id], nil
}

func TestRuntimeJanitorOwnershipAndSymlinkContainment(t *testing.T) {
	base := t.TempDir()
	now := time.Now().UTC()
	for _, dir := range []string{"a/tasks/shared/nested", "a/tasks/unknown", "a/assets", "b/tasks/shared", "b/assets"} {
		if err := os.MkdirAll(filepath.Join(base, "workspaces", dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(base, "workspaces/b/assets/retained")
	if err := os.WriteFile(secret, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "workspaces/b/assets"), filepath.Join(base, "workspaces/a/tasks/shared/nested/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "workspaces/b"), filepath.Join(base, "workspaces/alias")); err != nil {
		t.Fatal(err)
	}
	store := &runtimeCleanupFixture{tasks: map[string]*service.Task{
		"a/shared": {ID: "shared", Status: "completed", CompletedAt: now.Add(-48 * time.Hour).Format(time.RFC3339)},
		"b/shared": {ID: "shared", Status: "in_progress"},
	}}
	s := &Server{store: store}
	if s.sweepExecutionWorkspaces(t.Context(), base, now, 24*time.Hour) != 0 || store.calls != 0 {
		t.Fatal("unbound janitor acquired installation authority")
	}
	if count := s.sweepExecutionWorkspaces(service.WithExecutionMaintenance(t.Context()), base, now, 24*time.Hour); count != 1 {
		t.Fatalf("removed %d, want one owned terminal task", count)
	}
	if _, err := os.Stat(filepath.Join(base, "workspaces/a/tasks/shared")); !os.IsNotExist(err) {
		t.Fatalf("terminal task remained: %v", err)
	}
	for _, path := range []string{secret, filepath.Join(base, "workspaces/a/assets"), filepath.Join(base, "workspaces/a/tasks/unknown"), filepath.Join(base, "workspaces/b/tasks/shared")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("deleted protected path %s: %v", path, err)
		}
	}
}
