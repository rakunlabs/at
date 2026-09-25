package workflow

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

func TestSkillRuntimeForkRequest(t *testing.T) {
	skill := &service.Skill{
		ID: "review-skill", Name: "review", Context: "fork",
		Agent: "reviewer", SystemPrompt: "Review carefully.",
	}
	lookup := func(name string) (*service.Skill, error) {
		if name == "review" || name == skill.ID {
			return skill, nil
		}
		return nil, nil
	}
	rt, err := NewSkillRuntime(executiontest.Context(t), lookup, []service.SkillRef{{ID: "review"}}, nil, nil)
	if err != nil {
		t.Fatalf("NewSkillRuntime: %v", err)
	}
	req, forked, err := rt.ForkRequest(map[string]any{
		"skill_name": "review", "task": "Inspect the patch", "context": "Focus on auth", "run_mode": "background",
	})
	if err != nil {
		t.Fatalf("ForkRequest: %v", err)
	}
	if !forked || req.Skill != skill || req.Agent != "reviewer" || req.Task != "Inspect the patch" || req.Context != "Focus on auth" || !req.Background {
		t.Fatalf("unexpected request: %+v, forked=%v", req, forked)
	}
	if rt.IsSkillLoaded("review") {
		t.Fatal("forked skill was activated in the parent context")
	}
	if _, _, err := rt.ForkRequest(map[string]any{"skill_name": "review"}); err == nil {
		t.Fatal("expected a missing task error")
	}
	foreground, _, err := rt.ForkRequest(map[string]any{"skill_name": "review", "task": "Inspect again"})
	if err != nil || foreground.Background {
		t.Fatalf("omitted run_mode should use foreground: %+v, err=%v", foreground, err)
	}
	if _, _, err := rt.ForkRequest(map[string]any{"skill_name": "review", "task": "Inspect", "run_mode": "later"}); err == nil {
		t.Fatal("expected invalid run_mode error")
	}
}
