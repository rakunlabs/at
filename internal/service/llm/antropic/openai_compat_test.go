package antropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestBuildRequestBodyOAuthForcedToolChoice(t *testing.T) {
	p := &Provider{MaxTokens: 1024, tokenSource: NewStaticTokenSource("test-token")}
	parallel := false
	opts := &service.ChatOptions{
		ToolChoice:        map[string]any{"type": "function", "function": map[string]any{"name": "read_file"}},
		ParallelToolCalls: &parallel,
	}
	body := p.buildRequestBody("claude-opus-5", []service.Message{{Role: "user", Content: "Read the file"}},
		[]service.Tool{{Name: "read_file", InputSchema: map[string]any{"type": "object"}}}, opts)
	choice := body["tool_choice"].(map[string]any)
	tool := body["tools"].([]any)[0].(map[string]any)
	if choice["type"] != "tool" || choice["name"] != tool["name"] || choice["disable_parallel_tool_use"] != true {
		t.Fatalf("tool_choice does not match OAuth tool definition: choice=%v tool=%v", choice, tool)
	}
	if opts.ToolChoice.(map[string]any)["function"].(map[string]any)["name"] != "read_file" {
		t.Fatal("caller tool_choice was mutated")
	}
}

func TestChatStreamToolArguments(t *testing.T) {
	for _, tt := range []struct {
		name  string
		args  string
		valid bool
	}{
		{name: "truncated", args: `{"path":`},
		{name: "array", args: `[]`},
		{name: "null", args: `null`},
		{name: "no arguments", valid: true},
		{name: "empty object", args: `{}`, valid: true},
		{name: "object", args: `{"path":"file.txt"}`, valid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintln(w, `data: {"type":"content_block_start","content_block":{"type":"tool_use","id":"call_1","name":"read_file","input":{}}}`)
				// %q is sufficient for these ASCII JSON fixtures.
				fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%q}}\n", tt.args)
				fmt.Fprintln(w, "data: {\"type\":\"content_block_stop\"}\n\ndata: {\"type\":\"message_stop\"}")
			}))
			defer srv.Close()
			p, err := New("test-key", "claude-test", srv.URL, "", false)
			if err != nil {
				t.Fatal(err)
			}
			chunks, _, err := p.ChatStream(context.Background(), "", []service.Message{{Role: "user", Content: "Read"}}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var streamErr error
			var calls []service.ToolCall
			for chunk := range chunks {
				calls = append(calls, chunk.ToolCalls...)
				if chunk.Error != nil {
					streamErr = chunk.Error
				}
			}
			if tt.valid {
				if streamErr != nil || len(calls) != 1 || calls[0].Arguments == nil {
					t.Fatalf("valid tool call failed: calls=%+v error=%v", calls, streamErr)
				}
				if tt.name == "object" && calls[0].Arguments["path"] != "file.txt" {
					t.Fatalf("arguments lost: %+v", calls[0])
				}
				return
			}
			if len(calls) != 0 {
				t.Errorf("invalid arguments emitted as executable tool call: %+v", calls)
			}
			if streamErr == nil || !strings.Contains(streamErr.Error(), "tool arguments") {
				t.Fatalf("expected tool arguments error, got %v", streamErr)
			}
		})
	}
}

func TestTranslateAnthropicToolChoice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want map[string]any
	}{
		{"string auto", "auto", map[string]any{"type": "auto"}},
		{"string none", "none", map[string]any{"type": "none"}},
		{"string required → any", "required", map[string]any{"type": "any"}},
		{"openai function object", map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "foo"},
		}, map[string]any{"type": "tool", "name": "foo"}},
		{"anthropic-shape passthrough", map[string]any{"type": "tool", "name": "bar"}, map[string]any{"type": "tool", "name": "bar"}},
		{"unknown string nil", "weird", nil},
		{"object without name nil", map[string]any{"type": "function"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateAnthropicToolChoice(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnthropicResponseFormatInstruction(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"unset", nil, ""},
		{"json_object", map[string]any{"type": "json_object"}, "Respond with a single JSON object and nothing else. Do not wrap the JSON in markdown fences or prose."},
		{"json_schema simple", map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "Output",
				"schema": map[string]any{"type": "object"},
			},
		}, ""}, // we don't check exact body, just non-empty
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anthropicResponseFormatInstruction(tt.in)
			if tt.want == "" && tt.in == nil {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
				return
			}
			if tt.name == "json_object" {
				if got != tt.want {
					t.Errorf("got %q, want %q", got, tt.want)
				}
				return
			}
			if got == "" {
				t.Error("expected non-empty json_schema instruction")
			}
		})
	}
}
