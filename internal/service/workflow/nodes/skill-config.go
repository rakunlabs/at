package nodes

import (
	"context"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// skillConfigNode is a resource configuration node that outputs a list of
// skill names. It is designed to be connected to the bottom "skills" handle
// of an agent_call node.
//
// Config (node.Data):
//
//	"skills": []string — skill names or IDs to pass downstream
//
// Input ports:  (none)
// Output ports: "skills" — []string of skill names
type skillConfigNode struct {
	skills []string
}

func init() {
	workflow.RegisterNodeType("skill_config", newSkillConfigNode)
}

func newSkillConfigNode(node service.WorkflowNode) (workflow.Noder, error) {
	var skills []string
	if raw, ok := node.Data["skills"].([]any); ok {
		for _, s := range raw {
			if name, ok := s.(string); ok && name != "" {
				skills = append(skills, name)
			}
		}
	}

	return &skillConfigNode{skills: skills}, nil
}

func (n *skillConfigNode) Type() string { return "skill_config" }

func (n *skillConfigNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

func (n *skillConfigNode) Run(_ context.Context, _ *workflow.Registry, _ map[string]any) (workflow.NodeResult, error) {
	return workflow.NewResult(map[string]any{
		"skills": n.skills,
	}), nil
}
