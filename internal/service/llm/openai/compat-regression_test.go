package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestChatToolArgumentValidation(t *testing.T) {
	for _, backend := range []string{"openai", "codex"} {
		for _, tt := range []struct {
			name, args string
			valid      bool
		}{
			{name: "null", args: "null"},
			{name: "array", args: "[]"},
			{name: "partial", args: `{"path":`},
			{name: "empty", valid: true},
			{name: "object", args: `{"path":"a"}`, valid: true},
		} {
			t.Run(backend+"/"+tt.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					args, _ := json.Marshal(tt.args)
					if backend == "codex" {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read\",\"arguments\":%s}}\n\n", args)
						fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"choices":[{"message":{"tool_calls":[{"id":"call_1","function":{"name":"read","arguments":%s}}]},"finish_reason":"tool_calls"}]}`, args)
					}
				}))
				defer srv.Close()
				var p service.LLMProvider
				if backend == "codex" {
					p = NewCodexProvider("codex", "account", NewCodexTokenSource("token", "", "account", time.Time{}, nil), WithCodexBaseURL(srv.URL))
				} else {
					var err error
					p, err = New("token", "model", srv.URL, "", false, nil)
					if err != nil {
						t.Fatal(err)
					}
				}
				resp, err := p.Chat(context.Background(), "", nil, nil, nil)
				if !tt.valid {
					if err == nil {
						t.Fatalf("invalid arguments accepted: %+v", resp)
					}
					return
				}
				if err != nil || resp == nil || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments == nil {
					t.Fatalf("valid arguments lost: response=%+v error=%v", resp, err)
				}
			})
		}
	}
}

func TestOpenAIChatPreservesLengthWithoutPartialToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"tool_calls":[{"id":"call_1","function":{"name":"read","arguments":"{\"path\":"}}]},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`)
	}))
	defer srv.Close()
	p, err := New("token", "model", srv.URL, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Chat(context.Background(), "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != "length" || len(resp.ToolCalls) != 0 || resp.Usage.TotalTokens != 30 {
		t.Fatalf("truncation lost or partial tool call emitted: %+v", resp)
	}
}

func TestCodexEmptyToolHistoryUsesObject(t *testing.T) {
	input := codexInput([]service.Message{{Role: "assistant", Content: []service.ContentBlock{{Type: "tool_use", ID: "call_1", Name: "ping"}}}})
	if len(input) != 1 || input[0].(map[string]any)["arguments"] != "{}" {
		t.Fatalf("no-argument tool history must use an object: %#v", input)
	}
}

func TestCodexNativeMultipartInput(t *testing.T) {
	parts := []map[string]any{
		{"type": "input_image", "image_url": "https://example.com/image.png", "detail": "low"},
		{"type": "input_file", "file_id": "file_1"},
	}
	input := codexMapMessageInput(map[string]any{"type": "message", "role": "user", "content": parts})
	if len(input) != 1 {
		t.Fatalf("media-only message dropped: %#v", input)
	}
	got := input[0].(map[string]any)["content"].([]any)
	if len(got) != len(parts) || !reflect.DeepEqual(got[0], parts[0]) || !reflect.DeepEqual(got[1], parts[1]) {
		t.Fatalf("native media changed: %#v", got)
	}
}

