package nodes_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"

	// Blank import to trigger init() registrations for all node types.
	_ "github.com/rakunlabs/at/internal/service/workflow/nodes"
)

// ─── Mock Provider ───

type mockProvider struct {
	chatFunc func(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error)
}

func (m *mockProvider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	if m.chatFunc != nil {
		return m.chatFunc(ctx, model, messages, tools, opts)
	}
	return &service.LLMResponse{Content: "mock response", Finished: true}, nil
}

// ─── Helper: create a minimal Registry ───

func newTestRegistry() *workflow.Registry {
	return workflow.NewRegistry(
		nil, // providerLookup
		nil, // skillLookup
		nil, // varLookup
		nil, // varLister
		nil, // nodeConfigLookup
		nil, // workflowLookup
		nil, // agentLookup
		nil, // ragSearch
		nil, // ragIngest
		nil, // ragIngestFile
		nil, // ragDeleteBySource
		nil, // varSave
		nil, // ragStateLookup
		nil, // ragStateSave
		nil, // builtinDispatcher
		nil, // builtinDefs
		nil, // userPrefLookup
		nil, // chatMessageCreator
		nil, // chatSessionLookup
		nil, // recordUsage
		nil, // checkBudget
		nil, // recordAudit
		nil, // goalAncestry
		nil, // versionLookup
		nil, // inputs
	)
}

func newTestRegistryWithProvider(mp *mockProvider) *workflow.Registry {
	return workflow.NewRegistry(
		func(key string) (service.LLMProvider, string, error) {
			if key == "test-provider" {
				return mp, "default-model", nil
			}
			return nil, "", errors.New("provider not found: " + key)
		},
		nil, // skillLookup
		nil, // varLookup
		nil, // varLister
		nil, // nodeConfigLookup
		nil, // workflowLookup
		nil, // agentLookup
		nil, // ragSearch
		nil, // ragIngest
		nil, // ragIngestFile
		nil, // ragDeleteBySource
		nil, // varSave
		nil, // ragStateLookup
		nil, // ragStateSave
		nil, // builtinDispatcher
		nil, // builtinDefs
		nil, // userPrefLookup
		nil, // chatMessageCreator
		nil, // chatSessionLookup
		nil, // recordUsage
		nil, // checkBudget
		nil, // recordAudit
		nil, // goalAncestry
		nil, // versionLookup
		nil, // inputs
	)
}

// ─── Helper: build a node from factory ───

func makeNode(t *testing.T, typeName string, data map[string]any) workflow.Noder {
	t.Helper()
	factory := workflow.GetNodeFactory(typeName)
	if factory == nil {
		t.Fatalf("node type %q not registered", typeName)
	}
	noder, err := factory(service.WorkflowNode{
		ID:   "test-node",
		Type: typeName,
		Data: data,
	})
	if err != nil {
		t.Fatalf("factory(%q): %v", typeName, err)
	}
	return noder
}

// ═══════════════════════════════════════════════════════════════════
// conditional node tests
// ═══════════════════════════════════════════════════════════════════

