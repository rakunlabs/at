package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// The tool schema is the model's real interface. A status described only in
// prose is a status the model will guess at, so every task tool that accepts
// one must constrain it with an enum drawn from the canonical vocabulary.
func TestTaskToolStatusParametersAreConstrained(t *testing.T) {
	tools := map[string]map[string]any{}
	for _, tool := range builtinTools {
		schema, ok := tool.InputSchema["properties"].(map[string]any)
		if !ok {
			continue
		}
		tools[tool.Name] = schema
	}

	for _, name := range []string{"task_create", "task_create_child", "task_update", "task_update_current", "task_list"} {
		props, ok := tools[name]
		if !ok {
			t.Errorf("%s is not registered", name)
			continue
		}
		status, ok := props["status"].(map[string]any)
		if !ok {
			t.Errorf("%s has no status parameter", name)
			continue
		}
		enum, ok := status["enum"].([]string)
		if !ok {
			t.Errorf("%s.status has no enum; the model can emit anything", name)
			continue
		}
		if len(enum) != len(service.TaskStatuses) {
			t.Errorf("%s.status enum has %d values, vocabulary has %d", name, len(enum), len(service.TaskStatuses))
		}
		for _, value := range enum {
			if !service.ValidTaskStatus(value) {
				t.Errorf("%s.status offers %q, which the server rejects", name, value)
			}
		}
	}
}

// Descriptions must not name a status the server no longer accepts.
func TestTaskToolDescriptionsDropRetiredStatuses(t *testing.T) {
	for _, tool := range builtinTools {
		if !strings.HasPrefix(tool.Name, "task_") {
			continue
		}
		for _, retired := range []string{"completed, done", "in_review, review", "todo, open"} {
			if strings.Contains(tool.Description, retired) {
				t.Errorf("%s still advertises retired statuses: %q", tool.Name, retired)
			}
		}
	}
}

// Two couplings used to live only in prose or in nothing at all, and both
// produced tasks that could never run.
func TestTaskToolsStateTheirNonObviousCouplings(t *testing.T) {
	byName := map[string]string{}
	props := map[string]map[string]any{}
	for _, tool := range builtinTools {
		byName[tool.Name] = tool.Description
		if schema, ok := tool.InputSchema["properties"].(map[string]any); ok {
			props[tool.Name] = schema
		}
	}

	if desc := byName["task_create_child"]; !strings.Contains(desc, "task_process") {
		t.Error("task_create_child does not say the task still has to be started")
	}
	org, _ := props["task_create"]["organization_id"].(map[string]any)
	if text, _ := org["description"].(string); !strings.Contains(text, "task_process") {
		t.Error("task_create.organization_id does not say a task without one cannot be processed")
	}
	wait, _ := props["task_wait"]["timeout_seconds"].(map[string]any)
	text, _ := wait["description"].(string)
	if strings.Contains(text, "1800") {
		t.Error("task_wait still advertises a timeout the per-tool deadline cannot honour")
	}
	if !strings.Contains(text, "timed_out") {
		t.Error("task_wait does not tell the model what to do when it returns early")
	}
}
