package nodes_test

import (
	"context"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func TestAgentCall_BuiltinTimeout(t *testing.T) {
	for _, parentTimeout := range []time.Duration{0, 30 * time.Second} {
		t.Run(parentTimeout.String(), func(t *testing.T) {
			type contextKey struct{}
			ctx := context.WithValue(context.Background(), contextKey{}, "task-context")
			if parentTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, parentTimeout)
				defer cancel()
			}

			var dispatched context.Context
			calls := 0
			mp := &mockProvider{chatFunc: func(_ context.Context, _ string, _ []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
				calls++
				if calls == 1 {
					return &service.LLMResponse{ToolCalls: []service.ToolCall{{ID: "tc1", Name: "bash_execute", Arguments: map[string]any{"command": "unused"}}}}, nil
				}
				if dispatched == nil || dispatched.Err() != context.Canceled {
					t.Fatal("builtin context must be canceled before the next LLM call")
				}
				if ctx.Err() != nil {
					t.Fatalf("parent context canceled: %v", ctx.Err())
				}
				return &service.LLMResponse{Content: "done", Finished: true}, nil
			}}
			reg := newTestRegistryWithProvider(mp)
			reg.AgentLookup = func(context.Context, string) (*service.Agent, error) {
				return &service.Agent{Config: service.AgentConfig{
					Provider: "test-provider", ToolTimeout: 120, BuiltinTools: []string{"bash_execute"},
				}}, nil
			}
			reg.BuiltinToolDefs = []workflow.BuiltinToolDef{{Name: "bash_execute"}}
			started := time.Now()
			reg.BuiltinToolDispatcher = func(toolCtx context.Context, name string, args map[string]any) (string, error) {
				dispatched = toolCtx
				if name != "bash_execute" || args["command"] != "unused" {
					t.Fatalf("unexpected dispatch: %s %v", name, args)
				}
				if toolCtx.Value(contextKey{}) != "task-context" {
					t.Fatal("builtin context lost parent values")
				}
				deadline, ok := toolCtx.Deadline()
				if !ok {
					t.Fatal("builtin context has no deadline")
				}
				if parentDeadline, ok := ctx.Deadline(); ok {
					if !deadline.Equal(parentDeadline) {
						t.Fatalf("deadline = %v, want parent deadline %v", deadline, parentDeadline)
					}
				} else if deadline.Before(started.Add(120*time.Second)) || deadline.After(time.Now().Add(120*time.Second)) {
					t.Fatalf("deadline %v does not reflect agent tool timeout", deadline)
				}
				return "ok", nil
			}
			node := makeNode(t, "agent_call", map[string]any{"agent_id": "test-agent", "max_iterations": float64(3)})
			res, err := node.Run(ctx, reg, map[string]any{"prompt": "run builtin"})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if dispatched == nil || res.Data()["response"] != "done" {
				t.Fatalf("builtin loop did not complete: %+v", res.Data())
			}
		})
	}
}