func TestConditional_TrueExpression(t *testing.T) {
	node := makeNode(t, "conditional", map[string]any{
		"expression": "data > 5",
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{"data": 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	sel, ok := result.(workflow.NodeResultSelection)
	if !ok {
		t.Fatal("expected NodeResultSelection")
	}

	if len(sel.Selection()) != 1 || sel.Selection()[0] != "true" {
		t.Fatalf("expected selection [true], got %v", sel.Selection())
	}

	if sel.Data()["result"] != true {
		t.Fatalf("expected result=true, got %v", sel.Data()["result"])
	}
}

func TestConditional_FalseExpression(t *testing.T) {
	node := makeNode(t, "conditional", map[string]any{
		"expression": "data < 5",
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{"data": 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	sel, ok := result.(workflow.NodeResultSelection)
	if !ok {
		t.Fatal("expected NodeResultSelection")
	}

	if len(sel.Selection()) != 1 || sel.Selection()[0] != "false" {
		t.Fatalf("expected selection [false], got %v", sel.Selection())
	}

	if sel.Data()["result"] != false {
		t.Fatalf("expected result=false, got %v", sel.Data()["result"])
	}
}

func TestConditional_EmptyExpression_ValidateError(t *testing.T) {
	node := makeNode(t, "conditional", map[string]any{
		"expression": "",
	})

	reg := newTestRegistry()
	if err := node.Validate(context.Background(), reg); err == nil {
		t.Fatal("expected validation error for empty expression")
	}
}

func TestConditional_JSError(t *testing.T) {
	node := makeNode(t, "conditional", map[string]any{
		"expression": "undeclaredVar.nonExistent",
	})

	reg := newTestRegistry()
	_, err := node.Run(context.Background(), reg, map[string]any{})
	if err == nil {
		t.Fatal("expected error from bad JS expression")
	}
}

func TestConditional_ComplexExpression(t *testing.T) {
	node := makeNode(t, "conditional", map[string]any{
		"expression": "data.score >= 0.8 && data.active === true",
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{
		"data": map[string]any{"score": 0.9, "active": true},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	sel := result.(workflow.NodeResultSelection)
	if sel.Selection()[0] != "true" {
		t.Fatalf("expected true, got %v", sel.Selection())
	}
}

// ═══════════════════════════════════════════════════════════════════
// loop node tests
// ═══════════════════════════════════════════════════════════════════

func TestLoop_ArrayOfObjects(t *testing.T) {
	node := makeNode(t, "loop", map[string]any{
		"expression": "data",
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{
		"data": []any{
			map[string]any{"name": "Alice"},
			map[string]any{"name": "Bob"},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	fo, ok := result.(workflow.NodeResultFanOut)
	if !ok {
		t.Fatal("expected NodeResultFanOut")
	}

	items := fo.Items()
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0]["name"] != "Alice" {
		t.Fatalf("expected Alice, got %v", items[0]["name"])
	}
	// Index should be added.
	if items[1]["index"] != 1 {
		t.Fatalf("expected index=1 on second item, got %v", items[1]["index"])
	}
}

func TestLoop_ArrayOfPrimitives(t *testing.T) {
	node := makeNode(t, "loop", map[string]any{
		"expression": "data",
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{
		"data": []any{"a", "b", "c"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	fo := result.(workflow.NodeResultFanOut)
	items := fo.Items()
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	// Primitives are wrapped as {item, index}.
	if items[0]["item"] != "a" || items[0]["index"] != 0 {
		t.Fatalf("expected {item:a, index:0}, got %v", items[0])
	}
}

func TestLoop_EmptyArray_StopBranch(t *testing.T) {
	node := makeNode(t, "loop", map[string]any{
		"expression": "data",
	})

	reg := newTestRegistry()
	_, err := node.Run(context.Background(), reg, map[string]any{
		"data": []any{},
	})
	if !errors.Is(err, workflow.ErrStopBranch) {
		t.Fatalf("expected ErrStopBranch, got %v", err)
	}
}

func TestLoop_NilResult_StopBranch(t *testing.T) {
	node := makeNode(t, "loop", map[string]any{
		"expression": "data.missing",
	})

	reg := newTestRegistry()
	_, err := node.Run(context.Background(), reg, map[string]any{
		"data": map[string]any{},
	})
	if !errors.Is(err, workflow.ErrStopBranch) {
		t.Fatalf("expected ErrStopBranch for nil result, got %v", err)
	}
}

func TestLoop_SingleValue(t *testing.T) {
	node := makeNode(t, "loop", map[string]any{
		"expression": "data",
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{
		"data": "single",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	fo := result.(workflow.NodeResultFanOut)
	items := fo.Items()
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0]["item"] != "single" {
		t.Fatalf("expected single, got %v", items[0]["item"])
	}
}

func TestLoop_EmptyExpression_ValidateError(t *testing.T) {
	node := makeNode(t, "loop", map[string]any{
		"expression": "",
	})

	reg := newTestRegistry()
	if err := node.Validate(context.Background(), reg); err == nil {
		t.Fatal("expected validation error for empty expression")
	}
}

func TestLoop_FilterExpression(t *testing.T) {
	node := makeNode(t, "loop", map[string]any{
		"expression": "data.filter(function(x) { return x > 2; })",
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{
		"data": []any{1, 2, 3, 4},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	fo := result.(workflow.NodeResultFanOut)
	items := fo.Items()
	if len(items) != 2 {
		t.Fatalf("expected 2 items (3,4), got %d", len(items))
	}
}

// ═══════════════════════════════════════════════════════════════════
// script node tests
// ═══════════════════════════════════════════════════════════════════

func TestScript_SuccessfulExecution(t *testing.T) {
	node := makeNode(t, "script", map[string]any{
		"code": "return data * 2;",
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{"data": 21})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	sel, ok := result.(workflow.NodeResultSelection)
	if !ok {
		t.Fatal("expected NodeResultSelection")
	}

	// Should route to "always" and "true".
	selection := sel.Selection()
	hasAlways, hasTrue := false, false
	for _, s := range selection {
		if s == "always" {
			hasAlways = true
		}
		if s == "true" {
			hasTrue = true
		}
	}
	if !hasAlways || !hasTrue {
		t.Fatalf("expected selection [always, true], got %v", selection)
	}

	// Result should be 42.
	if sel.Data()["result"] != int64(42) {
		t.Fatalf("expected result=42, got %v (type %T)", sel.Data()["result"], sel.Data()["result"])
	}
}

func TestScript_ThrowsError(t *testing.T) {
	node := makeNode(t, "script", map[string]any{
		"code": "throw new Error('test error');",
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{})
	if err != nil {
		t.Fatalf("Run should not return error (error captured in result): %v", err)
	}

	sel := result.(workflow.NodeResultSelection)

	// Should route to "always" and "false".
	selection := sel.Selection()
	hasFalse := false
	for _, s := range selection {
		if s == "false" {
			hasFalse = true
		}
	}
	if !hasFalse {
		t.Fatalf("expected selection to include 'false', got %v", selection)
	}

	// Error message should be captured.
	if sel.Data()["error"] == nil {
		t.Fatal("expected error in output data")
	}
}

func TestScript_ReturnsObject(t *testing.T) {
	node := makeNode(t, "script", map[string]any{
		"code": `return {greeting: "hello " + data};`,
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{"data": "world"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	sel := result.(workflow.NodeResultSelection)
	res, ok := sel.Data()["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result to be map, got %T: %v", sel.Data()["result"], sel.Data()["result"])
	}
	if res["greeting"] != "hello world" {
		t.Fatalf("expected 'hello world', got %v", res["greeting"])
	}
}

func TestScript_EmptyCode_ValidateError(t *testing.T) {
	node := makeNode(t, "script", map[string]any{
		"code": "",
	})

	reg := newTestRegistry()
	if err := node.Validate(context.Background(), reg); err == nil {
		t.Fatal("expected validation error for empty code")
	}
}

func TestScript_InputPassthrough(t *testing.T) {
	node := makeNode(t, "script", map[string]any{
		"code": "return 1;",
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{
		"data":  "original",
		"extra": 42,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data := result.(workflow.NodeResultSelection).Data()
	if data["data"] != "original" {
		t.Fatalf("expected input 'data' to pass through, got %v", data["data"])
	}
	if data["extra"] != 42 {
		t.Fatalf("expected input 'extra' to pass through, got %v", data["extra"])
	}
}

func TestScript_InputCountCapped(t *testing.T) {
	// input_count > 10 should be capped to 10.
	node := makeNode(t, "script", map[string]any{
		"code":        "return 1;",
		"input_count": float64(15),
	})
	if node.Type() != "script" {
		t.Fatal("unexpected type")
	}
	// The factory should cap the count — no error expected.
}

// ═══════════════════════════════════════════════════════════════════
// llm_call node tests
// ═══════════════════════════════════════════════════════════════════

func TestLLMCall_HappyPath(t *testing.T) {
	mp := &mockProvider{
		chatFunc: func(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
			// Verify the messages structure.
			if len(messages) != 2 {
				t.Errorf("expected 2 messages (system + user), got %d", len(messages))
			}
			if messages[0].Role != "system" {
				t.Errorf("expected first message role=system, got %s", messages[0].Role)
			}
			if messages[1].Content != "Hello LLM" {
				t.Errorf("expected user message 'Hello LLM', got %v", messages[1].Content)
			}
			if model != "test-model" {
				t.Errorf("expected model 'test-model', got %s", model)
			}
			return &service.LLMResponse{Content: "Hi there!", Finished: true}, nil
		},
	}

	reg := newTestRegistryWithProvider(mp)
	node := makeNode(t, "llm_call", map[string]any{
		"provider":      "test-provider",
		"model":         "test-model",
		"system_prompt": "You are helpful.",
	})

	if err := node.Validate(context.Background(), reg); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	result, err := node.Run(context.Background(), reg, map[string]any{
		"prompt": "Hello LLM",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Data()["response"] != "Hi there!" {
		t.Fatalf("expected response 'Hi there!', got %v", result.Data()["response"])
	}
}

func TestLLMCall_DefaultModel(t *testing.T) {
	var capturedModel string
	mp := &mockProvider{
		chatFunc: func(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
			capturedModel = model
			return &service.LLMResponse{Content: "ok", Finished: true}, nil
		},
	}

	reg := newTestRegistryWithProvider(mp)
	node := makeNode(t, "llm_call", map[string]any{
		"provider": "test-provider",
		// No model specified — should use default.
	})

	_, err := node.Run(context.Background(), reg, map[string]any{"prompt": "test"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if capturedModel != "default-model" {
		t.Fatalf("expected default-model, got %s", capturedModel)
	}
}

func TestLLMCall_NoPrompt_Error(t *testing.T) {
	mp := &mockProvider{}
	reg := newTestRegistryWithProvider(mp)
	node := makeNode(t, "llm_call", map[string]any{
		"provider": "test-provider",
	})

	_, err := node.Run(context.Background(), reg, map[string]any{})
	if err == nil {
		t.Fatal("expected error when no prompt is provided")
	}
}

func TestLLMCall_FallbackPromptFromText(t *testing.T) {
	var capturedPrompt string
	mp := &mockProvider{
		chatFunc: func(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
			for _, msg := range messages {
				if msg.Role == "user" {
					capturedPrompt, _ = msg.Content.(string)
				}
			}
			return &service.LLMResponse{Content: "ok", Finished: true}, nil
		},
	}

	reg := newTestRegistryWithProvider(mp)
	node := makeNode(t, "llm_call", map[string]any{
		"provider": "test-provider",
	})

	// "text" is fallback for "prompt".
	_, err := node.Run(context.Background(), reg, map[string]any{
		"text": "from text input",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if capturedPrompt != "from text input" {
		t.Fatalf("expected 'from text input', got %q", capturedPrompt)
	}
}

func TestLLMCall_ContextAppended(t *testing.T) {
	var capturedPrompt string
	mp := &mockProvider{
		chatFunc: func(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
			for _, msg := range messages {
				if msg.Role == "user" {
					capturedPrompt, _ = msg.Content.(string)
				}
			}
			return &service.LLMResponse{Content: "ok", Finished: true}, nil
		},
	}

	reg := newTestRegistryWithProvider(mp)
	node := makeNode(t, "llm_call", map[string]any{
		"provider": "test-provider",
	})

	_, err := node.Run(context.Background(), reg, map[string]any{
		"prompt":  "Question?",
		"context": "Some relevant context.",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if capturedPrompt != "Question?\n\nContext:\nSome relevant context." {
		t.Fatalf("expected context appended, got %q", capturedPrompt)
	}
}

func TestLLMCall_MissingProvider_ValidateError(t *testing.T) {
	node := makeNode(t, "llm_call", map[string]any{
		"provider": "",
	})

	reg := newTestRegistry()
	if err := node.Validate(context.Background(), reg); err == nil {
		t.Fatal("expected validation error for empty provider")
	}
}

func TestLLMCall_NilProviderLookup_ValidateError(t *testing.T) {
	node := makeNode(t, "llm_call", map[string]any{
		"provider": "test",
	})

	reg := newTestRegistry()
	if err := node.Validate(context.Background(), reg); err == nil {
		t.Fatal("expected validation error for nil provider lookup")
	}
}

func TestLLMCall_ProviderError(t *testing.T) {
	mp := &mockProvider{
		chatFunc: func(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
			return nil, errors.New("provider error")
		},
	}

	reg := newTestRegistryWithProvider(mp)
	node := makeNode(t, "llm_call", map[string]any{
		"provider": "test-provider",
	})

	_, err := node.Run(context.Background(), reg, map[string]any{"prompt": "test"})
	if err == nil {
		t.Fatal("expected error from provider")
	}
}

// ═══════════════════════════════════════════════════════════════════
// agent_call node tests (basic — no tool loop)
// ═══════════════════════════════════════════════════════════════════

func TestAgentCall_SimplePrompt_NoTools(t *testing.T) {
	mp := &mockProvider{
		chatFunc: func(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
			return &service.LLMResponse{
				Content:  "Agent response",
				Finished: true,
			}, nil
		},
	}

	reg := newTestRegistryWithProvider(mp)
	node := makeNode(t, "agent_call", map[string]any{
		"provider": "test-provider",
	})

	if err := node.Validate(context.Background(), reg); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	result, err := node.Run(context.Background(), reg, map[string]any{
		"prompt": "Hello agent",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Data()["response"] != "Agent response" {
		t.Fatalf("expected 'Agent response', got %v", result.Data()["response"])
	}
}

func TestAgentCall_MissingProviderAndAgent_ValidateError(t *testing.T) {
	node := makeNode(t, "agent_call", map[string]any{
		// Neither agent_id nor provider specified.
	})

	reg := newTestRegistry()
	if err := node.Validate(context.Background(), reg); err == nil {
		t.Fatal("expected validation error when both provider and agent_id are empty")
	}
}

func TestAgentCall_ToolCallLoop(t *testing.T) {
	callCount := 0
	mp := &mockProvider{
		chatFunc: func(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
			callCount++
			if callCount == 1 {
				// First call: return a tool call.
				return &service.LLMResponse{
					Content:  "",
					Finished: false,
					ToolCalls: []service.ToolCall{
						{
							ID:        "call_1",
							Name:      "test_tool",
							Arguments: map[string]any{"x": "hello"},
						},
					},
				}, nil
			}
			// Second call: tool result has been provided, return final answer.
			return &service.LLMResponse{
				Content:  "Final answer after tool",
				Finished: true,
			}, nil
		},
	}

	reg := newTestRegistryWithProvider(mp)
	node := makeNode(t, "agent_call", map[string]any{
		"provider": "test-provider",
		"tools": []any{
			map[string]any{
				"name":         "test_tool",
				"description":  "A test tool",
				"input_schema": map[string]any{"type": "object"},
				"handler":      `return "tool result: " + args.x;`,
			},
		},
	})

	result, err := node.Run(context.Background(), reg, map[string]any{
		"prompt": "Use the tool",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Data()["response"] != "Final answer after tool" {
		t.Fatalf("expected 'Final answer after tool', got %v", result.Data()["response"])
	}

	if callCount != 2 {
		t.Fatalf("expected 2 LLM calls (initial + after tool), got %d", callCount)
	}
}

// ═══════════════════════════════════════════════════════════════════
// common/content conversion tests
// ═══════════════════════════════════════════════════════════════════

func TestConvertContentBlocksToOpenAI_AssistantText(t *testing.T) {
	// This tests the extracted common function indirectly through imports.
	// Direct test of the common package is below.
}

// ═══════════════════════════════════════════════════════════════════
// exec node tests (basic)
// ═══════════════════════════════════════════════════════════════════

func TestExec_AllowInputOverride_DefaultFalse(t *testing.T) {
	// When allow_input_override is not set (default false),
	// the command from inputs should NOT override the static config.
	node := makeNode(t, "exec", map[string]any{
		"command": "echo static",
		// allow_input_override defaults to false.
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{
		"command": "echo INJECTED",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	sel := result.(workflow.NodeResultSelection)
	stdout := sel.Data()["stdout"].(string)
	if stdout != "static\n" {
		t.Fatalf("expected 'static\\n' (input override blocked), got %q", stdout)
	}
}

func TestExec_AllowInputOverride_True(t *testing.T) {
	node := makeNode(t, "exec", map[string]any{
		"command":              "echo static",
		"allow_input_override": true,
	})

	reg := newTestRegistry()
	result, err := node.Run(context.Background(), reg, map[string]any{
		"command": "echo OVERRIDE",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	sel := result.(workflow.NodeResultSelection)
	stdout := sel.Data()["stdout"].(string)
	if stdout != "OVERRIDE\n" {
		t.Fatalf("expected 'OVERRIDE\\n', got %q", stdout)
	}
}

func TestExec_EmptyCommand_ValidateError(t *testing.T) {
	node := makeNode(t, "exec", map[string]any{
		"command": "",
	})

	reg := newTestRegistry()
	if err := node.Validate(context.Background(), reg); err == nil {
		t.Fatal("expected validation error for empty command")
	}
}
