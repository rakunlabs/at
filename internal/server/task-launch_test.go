package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestTaskLaunchPreservesRequestRuntime(t *testing.T) {
	for _, name := range []string{"process", "intake", "tool"} {
		t.Run(name, func(t *testing.T) {
			s, tasks, _, _ := consultationFixture(t, &service.LLMResponse{Content: "Delivered", Finished: true})
			s.organizationStore.(*mockOrgStoreForDelegation).orgs["org1"].HeadAgentID = "writer"
			requestCtx, cancelRequest := context.WithCancel(s.ctx)
			defer cancelRequest()
			// Production's lifecycle context does not carry a browser principal.
			s.ctx = context.Background()
			w := httptest.NewRecorder()
			if name == "intake" {
				r := httptest.NewRequestWithContext(requestCtx, http.MethodPost, "/api/v1/organizations/org1/tasks", strings.NewReader(`{"title":"Write"}`))
				r.SetPathValue("id", "org1")
				s.IntakeTaskAPI(w, r)
			} else {
				task, _ := tasks.CreateTask(requestCtx, service.Task{OrganizationID: "org1", AssignedAgentID: "writer", Title: "Write"})
				if name == "tool" {
					if _, err := s.execTaskProcess(requestCtx, map[string]any{"id": task.ID}); err != nil {
						t.Fatal(err)
					}
					w.WriteHeader(http.StatusAccepted)
				} else {
					r := httptest.NewRequestWithContext(requestCtx, http.MethodPost, "/api/v1/tasks/"+task.ID+"/process", nil)
					r.SetPathValue("id", task.ID)
					s.ProcessTaskAPI(w, r)
				}
			}
			cancelRequest()
			if w.Code != http.StatusAccepted {
				t.Fatalf("start: %d %s", w.Code, w.Body)
			}
			for range 200 {
				task, _ := tasks.GetTask(t.Context(), "task-1")
				if task != nil && task.Status == service.TaskStatusDone && !s.isDelegationActive(task.ID) {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			task, _ := tasks.GetTask(t.Context(), "task-1")
			t.Fatalf("accepted task did not finish under request identity: %+v", task)
		})
	}
}

func TestBackgroundChildFollowsOwnerNotToolDeadline(t *testing.T) {
	s := &Server{}
	owner, cleanup, err := s.tryRegisterDelegation(t.Context(), "parent", "agent", "org")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	toolCtx, cancelTool := context.WithCancel(owner)
	child, err := s.reserveDelegationRun(context.WithoutCancel(toolCtx), "child", "agent", "org")
	if err != nil {
		t.Fatal(err)
	}
	defer child.cleanup()
	cancelTool()
	if child.ctx.Err() != nil {
		t.Fatal("child stopped when task_process returned")
	}
	s.cancelDelegation("parent")
	select {
	case <-child.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("background child did not follow owner cancellation")
	}
}

func TestDetachedDelegationStopsWithServer(t *testing.T) {
	lifecycle, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	s := &Server{ctx: lifecycle}
	reservation, err := s.reserveDelegationRun(context.Background(), "task", "agent", "org")
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.cleanup()
	shutdown()
	select {
	case <-reservation.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("detached delegation outlived the server")
	}
}

type liveFailureTaskStore struct{ *mockTaskStoreForDelegation }

func (s *liveFailureTaskStore) UpdateTaskStatus(ctx context.Context, id, status, result string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.mockTaskStoreForDelegation.UpdateTaskStatus(ctx, id, status, result)
}

func TestDelegationFailureState(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		task := service.Task{ID: "child", Status: service.TaskStatusInProgress}
		store := &liveFailureTaskStore{&mockTaskStoreForDelegation{tasks: []service.Task{task}}}
		s := &Server{taskStore: store}
		ctx, cancel := context.WithCancel(t.Context())
		want := service.TaskStatusBlocked
		if cancelled {
			cancel()
			want = service.TaskStatusCancelled
		}
		s.persistDelegationFailure(ctx, &task, errors.New("provider unavailable"))
		cancel()
		updated, _ := store.GetTask(t.Context(), task.ID)
		if updated.Status != want {
			t.Fatalf("failure state = %s, want %s", updated.Status, want)
		}
	}
}
