package server

import (
	"context"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type syncTriggerStore struct {
	service.TriggerStorer
	triggers map[string]service.Trigger
	deleted  []string
}

func (m *syncTriggerStore) ListTriggers(context.Context, string) ([]service.Trigger, error) {
	var out []service.Trigger
	for _, t := range m.triggers {
		out = append(out, t)
	}
	return out, nil
}

func (m *syncTriggerStore) DeleteTrigger(_ context.Context, id string) error {
	m.deleted = append(m.deleted, id)
	delete(m.triggers, id)
	return nil
}

type syncWorkflowStore struct {
	service.WorkflowStorer
	wf *service.Workflow
}

func (m *syncWorkflowStore) GetWorkflow(context.Context, string) (*service.Workflow, error) {
	return m.wf, nil
}

func TestSyncTriggersKeepsStandaloneTriggers(t *testing.T) {
	triggers := &syncTriggerStore{triggers: map[string]service.Trigger{
		"standalone": {ID: "standalone", Type: "cron", Config: map[string]any{"schedule": "0 7 * * *"}},
		"from-node":  {ID: "from-node", Type: "cron", Config: map[string]any{"schedule": "0 8 * * *"}},
	}}
	previous := &service.Workflow{ID: "wf", Graph: service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "cron", Type: "cron_trigger", Data: map[string]any{"trigger_id": "from-node", "schedule": "0 8 * * *"}},
	}}}
	s := &Server{triggerStore: triggers, workflowStore: &syncWorkflowStore{wf: previous}}

	// A save without any trigger node removes only the node-owned trigger.
	if _, err := s.syncTriggers(t.Context(), "wf", &service.WorkflowGraph{}, "agent"); err != nil {
		t.Fatal(err)
	}
	if len(triggers.deleted) != 1 || triggers.deleted[0] != "from-node" {
		t.Fatalf("deleted %v, want only from-node", triggers.deleted)
	}
	if _, ok := triggers.triggers["standalone"]; !ok {
		t.Fatal("standalone trigger was deleted by a graph save")
	}
}
