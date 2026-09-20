package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestDelegationResultPreservesProductionContracts(t *testing.T) {
	for _, result := range []string{
		`{"video_file":"/workspace/final.mp4","production_mode":"short"}`,
		"/workspace/script.json",
		"/workspace/images_manifest.json",
	} {
		got := delegationTaskResult(&service.Task{ID: "stage", Status: service.TaskStatusDone, Result: result})
		if got != result {
			t.Fatalf("successful result changed: %q != %q", got, result)
		}
	}
	for _, result := range []string{"[BLOCKED] TTS unavailable", "[ITERATION_LIMIT] Partial script", "[OUTPUT_LIMIT] Truncated script"} {
		got := delegationTaskResult(&service.Task{ID: "stage", Status: service.TaskStatusBlocked, Result: result})
		if !strings.HasPrefix(got, result) || !strings.Contains(got, "status: blocked") {
			t.Fatalf("lost failure marker or status: %q", got)
		}
	}
	got := delegationTaskResult(&service.Task{ID: "stage", Status: service.TaskStatusCancelled, Result: "Stopped"})
	if !strings.HasPrefix(got, "[BLOCKED]") || !strings.Contains(got, "status: cancelled") {
		t.Fatalf("cancelled stage appears successful: %q", got)
	}
}

// Exercise the production engine's three sequential delegates with the same
// path-based handoff as YouTube Shorts, without running paid media services.
func TestProductionDelegationKeepsThreeSpecialistStages(t *testing.T) {
	stageIDs := []string{"script", "graphics", "video"}
	stageNames := []string{"Script Writer", "Graphic Designer", "Video Producer"}
	toolNames := []string{"delegate_to_script_writer", "delegate_to_graphic_designer", "delegate_to_video_producer"}
	results := []string{"/workspace/script.json", "/workspace/images_manifest.json", `{"video_file":"/workspace/final.mp4"}`}
	provider := &fakeObsProvider{}
	agents := map[string]*service.Agent{
		"director": {ID: "director", Name: "Content Director", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 8, SystemPrompt: "Use Script Writer, Graphic Designer, then Video Producer, once each. Do not perform specialist work yourself."}},
	}
	members := []service.OrganizationAgent{{OrganizationID: "org1", AgentID: "director", Status: "active"}}
	for i, id := range stageIDs {
		agents[id] = &service.Agent{ID: id, Name: stageNames[i], Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 2}}
		members = append(members, service.OrganizationAgent{OrganizationID: "org1", AgentID: id, ParentAgentID: "director", Status: "active"})
		provider.responses = append(provider.responses,
			&service.LLMResponse{ToolCalls: []service.ToolCall{{ID: id, Name: toolNames[i], Arguments: map[string]any{"task": "Produce your stage's artifact", "context": "production_mode=short"}}}},
			&service.LLMResponse{Content: results[i], Finished: true},
		)
	}
	provider.responses = append(provider.responses, &service.LLMResponse{Content: results[2], Finished: true})
	s, tasks := newObsTestServer(t, provider, &fakeLLMCallStore{}, agents, members)
	task, _ := tasks.CreateTask(s.ctx, service.Task{OrganizationID: "org1", AssignedAgentID: "director", Title: "Produce a Short"})
	if err := s.runOrgDelegation(s.ctx, &service.Organization{ID: "org1", MaxDelegationDepth: 3}, task, "director", 0); err != nil {
		t.Fatal(err)
	}
	children, _ := tasks.ListChildTasks(s.ctx, task.ID)
	if len(children) != 3 {
		t.Fatalf("got %d specialist stages, want 3", len(children))
	}
	for i, child := range children {
		if child.AssignedAgentID != stageIDs[i] || child.Status != service.TaskStatusDone || child.Result != results[i] {
			t.Fatalf("stage %d changed: %+v", i, child)
		}
		// Parent sees the exact path/JSON after each stage, not a status prefix.
		req := provider.requests[(i+1)*2]
		blocks := req[len(req)-1].Content.([]service.ContentBlock)
		if len(blocks) != 1 || blocks[0].ContentText() != results[i] {
			t.Fatalf("stage %d handoff changed: %+v", i, blocks)
		}
	}
	updated, _ := tasks.GetTask(s.ctx, task.ID)
	if updated.Status != service.TaskStatusDone || updated.Result != results[2] {
		t.Fatalf("production did not finish: %+v", updated)
	}
	prompt := provider.requests[0][0].Content.(string)
	if !strings.Contains(prompt, "Never replace a required production stage with consultation") {
		t.Fatal("production role instructions lack precedence over optional consultation")
	}
}
