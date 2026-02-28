package workflow

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestReachableNodes_ForwardOnly(t *testing.T) {
	// Simple linear graph: input → llm_call → output
	nodes := []service.WorkflowNode{
		{ID: "n1", Type: "input"},
		{ID: "n2", Type: "llm_call"},
		{ID: "n3", Type: "output"},
	}
	edges := []service.WorkflowEdge{
		{ID: "e1", Source: "n1", Target: "n2"},
		{ID: "e2", Source: "n2", Target: "n3"},
	}

	got := reachableNodes(nil, nodes, edges)
	for _, id := range []string{"n1", "n2", "n3"} {
		if !got[id] {
			t.Errorf("expected node %q to be reachable", id)
		}
	}
}

func TestReachableNodes_ResourceConfigIncluded(t *testing.T) {
	// Graph:
	//   input ──prompt──► agent_call ──► output
	//   skill_config ──skills──► agent_call (bottom handle)
	//
	// skill_config has no incoming edges and is not a start type.
	// It must still be reachable because agent_call depends on it.
	nodes := []service.WorkflowNode{
		{ID: "input1", Type: "input"},
		{ID: "agent1", Type: "agent_call"},
		{ID: "output1", Type: "output"},
		{ID: "skill1", Type: "skill_config"},
	}
	edges := []service.WorkflowEdge{
		{ID: "e1", Source: "input1", Target: "agent1", SourceHandle: "output", TargetHandle: "prompt"},
		{ID: "e2", Source: "agent1", Target: "output1", SourceHandle: "response", TargetHandle: "input"},
		{ID: "e3", Source: "skill1", Target: "agent1", SourceHandle: "skills", TargetHandle: "skills"},
	}

	got := reachableNodes(nil, nodes, edges)

	want := map[string]bool{"input1": true, "agent1": true, "output1": true, "skill1": true}
	for id := range want {
		if !got[id] {
			t.Errorf("expected node %q to be reachable", id)
		}
	}
	if len(got) != len(want) {
		t.Errorf("expected %d reachable nodes, got %d", len(want), len(got))
	}
}

func TestReachableNodes_MultipleResourceConfigs(t *testing.T) {
	// Graph:
	//   input ──► agent_call ──► output
	//   skill_config ──► agent_call (bottom)
	//   mcp_config ──► agent_call (bottom)
	//   memory_config ──► agent_call (bottom)
	nodes := []service.WorkflowNode{
		{ID: "input1", Type: "input"},
		{ID: "agent1", Type: "agent_call"},
		{ID: "output1", Type: "output"},
		{ID: "skill1", Type: "skill_config"},
		{ID: "mcp1", Type: "mcp_config"},
		{ID: "mem1", Type: "memory_config"},
	}
	edges := []service.WorkflowEdge{
		{ID: "e1", Source: "input1", Target: "agent1"},
		{ID: "e2", Source: "agent1", Target: "output1"},
		{ID: "e3", Source: "skill1", Target: "agent1", SourceHandle: "skills", TargetHandle: "skills"},
		{ID: "e4", Source: "mcp1", Target: "agent1", SourceHandle: "mcp_urls", TargetHandle: "mcp"},
		{ID: "e5", Source: "mem1", Target: "agent1", SourceHandle: "memory", TargetHandle: "memory"},
	}

	got := reachableNodes(nil, nodes, edges)

	for _, id := range []string{"input1", "agent1", "output1", "skill1", "mcp1", "mem1"} {
		if !got[id] {
			t.Errorf("expected node %q to be reachable", id)
		}
	}
}

func TestReachableNodes_TransitiveDependency(t *testing.T) {
	// Graph:
	//   input ──► agent_call ──► output
	//   upstream_data ──► memory_config ──► agent_call (bottom)
	//
	// Both memory_config AND upstream_data should be included.
	nodes := []service.WorkflowNode{
		{ID: "input1", Type: "input"},
		{ID: "agent1", Type: "agent_call"},
		{ID: "output1", Type: "output"},
		{ID: "mem1", Type: "memory_config"},
		{ID: "data1", Type: "template"},
	}
	edges := []service.WorkflowEdge{
		{ID: "e1", Source: "input1", Target: "agent1"},
		{ID: "e2", Source: "agent1", Target: "output1"},
		{ID: "e3", Source: "mem1", Target: "agent1", SourceHandle: "memory", TargetHandle: "memory"},
		{ID: "e4", Source: "data1", Target: "mem1", SourceHandle: "output", TargetHandle: "data"},
	}

	got := reachableNodes(nil, nodes, edges)

	for _, id := range []string{"input1", "agent1", "output1", "mem1", "data1"} {
		if !got[id] {
			t.Errorf("expected node %q to be reachable", id)
		}
	}
}

func TestReachableNodes_DisconnectedNotIncluded(t *testing.T) {
	// A completely disconnected node should NOT be included.
	nodes := []service.WorkflowNode{
		{ID: "input1", Type: "input"},
		{ID: "agent1", Type: "agent_call"},
		{ID: "orphan", Type: "skill_config"},
	}
	edges := []service.WorkflowEdge{
		{ID: "e1", Source: "input1", Target: "agent1"},
	}

	got := reachableNodes(nil, nodes, edges)

	if got["orphan"] {
		t.Errorf("orphan node should NOT be reachable (no edge connects it to any reachable node)")
	}
	if !got["input1"] || !got["agent1"] {
		t.Errorf("input1 and agent1 should be reachable")
	}
}

func TestReachableNodes_WithEntryNodeIDs(t *testing.T) {
	// Same resource config scenario but with explicit entry node IDs.
	nodes := []service.WorkflowNode{
		{ID: "trigger1", Type: "http_trigger"},
		{ID: "agent1", Type: "agent_call"},
		{ID: "skill1", Type: "skill_config"},
	}
	edges := []service.WorkflowEdge{
		{ID: "e1", Source: "trigger1", Target: "agent1"},
		{ID: "e2", Source: "skill1", Target: "agent1", SourceHandle: "skills", TargetHandle: "skills"},
	}

	got := reachableNodes([]string{"trigger1"}, nodes, edges)

	for _, id := range []string{"trigger1", "agent1", "skill1"} {
		if !got[id] {
			t.Errorf("expected node %q to be reachable", id)
		}
	}
}