func TestCodexGatewayMultipartInput(t *testing.T) {
	parts := []any{
		map[string]any{"type": "text", "text": "Compare these"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,aW1hZ2U=", "detail": "high"}},
		map[string]any{"type": "file", "file": map[string]any{"filename": "report.pdf", "file_data": "data:application/pdf;base64,cGRm"}},
		map[string]any{"type": "text", "text": "and summarize"},
	}
	request := buildCodexRequest("codex", []service.Message{{Role: "user", Content: map[string]any{"role": "user", "content": parts}}}, nil, nil)
	input := request["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("input = %#v", input)
	}
	want := []any{
		map[string]any{"type": "input_text", "text": "Compare these"},
		map[string]any{"type": "input_image", "image_url": "data:image/png;base64,aW1hZ2U=", "detail": "high"},
		map[string]any{"type": "input_file", "filename": "report.pdf", "file_data": "data:application/pdf;base64,cGRm"},
		map[string]any{"type": "input_text", "text": "and summarize"},
	}
	if got := input[0].(map[string]any)["content"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("multipart input lost: got %#v, want %#v", got, want)
	}
}

func TestCodexPreservesMultipleOutputParts(t *testing.T) {
	srv := newSSEServer(t, []string{
		`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"First"}`,
		`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"First"}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","content":[{"type":"output_text","text":"First"},{"type":"output_text","text":" second part"}]}}`,
		`{"type":"response.output_text.done","output_index":1,"content_index":0,"text":" second message"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","content":[{"type":"output_text","text":" second message"}]}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":2,"summary_index":0,"delta":"Reason one"}`,
		`{"type":"response.reasoning_summary_text.done","output_index":2,"summary_index":0,"text":"Reason one"}`,
		`{"type":"response.reasoning_summary_text.done","output_index":2,"summary_index":1,"text":" reason two"}`,
		`{"type":"response.completed","response":{"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":80},"output_tokens":10,"output_tokens_details":{"reasoning_tokens":5},"total_tokens":110}}}`,
	})
	defer srv.Close()
	p := NewCodexProvider("codex", "account", NewCodexTokenSource("token", "", "account", time.Time{}, nil), WithCodexBaseURL(srv.URL))
	resp, err := p.Chat(context.Background(), "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "First second part second message" || resp.ReasoningContent != "Reason one reason two" {
		t.Fatalf("output parts lost or duplicated: %+v", resp)
	}
	if resp.Usage.PromptTokens != 20 || resp.Usage.CacheReadTokens != 80 || resp.Usage.TotalTokens != 110 || resp.Usage.ReasoningTokens != 5 {
		t.Fatalf("incorrect cache/reasoning usage: %+v", resp.Usage)
	}
}

func TestOpenAIStreamUsageWithChoices(t *testing.T) {
	srv := newSSEServer(t, []string{
		`{"choices":[{"delta":{"content":"Hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_tokens_details":{"cached_tokens":80},"completion_tokens_details":{"reasoning_tokens":5}}}`,
		`[DONE]`,
	})
	defer srv.Close()
	p, err := New("token", "model", srv.URL, "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch, _, err := p.ChatStream(context.Background(), "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var usage *service.Usage
	var text, finish string
	for c := range ch {
		if c.Error != nil {
			t.Error(c.Error)
		}
		text += c.Content
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
		if c.Usage != nil {
			usage = c.Usage
		}
	}
	if text != "Hi" || finish != "stop" || usage == nil || usage.PromptTokens != 20 || usage.CacheReadTokens != 80 || usage.TotalTokens != 110 || usage.ReasoningTokens != 5 {
		t.Fatalf("lost output/usage: text=%q finish=%q usage=%+v", text, finish, usage)
	}
}

func TestOpenAIStreamToolArgumentValidation(t *testing.T) {
	for _, tt := range []struct {
		name, args, finish string
		wantError          bool
		wantCalls          int
	}{
		{"truncated", `{"path":`, "tool_calls", true, 0},
		{"null", `null`, "tool_calls", true, 0},
		{"array", `[]`, "tool_calls", true, 0},
		{"valid", `{"path":"a"}`, "tool_calls", false, 1},
		{"empty", `{}`, "tool_calls", false, 1},
		{"length", `{"path":`, "length", false, 0},
		{"filtered", `{"path":`, "content_filter", false, 0},
		{"bad EOF", `{"path":`, "", true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args, _ := json.Marshal(tt.args)
			events := []string{fmt.Sprintf(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read","arguments":%s}}]},"finish_reason":null}]}`, args)}
			if tt.finish != "" {
				events = append(events, fmt.Sprintf(`{"choices":[{"delta":{},"finish_reason":%q}]}`, tt.finish), `[DONE]`)
			}
			srv := newSSEServer(t, events)
			defer srv.Close()
			p, err := New("token", "model", srv.URL, "", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			ch, _, err := p.ChatStream(context.Background(), "", nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var streamErr error
			var calls []service.ToolCall
			var finish string
			for c := range ch {
				if c.Error != nil {
					streamErr = c.Error
				}
				calls = append(calls, c.ToolCalls...)
				if c.FinishReason != "" {
					finish = c.FinishReason
				}
			}
			if (streamErr != nil) != tt.wantError || len(calls) != tt.wantCalls {
				t.Fatalf("calls=%+v error=%v", calls, streamErr)
			}
			if !tt.wantError && finish != tt.finish {
				t.Fatalf("finish=%q, want %q", finish, tt.finish)
			}
		})
	}
}
