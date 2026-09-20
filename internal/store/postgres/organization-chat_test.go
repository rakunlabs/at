package postgres

import (
	"context"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestOrganizationChatAtomicTaskSelection(t *testing.T) {
	p, base, workspace, actor := workspaceFixture(t)
	agent, err := p.CreateAgent(base, service.Agent{Name: "chat-director"})
	if err != nil {
		t.Fatal(err)
	}
	org, err := p.CreateOrganization(base, service.Organization{Name: "chat-production", HeadAgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	session, err := p.CreateChatSession(base, service.ChatSession{AgentID: agent.ID, OrganizationID: org.ID, WorkspaceID: workspace.ID, OwnerUserID: actor.ID, Config: service.ChatSessionConfig{OrganizationChat: true}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := service.BindExecution(base, service.ExecutionProvenance{RunID: "chat-run", UserID: actor.ID, WorkspaceID: workspace.ID, Source: "test"}, t.TempDir(), func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
		return service.ExecutionValidation{Allowed: true, Policy: service.ExecutionPolicy{WorkspaceID: workspace.ID}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	task := service.Task{Title: "One video", OrganizationID: org.ID, AssignedAgentID: agent.ID, Status: service.TaskStatusTodo}
	type answer struct {
		task    *service.Task
		created bool
		err     error
	}
	answers := make(chan answer, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, created, err := p.CreateOrganizationChatTask(ctx, session.ID, task, false)
			answers <- answer{task, created, err}
		}()
	}
	wg.Wait()
	close(answers)
	createdCount, selected := 0, ""
	for result := range answers {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			createdCount++
		}
		if selected != "" && result.task.ID != selected {
			t.Fatal("concurrent turns created different tasks")
		}
		selected = result.task.ID
	}
	if createdCount != 1 {
		t.Fatalf("created %d jobs, want 1", createdCount)
	}
	saved, err := p.GetChatSession(ctx, session.ID)
	if err != nil || saved.Config.ActiveTaskID != selected {
		t.Fatalf("task link not durable: %+v %v", saved, err)
	}
	provenance, err := p.GetExecutionProvenance(ctx, "task/"+selected)
	if err != nil || provenance == nil || provenance.UserID != actor.ID {
		t.Fatalf("lost task authority: %+v %v", provenance, err)
	}
	if _, _, err := p.CreateOrganizationChatTask(ctx, session.ID, task, true); err == nil {
		t.Fatal("started another job while first was pending")
	}
	if err := p.UpdateTaskStatus(ctx, selected, service.TaskStatusDone, "final.mp4"); err != nil {
		t.Fatal(err)
	}
	reused, created, err := p.CreateOrganizationChatTask(ctx, session.ID, task, false)
	if err != nil || created || reused.ID != selected || reused.Result != "final.mp4" {
		t.Fatalf("completed task was not reused: %+v %v", reused, err)
	}
	separate, created, err := p.CreateOrganizationChatTask(ctx, session.ID, task, true)
	if err != nil || !created || separate.ID == selected {
		t.Fatalf("explicit new deliverable failed: %+v %v", separate, err)
	}
}
