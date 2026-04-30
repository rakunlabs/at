package openaicompatible

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSSEServer spins up an httptest server that returns the given SSE body
// for POST /chat/completions and a 404 for everything else.
func fakeSSEServer(t *testing.T, body string) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	client, err := New(
		WithBaseURL(srv.URL),
		WithModel("test-model"),
		WithDisableRetry(true),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, client
}

func TestAccumulateStream_TextContent(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":", "}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"world!"}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`,
		`data: [DONE]`,
		``,
	}, "\n\n")

	_, client := fakeSSEServer(t, body)

	stream, err := client.ChatStream(context.Background(), &ChatRequest{
		Messages: []Message{UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer stream.Close()

	resp, err := AccumulateStream(stream, nil)
	if err != nil {
		t.Fatalf("AccumulateStream: %v", err)
	}
	if got := resp.Content(); got != "Hello, world!" {
		t.Errorf("Content() = %q, want %q", got, "Hello, world!")
	}
	if resp.FirstChoice().FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.FirstChoice().FinishReason)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 7 {
		t.Errorf("Usage = %+v, want TotalTokens=7", resp.Usage)
	}
}

func TestAccumulateStream_ToolCallReassembly(t *testing.T) {
	// Real OpenAI splits a single tool call across many small deltas: the
	// first carries id+name with empty arguments; subsequent deltas carry
	// partial arguments string fragments keyed by index.
	body := strings.Join([]string{
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ci"}}]}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":"}}]}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Ist"}}]}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"anbul\"}"}}]}}]}`,
		`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n")

	_, client := fakeSSEServer(t, body)

	stream, err := client.ChatStream(context.Background(), &ChatRequest{
		Messages: []Message{UserMessage("weather?")},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer stream.Close()

	resp, err := AccumulateStream(stream, nil)
	if err != nil {
		t.Fatalf("AccumulateStream: %v", err)
	}

	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1: %+v", len(calls), calls)
	}
	tc := calls[0]
	if tc.ID != "call_1" || tc.Function.Name != "get_weather" {
		t.Errorf("tool call header wrong: id=%q name=%q", tc.ID, tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"Istanbul"}` {
		t.Errorf("Arguments = %q, want %q", tc.Function.Arguments, `{"city":"Istanbul"}`)
	}
	args, err := tc.ArgumentsMap()
	if err != nil {
		t.Fatalf("ArgumentsMap: %v", err)
	}
	if args["city"] != "Istanbul" {
		t.Errorf("ArgumentsMap()[city] = %v, want Istanbul", args["city"])
	}
}

func TestAccumulateStream_MultipleChoices(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"x","choices":[{"index":0,"delta":{"role":"assistant","content":"A"}},{"index":1,"delta":{"role":"assistant","content":"B"}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{"content":"1"}},{"index":1,"delta":{"content":"2"}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{},"finish_reason":"stop"},{"index":1,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n")

	_, client := fakeSSEServer(t, body)

	stream, err := client.ChatStream(context.Background(), &ChatRequest{
		Messages: []Message{UserMessage("hi")},
		N:        new(2),
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer stream.Close()

	resp, err := AccumulateStream(stream, nil)
	if err != nil {
		t.Fatalf("AccumulateStream: %v", err)
	}
	if len(resp.Choices) != 2 {
		t.Fatalf("got %d choices, want 2", len(resp.Choices))
	}
	if got, _ := resp.Choices[0].Message.Content.(string); got != "A1" {
		t.Errorf("choice 0 content = %q, want A1", got)
	}
	if got, _ := resp.Choices[1].Message.Content.(string); got != "B2" {
		t.Errorf("choice 1 content = %q, want B2", got)
	}
}

func TestStream_OnChunkCallback(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"x","choices":[{"index":0,"delta":{"content":"a"}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{"content":"b"}}]}`,
		`data: {"id":"x","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n")

	_, client := fakeSSEServer(t, body)

	stream, err := client.ChatStream(context.Background(), &ChatRequest{
		Messages: []Message{UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer stream.Close()

	var seen []string
	_, err = AccumulateStream(stream, func(ev *StreamEvent) {
		for _, c := range ev.Choices {
			if c.Delta.Content != "" {
				seen = append(seen, c.Delta.Content)
			}
		}
	})
	if err != nil {
		t.Fatalf("AccumulateStream: %v", err)
	}
	if got := strings.Join(seen, ""); got != "ab" {
		t.Errorf("onChunk saw %q, want ab", got)
	}
}

func TestChat_RateLimitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
	}))
	defer srv.Close()

	client, err := New(
		WithBaseURL(srv.URL),
		WithModel("test"),
		WithDisableRetry(true),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), &ChatRequest{
		Messages: []Message{UserMessage("hi")},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsRateLimit(err) {
		t.Fatalf("IsRateLimit(%v) = false, want true", err)
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("errors.As to *RateLimitError failed: %v", err)
	}
	if rle.RetryAfter.Seconds() != 12 {
		t.Errorf("RetryAfter = %s, want 12s", rle.RetryAfter)
	}
	if rle.Type != "rate_limit_error" {
		t.Errorf("Type = %q, want rate_limit_error", rle.Type)
	}
}

func TestChat_PassesRequestBody(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","object":"chat.completion","model":"test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	client, _ := New(
		WithBaseURL(srv.URL),
		WithModel("test"),
		WithDisableRetry(true),
	)

	_, err := client.Chat(context.Background(), &ChatRequest{
		Messages:    []Message{UserMessage("hi")},
		Temperature: new(0.5),
		Extra: map[string]any{
			"thinking":           map[string]any{"type": "enabled", "budget_tokens": 2048},
			"web_search_options": map[string]any{"search_context_size": "medium"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if got["model"] != "test" {
		t.Errorf("model wire = %v, want test", got["model"])
	}
	if got["temperature"].(float64) != 0.5 {
		t.Errorf("temperature wire = %v, want 0.5", got["temperature"])
	}
	if got["stream"] != nil && got["stream"] != false {
		t.Errorf("stream wire = %v, want false/absent", got["stream"])
	}
	// Extra must be merged into the body.
	if got["thinking"] == nil {
		t.Errorf("Extra.thinking not merged: %v", got)
	}
	if got["web_search_options"] == nil {
		t.Errorf("Extra.web_search_options not merged: %v", got)
	}
}

func TestNew_StripsTrailingChatCompletions(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1"},
		{"https://api.openai.com/v1/chat/completions/", "https://api.openai.com/v1"},
		{"https://api.openai.com/v1/", "https://api.openai.com/v1"},
		{"https://api.openai.com/v1", "https://api.openai.com/v1"},
	}
	for _, tt := range cases {
		t.Run(tt.in, func(t *testing.T) {
			c, err := New(WithBaseURL(tt.in), WithDisableRetry(true))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if c.BaseURL() != tt.want {
				t.Errorf("BaseURL() = %q, want %q", c.BaseURL(), tt.want)
			}
		})
	}
}

// silence unused-import warnings on environments where fmt is otherwise unreferenced.
var _ = fmt.Sprintf
