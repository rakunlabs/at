package workflow_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	_ "github.com/rakunlabs/at/internal/service/workflow/nodes"
)

func boundExecution(t *testing.T, policy service.ExecutionPolicy, revoked *atomic.Bool) context.Context {
	t.Helper()
	policy.WorkspaceID = "w"
	ctx, err := service.BindExecution(t.Context(), service.ExecutionProvenance{RunID: "r", UserID: "u", WorkspaceID: "w", Source: "test"}, t.TempDir(), func(ctx context.Context, p service.ExecutionProvenance, a service.ExecutionAction) (service.ExecutionValidation, error) {
		return service.ExecutionValidation{Allowed: !revoked.Load() && a.ResourceID != "other-workspace", Policy: policy}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestExecutionRestrictedHandlerAndIndirectWorkflow(t *testing.T) {
	var revoked atomic.Bool
	ctx := boundExecution(t, service.ExecutionPolicy{AllowedNodes: []string{"input", "workflow_call", "exec"}, AllowedTools: []string{"skill_tool:skill:shell", "delegate:other-workspace:delegate"}}, &revoked)
	if _, err := workflow.ExecuteBashHandler(ctx, "touch /tmp/should-never-exist", nil, nil, time.Second); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("custom bash allowed: %v", err)
	}
	if _, err := workflow.ExecuteJSHandlerContext(ctx, "return httpGet('http://127.0.0.1');", nil); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("custom JS allowed: %v", err)
	}
	if err := workflow.AuthorizeToolHandler(ctx, "shell", "bash", "skill", ""); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("indirect custom shell allowed")
	}
	if err := workflow.AuthorizeToolHandler(ctx, "delegate", "delegate", "", "other-workspace"); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("cross-scope delegation allowed")
	}
	marker := filepath.Join(t.TempDir(), "executed")
	child := &service.Workflow{ID: "child", Graph: service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "start", Type: "input"}, {ID: "host", Type: "exec", Data: map[string]any{"command": "touch " + marker}}}, Edges: []service.WorkflowEdge{{Source: "start", Target: "host"}}}}
	engine := workflow.NewEngineWithDependencies(workflow.Dependencies{WorkflowLookup: func(context.Context, string) (*service.Workflow, error) { return child, nil }})
	parent := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "start", Type: "input"}, {ID: "child", Type: "workflow_call", Data: map[string]any{"workflow_id": "child"}}}, Edges: []service.WorkflowEdge{{Source: "start", Target: "child"}}}
	_, err := engine.Run(ctx, parent, nil, []string{"start"}, nil)
	if !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("indirect exec denial: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("indirect exec ran")
	}
}

func TestExecutionScopedLookupDeniesBeforeCredentials(t *testing.T) {
	var revoked atomic.Bool
	ctx := boundExecution(t, service.ExecutionPolicy{}, &revoked)
	called := false
	deps := workflow.ScopeDependencies(ctx, workflow.Dependencies{ConnectionLookup: func(context.Context, string) (*service.Connection, error) {
		called = true
		return &service.Connection{}, nil
	}, ProviderLookup: func(string) (service.LLMProvider, string, error) { called = true; return nil, "", nil }})
	if _, err := deps.ConnectionLookup(ctx, "other-workspace"); !errors.Is(err, service.ErrExecutionDenied) || called {
		t.Fatal("denied connection lookup reached credential store")
	}
	if _, _, err := deps.ProviderLookup("other-workspace"); !errors.Is(err, service.ErrExecutionDenied) || called {
		t.Fatal("denied provider reached global registry")
	}
	bindings := workflow.ResolveAgentConnectionBindings(ctx, deps.ConnectionLookup, map[string]string{"google": "other-workspace"}, nil)
	lookup := workflow.WrapVarLookupWithConnectionsContext(ctx, func(string) (string, error) { t.Fatal("denied binding fell back to global credential"); return "", nil }, bindings)
	if _, err := lookup("google_api_key"); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("missing denied-binding tombstone")
	}
}
