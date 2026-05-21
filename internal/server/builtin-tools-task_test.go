package server

import (
	"context"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// taskInheritStore is a focused mock for execTaskCreate inheritance tests.
// It records every CreateTask call and answers GetTask from a preset map.
type taskInheritStore struct {
	mockTaskStore
	preset  map[string]*service.Task
	created []service.Task
}

func (m *taskInheritStore) GetTask(_ context.Context, id string) (*service.Task, error) {
	if t, ok := m.preset[id]; ok {
		return t, nil
	}
	return nil, nil
}

func (m *taskInheritStore) CreateTask(_ context.Context, task service.Task) (*service.Task, error) {
	if task.ID == "" {
		task.ID = "task-" + task.Title
	}
	m.created = append(m.created, task)
	t := task
	return &t, nil
}

func newTaskInheritServer(parent *service.Task) (*Server, *taskInheritStore) {
	store := &taskInheritStore{
		preset: map[string]*service.Task{},
	}
	if parent != nil {
		store.preset[parent.ID] = parent
	}
	return &Server{taskStore: store}, store
}

// TestExecTaskCreate_InheritsParentAndOrgFromContext verifies that when an
// agent calls task_create from inside a delegation loop without supplying
// parent_id / organization_id, both fields are auto-inherited from the
// currently-executing task. This guards the bug where Content Director
// produced an orphaned "Graphic Design — Baby Animals Images" task.
func TestExecTaskCreate_InheritsParentAndOrgFromContext(t *testing.T) {
	parent := &service.Task{
		ID:             "parent-1",
		OrganizationID: "org-yts",
		Title:          "Re-produce Baby Animals Short",
	}
	s, store := newTaskInheritServer(parent)

	ctx := contextWithTaskID(context.Background(), parent.ID)
	out, err := s.execTaskCreate(ctx, map[string]any{
		"title":       "Graphic Design — Baby Animals Images",
		"description": "Produce all 7 scene images",
	})
	if err != nil {
		t.Fatalf("execTaskCreate returned error: %v", err)
	}
	if !strings.Contains(out, "Graphic Design") {
		t.Fatalf("unexpected result payload: %s", out)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 created task, got %d", len(store.created))
	}
	got := store.created[0]
	if got.ParentID != parent.ID {
		t.Errorf("parent_id not inherited: got %q, want %q", got.ParentID, parent.ID)
	}
	if got.OrganizationID != parent.OrganizationID {
		t.Errorf("organization_id not inherited: got %q, want %q", got.OrganizationID, parent.OrganizationID)
	}
}

// TestExecTaskCreate_RejectsDifferentParentInTaskContext verifies that a
// task-context task_create cannot silently create work under another parent.
func TestExecTaskCreate_RejectsDifferentParentInTaskContext(t *testing.T) {
	parent := &service.Task{
		ID:             "parent-1",
		OrganizationID: "org-yts",
	}
	s, _ := newTaskInheritServer(parent)

	ctx := contextWithTaskID(context.Background(), parent.ID)
	_, err := s.execTaskCreate(ctx, map[string]any{
		"title":           "Cross-org subtask",
		"parent_id":       "other-parent",
		"organization_id": "other-org",
	})
	if err == nil {
		t.Fatal("expected error for different parent_id in task context")
	}
}

// TestExecTaskCreate_NoCurrentTaskNoInheritance confirms that without a
// task ID in context, no inheritance happens and the task is created as-is
// (the legacy behavior).
func TestExecTaskCreate_NoCurrentTaskNoInheritance(t *testing.T) {
	s, store := newTaskInheritServer(nil)

	_, err := s.execTaskCreate(context.Background(), map[string]any{
		"title": "Standalone task",
	})
	if err != nil {
		t.Fatalf("execTaskCreate returned error: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 created task, got %d", len(store.created))
	}
	got := store.created[0]
	if got.ParentID != "" || got.OrganizationID != "" {
		t.Errorf("expected empty parent/org without context, got parent=%q org=%q",
			got.ParentID, got.OrganizationID)
	}
}

func TestExecTaskCreate_AllowsExplicitRootWithReason(t *testing.T) {
	parent := &service.Task{
		ID:             "parent-1",
		OrganizationID: "org-yts",
		ProjectID:      "project-1",
	}
	s, store := newTaskInheritServer(parent)

	ctx := contextWithTaskID(context.Background(), parent.ID)
	_, err := s.execTaskCreate(ctx, map[string]any{
		"title":  "Independent follow-up",
		"root":   true,
		"reason": "User explicitly asked for a separate task",
	})
	if err != nil {
		t.Fatalf("execTaskCreate returned error: %v", err)
	}
	got := store.created[0]
	if got.ParentID != "" {
		t.Errorf("root task should not have parent_id, got %q", got.ParentID)
	}
	if got.OrganizationID != parent.OrganizationID {
		t.Errorf("organization_id should have been inherited for root task: got %q want %q",
			got.OrganizationID, parent.OrganizationID)
	}
	if got.ProjectID != parent.ProjectID {
		t.Errorf("project_id should have been inherited for root task: got %q want %q", got.ProjectID, parent.ProjectID)
	}
}

func TestExecTaskCreate_RootRequiresReasonInTaskContext(t *testing.T) {
	parent := &service.Task{ID: "parent-1"}
	s, _ := newTaskInheritServer(parent)

	ctx := contextWithTaskID(context.Background(), parent.ID)
	_, err := s.execTaskCreate(ctx, map[string]any{
		"title": "Independent follow-up",
		"root":  true,
	})
	if err == nil {
		t.Fatal("expected root=true without reason to fail")
	}
}

func TestExecTaskCreateChild_RequiresTaskContext(t *testing.T) {
	s, _ := newTaskInheritServer(nil)

	_, err := s.execTaskCreateChild(context.Background(), map[string]any{
		"title": "Child work",
	})
	if err == nil {
		t.Fatal("expected task_create_child outside task context to fail")
	}
}

// TestTaskIDFromContext_RoundTrip is a minimal sanity test for the new
// context helpers.
func TestTaskIDFromContext_RoundTrip(t *testing.T) {
	if got := taskIDFromContext(context.Background()); got != "" {
		t.Fatalf("empty context should return empty string, got %q", got)
	}
	ctx := contextWithTaskID(context.Background(), "task-xyz")
	if got := taskIDFromContext(ctx); got != "task-xyz" {
		t.Fatalf("expected round-trip task-xyz, got %q", got)
	}
}
