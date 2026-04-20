package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// newSSEServer returns a test HTTP server that replies to any request with
// a pre-baked SSE stream. The caller provides a slice of "data: ..." payload
// lines; the server handles framing (blank line between events, [DONE]).
func newSSEServer(t *testing.T, events []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, ev := range events {
			fmt.Fprintf(w, "data: %s\n\n", ev)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
}

func collectStream(t *testing.T, ch <-chan service.StreamChunk) []service.StreamChunk {
	t.Helper()
	var out []service.StreamChunk
	for c := range ch {
		out = append(out, c)
	}
	return out
}

// TestChatStreamAccumulatesToolCallFragments reproduces OpenAI's real
// streaming wire format: tool calls are split across many SSE deltas with
// partial JSON "arguments" strings keyed by index. The provider must
// reassemble them into a single ToolCall with complete Arguments.
func TestChatStreamAccumulatesToolCallFragments(t *testing.T) {
	// Wire events that mirror OpenAI's documented streaming format.
	// The first delta announces id+name; subsequent deltas carry partial
	// JSON argument chunks; a final chunk carries finish_reason="tool_calls".
	events := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"ci"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ty\":"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}

	srv := newSSEServer(t, events)
	defer srv.Close()

	p, err := New("test-key", "gpt-4o", srv.URL, "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, _, err := p.ChatStream(context.Background(), "gpt-4o",
		[]service.Message{{Role: "user", Content: "weather in Paris?"}},
		nil, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	chunks := collectStream(t, ch)

	// Collect every tool call emitted across the full stream.
	var toolCalls []service.ToolCall
	var finishReason string
	for _, c := range chunks {
		if c.Error != nil {
			t.Fatalf("stream error: %v", c.Error)
		}
		toolCalls = append(toolCalls, c.ToolCalls...)
		if c.FinishReason != "" {
			finishReason = c.FinishReason
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 accumulated tool call, got %d: %+v", len(toolCalls), toolCalls)
	}
	tc := toolCalls[0]
	if tc.ID != "call_abc" {
		t.Errorf("tool call id: got %q want %q", tc.ID, "call_abc")
	}
	if tc.Name != "get_weather" {
		t.Errorf("tool call name: got %q want %q", tc.Name, "get_weather")
	}
	if tc.Arguments == nil {
		t.Fatalf("tool call arguments is nil — fragments were not accumulated")
	}
	if city, _ := tc.Arguments["city"].(string); city != "Paris" {
		t.Errorf("tool call arguments[city]: got %v want %q", tc.Arguments["city"], "Paris")
	}
	if finishReason != "tool_calls" {
		t.Errorf("finish_reason: got %q want %q", finishReason, "tool_calls")
	}
}

// TestChatStreamAccumulatesMultipleToolCalls verifies two parallel tool
// calls at different indices are each reassembled independently.
func TestChatStreamAccumulatesMultipleToolCalls(t *testing.T) {
	events := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"a","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"b","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\":1}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"y\":2}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}

	srv := newSSEServer(t, events)
	defer srv.Close()

	p, err := New("test-key", "gpt-4o", srv.URL, "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, _, err := p.ChatStream(context.Background(), "gpt-4o",
		[]service.Message{{Role: "user", Content: "do two things"}}, nil, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var toolCalls []service.ToolCall
	for c := range ch {
		if c.Error != nil {
			t.Fatalf("stream error: %v", c.Error)
		}
		toolCalls = append(toolCalls, c.ToolCalls...)
	}

	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d: %+v", len(toolCalls), toolCalls)
	}
	if toolCalls[0].ID != "call_1" || toolCalls[0].Name != "a" {
		t.Errorf("first tool call: %+v", toolCalls[0])
	}
	if toolCalls[1].ID != "call_2" || toolCalls[1].Name != "b" {
		t.Errorf("second tool call: %+v", toolCalls[1])
	}
	if x, _ := toolCalls[0].Arguments["x"].(float64); x != 1 {
		t.Errorf("call_1 arguments: %+v", toolCalls[0].Arguments)
	}
	if y, _ := toolCalls[1].Arguments["y"].(float64); y != 2 {
		t.Errorf("call_2 arguments: %+v", toolCalls[1].Arguments)
	}
}

// TestChatStreamEmitsTextDeltas verifies ordinary content deltas still
// flow through unchanged.
func TestChatStreamEmitsTextDeltas(t *testing.T) {
	events := []string{
		`{"choices":[{"delta":{"role":"assistant","content":"Hel"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"content":"lo "},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"content":"world"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	}

	srv := newSSEServer(t, events)
	defer srv.Close()

	p, err := New("k", "m", srv.URL, "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, _, err := p.ChatStream(context.Background(), "m",
		[]service.Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var text strings.Builder
	var finish string
	for c := range ch {
		if c.Error != nil {
			t.Fatalf("stream error: %v", c.Error)
		}
		text.WriteString(c.Content)
		if c.FinishReason != "" {
			finish = c.FinishReason
		}
	}

	if text.String() != "Hello world" {
		t.Errorf("content: got %q want %q", text.String(), "Hello world")
	}
	if finish != "stop" {
		t.Errorf("finish_reason: got %q want %q", finish, "stop")
	}
}

// TestChatStreamFlushesOnScannerEndWithoutDone guards against providers
// that close the SSE stream without sending the [DONE] terminator — we
// should still emit any accumulated tool calls.
func TestChatStreamFlushesOnScannerEndWithoutDone(t *testing.T) {
	events := []string{
		`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"f","arguments":"{\"k\":\"v\"}"}}]},"finish_reason":null}]}`,
		// No finish_reason chunk, no [DONE]. Just EOF.
	}

	srv := newSSEServer(t, events)
	defer srv.Close()

	p, err := New("k", "m", srv.URL, "", false, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, _, err := p.ChatStream(context.Background(), "m",
		[]service.Message{{Role: "user", Content: "x"}}, nil, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var toolCalls []service.ToolCall
	for c := range ch {
		if c.Error != nil {
			t.Fatalf("stream error: %v", c.Error)
		}
		toolCalls = append(toolCalls, c.ToolCalls...)
	}

	if len(toolCalls) != 1 {
		t.Fatalf("expected tool call flushed on EOF, got %d: %+v", len(toolCalls), toolCalls)
	}
	if v, _ := toolCalls[0].Arguments["k"].(string); v != "v" {
		t.Errorf("arguments: %+v", toolCalls[0].Arguments)
	}
}
