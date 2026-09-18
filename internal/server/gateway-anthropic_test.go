package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func decodeAnthropicRequest(t *testing.T, body string) *anthropicMessagesRequest {
	t.Helper()

	var req anthropicMessagesRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return &req
}

// ─── Request validation ───

func TestAnthropicRequestValidation(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"complete", `{"model":"anthropic/claude","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`, false},
		{"missing max_tokens", `{"model":"anthropic/claude","messages":[{"role":"user","content":"hi"}]}`, true},
		{"zero max_tokens", `{"model":"anthropic/claude","max_tokens":0,"messages":[{"role":"user","content":"hi"}]}`, true},
		{"missing model", `{"max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`, true},
		{"empty messages", `{"model":"anthropic/claude","max_tokens":100,"messages":[]}`, true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAnthropicRequest(decodeAnthropicRequest(t, tt.body))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestAnthropicSystemPromptBothForms(t *testing.T) {
	asString := decodeAnthropicRequest(t, `{"system":"be terse"}`)
	if got := anthropicSystemPrompt(asString.System); got != "be terse" {
		t.Fatalf("string form = %q", got)
	}

	asBlocks := decodeAnthropicRequest(t, `{"system":[{"type":"text","text":"be terse"},{"type":"text","text":"and precise"}]}`)
	if got := anthropicSystemPrompt(asBlocks.System); got != "be terse\n\nand precise" {
		t.Fatalf("block form = %q", got)
	}

	if got := anthropicSystemPrompt(nil); got != "" {
		t.Fatalf("absent system = %q", got)
	}
}

// ─── Cell 4: Anthropic inbound → Anthropic-family target ───

func TestAnthropicToAnthropicIsNearIdentity(t *testing.T) {
	req := decodeAnthropicRequest(t, `{
		"model":"anthropic/claude","max_tokens":100,
		"system":"be terse",
		"messages":[
			{"role":"user","content":"screenshot the page"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"screenshot","input":{"url":"a"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[
				{"type":"text","text":"done"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}
			]}]}
		]
	}`)

	system, messages := translateAnthropicMessages(req)
	if system != "be terse" {
		t.Fatalf("system = %q", system)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d want 3", len(messages))
	}

	// tool_use preserves its identifier.
	assistant, ok := messages[1].Content.([]service.ContentBlock)
	if !ok || len(assistant) != 1 || assistant[0].Type != "tool_use" || assistant[0].ID != "toolu_1" {
		t.Fatalf("assistant = %+v", messages[1].Content)
	}

	// tool_result keeps its correlation and its image.
	user, ok := messages[2].Content.([]service.ContentBlock)
	if !ok || len(user) != 1 || user[0].Type != "tool_result" || user[0].ToolUseID != "toolu_1" {
		t.Fatalf("tool result = %+v", messages[2].Content)
	}
	parts, structured := user[0].ContentBlocks()
	if !structured || len(parts) != 2 {
		t.Fatalf("tool result content = %+v structured=%v", user[0].Content, structured)
	}
	if parts[1].Type != "image" || parts[1].Source == nil || parts[1].Source.Data != "aGk=" {
		t.Fatalf("image lost: %+v", parts[1])
	}
}

// A text-only tool result must collapse to a string, matching what the wire
// carried, so nothing about ordinary traffic changes shape.
func TestAnthropicTextOnlyToolResultStaysString(t *testing.T) {
	req := decodeAnthropicRequest(t, `{
		"model":"anthropic/claude","max_tokens":100,
		"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"ok"}]}]}]
	}`)

	_, messages := translateAnthropicMessages(req)
	blocks := messages[0].Content.([]service.ContentBlock)
	if got, isString := blocks[0].Content.(string); !isString || got != "ok" {
		t.Fatalf("content = %#v want the string \"ok\"", blocks[0].Content)
	}
}

// ─── Cell 3: Anthropic inbound → OpenAI-family target ───

func TestAnthropicToOpenAIConversion(t *testing.T) {
	req := decodeAnthropicRequest(t, `{
		"model":"openai/gpt-4.1","max_tokens":100,
		"system":"be terse",
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"screenshot","input":{"url":"a"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"done"}]}
		]
	}`)

	msgs := translateAnthropicToOpenAI(req)
	if len(msgs) != 4 {
		t.Fatalf("messages = %d want system+user+assistant+tool: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" {
		t.Fatalf("first message = %+v want system", msgs[0])
	}

	if len(msgs[2].ToolCalls) != 1 || msgs[2].ToolCalls[0].ID != "toolu_1" {
		t.Fatalf("tool call lost: %+v", msgs[2])
	}
	if msgs[2].ToolCalls[0].Function.Name != "screenshot" {
		t.Fatalf("tool name = %q", msgs[2].ToolCalls[0].Function.Name)
	}

	if msgs[3].Role != "tool" || msgs[3].ToolCallID != "toolu_1" {
		t.Fatalf("tool result not correlated: %+v", msgs[3])
	}
	var content string
	if err := json.Unmarshal(msgs[3].Content, &content); err != nil || content != "done" {
		t.Fatalf("tool content = %s (%v)", msgs[3].Content, err)
	}
}

// An image must survive the conversion to an OpenAI-family target, as a data URL
// — the only inline form the OpenAI shape has.
func TestAnthropicImageBecomesOpenAIDataURL(t *testing.T) {
	req := decodeAnthropicRequest(t, `{
		"model":"openai/gpt-4.1","max_tokens":100,
		"messages":[{"role":"user","content":[
			{"type":"text","text":"what is this"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}
		]}]
	}`)

	msgs := translateAnthropicToOpenAI(req)
	if len(msgs) != 1 {
		t.Fatalf("messages = %+v", msgs)
	}

	var parts []map[string]any
	if err := json.Unmarshal(msgs[0].Content, &parts); err != nil {
		t.Fatalf("content = %s (%v)", msgs[0].Content, err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %+v want text + image", parts)
	}
	image, _ := parts[1]["image_url"].(map[string]any)
	if image == nil || image["url"] != "data:image/png;base64,aGk=" {
		t.Fatalf("image part = %+v", parts[1])
	}
}

func TestAnthropicMediaInToolResultReachesOpenAITarget(t *testing.T) {
	req := decodeAnthropicRequest(t, `{
		"model":"openai/gpt-4.1","max_tokens":100,
		"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[
			{"type":"text","text":"done"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}
		]}]}]
	}`)

	msgs := translateAnthropicToOpenAI(req)
	var parts []map[string]any
	if err := json.Unmarshal(msgs[0].Content, &parts); err != nil {
		t.Fatalf("tool content = %s (%v)", msgs[0].Content, err)
	}
	if len(parts) != 2 || parts[1]["type"] != "image_url" {
		t.Fatalf("image dropped from tool result: %+v", parts)
	}
}

func TestAnthropicRemoteMediaURLPassesThrough(t *testing.T) {
	req := decodeAnthropicRequest(t, `{
		"model":"openai/gpt-4.1","max_tokens":100,
		"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"url","url":"https://example.test/a.png"}}
		]}]
	}`)

	msgs := translateAnthropicToOpenAI(req)
	var parts []map[string]any
	if err := json.Unmarshal(msgs[0].Content, &parts); err != nil {
		t.Fatal(err)
	}
	image, _ := parts[0]["image_url"].(map[string]any)
	if image == nil || image["url"] != "https://example.test/a.png" {
		t.Fatalf("url reference not passed through: %+v", parts[0])
	}
}

func TestAnthropicDocumentBecomesOpenAIFilePart(t *testing.T) {
	req := decodeAnthropicRequest(t, `{
		"model":"openai/gpt-4.1","max_tokens":100,
		"messages":[{"role":"user","content":[
			{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERg=="}}
		]}]
	}`)

	msgs := translateAnthropicToOpenAI(req)
	var parts []map[string]any
	if err := json.Unmarshal(msgs[0].Content, &parts); err != nil {
		t.Fatal(err)
	}
	if parts[0]["type"] != "file" {
		t.Fatalf("document part = %+v", parts[0])
	}
	file, _ := parts[0]["file"].(map[string]any)
	if file == nil || file["file_data"] != "data:application/pdf;base64,JVBERg==" {
		t.Fatalf("file part = %+v", parts[0])
	}
}

// ─── Matrix dispatch ───

func TestBuildAnthropicProviderMessagesSelectsCellByFamily(t *testing.T) {
	s := &Server{}
	req := decodeAnthropicRequest(t, `{
		"model":"m","max_tokens":100,
		"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}]}]
	}`)

	t.Run("anthropic family keeps native content blocks", func(t *testing.T) {
		messages, _ := s.buildAnthropicProviderMessages("anthropic", req)
		blocks, ok := messages[0].Content.([]service.ContentBlock)
		if !ok || blocks[0].Type != "image" || blocks[0].Source == nil {
			t.Fatalf("expected native blocks, got %#v", messages[0].Content)
		}
	})

	t.Run("bedrock is anthropic family", func(t *testing.T) {
		if !anthropicFamilyProvider("bedrock") || !anthropicFamilyProvider("minimax") {
			t.Fatal("bedrock and minimax consume the Anthropic block shape")
		}
		if anthropicFamilyProvider("openai") || anthropicFamilyProvider("gemini") {
			t.Fatal("openai and gemini are not anthropic family")
		}
	})

	t.Run("openai family produces the openai shape", func(t *testing.T) {
		messages, _ := s.buildAnthropicProviderMessages("openai", req)
		if _, ok := messages[0].Content.([]service.ContentBlock); ok {
			t.Fatal("an OpenAI target must not receive Anthropic content blocks")
		}
	})
}

// ─── Response encoding ───

func TestAnthropicStopReasonMapping(t *testing.T) {
	cases := []struct {
		name string
		resp service.LLMResponse
		want string
	}{
		{"plain text", service.LLMResponse{Content: "hi", FinishReason: "stop"}, "end_turn"},
		{"truncated", service.LLMResponse{Content: "hi", FinishReason: "length"}, "max_tokens"},
		{"content filtered", service.LLMResponse{FinishReason: "content_filter"}, "refusal"},
		{"tool calls", service.LLMResponse{ToolCalls: []service.ToolCall{{ID: "t1", Name: "x"}}, FinishReason: "tool_calls"}, "tool_use"},
		{
			// Adapters reconcile this, but a provider reporting "stop" alongside
			// calls must never be told the turn ended.
			name: "tool calls despite a stop finish reason",
			resp: service.LLMResponse{ToolCalls: []service.ToolCall{{ID: "t1"}}, FinishReason: "stop"},
			want: "tool_use",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := anthropicStopReason(&tt.resp, nil)
			if got != tt.want {
				t.Fatalf("stop_reason = %q want %q", got, tt.want)
			}
		})
	}

	t.Run("stop sequence is identified", func(t *testing.T) {
		resp := service.LLMResponse{Content: "answer<END>", FinishReason: "stop"}
		got, matched := anthropicStopReason(&resp, []string{"<END>"})
		if got != "stop_sequence" || matched == nil || *matched != "<END>" {
			t.Fatalf("stop_reason = %q matched = %v", got, matched)
		}
	})
}

func TestBuildAnthropicResponse(t *testing.T) {
	resp := &service.LLMResponse{
		Content:   "hello",
		ToolCalls: []service.ToolCall{{ID: "toolu_1", Name: "screenshot", Arguments: map[string]any{"url": "a"}}},
		Usage:     service.Usage{PromptTokens: 12, CompletionTokens: 5, CacheReadTokens: 7},
	}

	body := buildAnthropicResponse("msg_1", "claude-sonnet", resp, nil)

	if body.Type != "message" || body.Role != "assistant" || body.Model != "claude-sonnet" {
		t.Fatalf("envelope = %+v", body)
	}
	if len(body.Content) != 2 || body.Content[0]["type"] != "text" || body.Content[1]["type"] != "tool_use" {
		t.Fatalf("content = %+v", body.Content)
	}
	if body.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %q", body.StopReason)
	}
	if body.Usage.InputTokens != 12 || body.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v", body.Usage)
	}
	if body.Usage.CacheReadInputTokens == nil || *body.Usage.CacheReadInputTokens != 7 {
		t.Fatalf("cache usage = %+v", body.Usage)
	}
}

// A refusal must be visible; reporting an empty turn makes it indistinguishable
// from a model that said nothing.
func TestBuildAnthropicResponseSurfacesRefusal(t *testing.T) {
	body := buildAnthropicResponse("msg_1", "m", &service.LLMResponse{Refusal: "I cannot help with that"}, nil)

	if len(body.Content) != 1 || body.Content[0]["text"] != "I cannot help with that" {
		t.Fatalf("refusal dropped: %+v", body.Content)
	}
}

func TestAnthropicErrorTypeForStatus(t *testing.T) {
	cases := map[int]string{
		400: anthropicErrInvalidRequest,
		401: anthropicErrAuthentication,
		403: anthropicErrPermission,
		404: anthropicErrNotFound,
		429: anthropicErrRateLimit,
		500: anthropicErrAPI,
		502: anthropicErrAPI,
		529: anthropicErrOverloaded,
	}
	for status, want := range cases {
		if got := anthropicErrorTypeForStatus(status); got != want {
			t.Errorf("status %d = %q want %q", status, got, want)
		}
	}
}

func TestAnthropicChatOptionsMapping(t *testing.T) {
	req := decodeAnthropicRequest(t, `{
		"model":"m","max_tokens":512,"temperature":0.3,"top_p":0.8,"top_k":40,
		"stop_sequences":["<END>"],
		"tool_choice":{"type":"tool","name":"screenshot"},
		"thinking":{"type":"enabled","budget_tokens":2048}
	}`)

	opts := buildAnthropicChatOptions(req)

	if opts.MaxTokens == nil || *opts.MaxTokens != 512 {
		t.Fatalf("max_tokens = %v", opts.MaxTokens)
	}
	if opts.Temperature == nil || *opts.Temperature != 0.3 {
		t.Fatalf("temperature = %v", opts.Temperature)
	}
	if len(opts.Stop) != 1 || opts.Stop[0] != "<END>" {
		t.Fatalf("stop = %v", opts.Stop)
	}
	choice, ok := opts.ToolChoice.(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v", opts.ToolChoice)
	}
	fn, _ := choice["function"].(map[string]any)
	if fn == nil || fn["name"] != "screenshot" {
		t.Fatalf("tool_choice = %+v", choice)
	}
	if opts.Thinking == nil || opts.Thinking.BudgetTokens != 2048 {
		t.Fatalf("thinking = %+v", opts.Thinking)
	}
	// top_k has no ChatOptions field; extra_body is the documented route.
	if opts.ExtraBody == nil || opts.ExtraBody["top_k"] != 40 {
		t.Fatalf("extra_body = %+v", opts.ExtraBody)
	}
}

func TestAnthropicToolChoiceMapping(t *testing.T) {
	cases := []struct {
		in   *anthropicToolChoig
		want any
	}{
		{nil, nil},
		{&anthropicToolChoig{Type: "auto"}, "auto"},
		{&anthropicToolChoig{Type: "any"}, "required"},
		{&anthropicToolChoig{Type: "none"}, "none"},
		{&anthropicToolChoig{Type: "tool"}, "required"}, // no name given
	}
	for _, tt := range cases {
		if got := anthropicToolChoiceToOpenAI(tt.in); got != tt.want {
			t.Errorf("%+v = %v want %v", tt.in, got, tt.want)
		}
	}
}

var _ = context.Background
