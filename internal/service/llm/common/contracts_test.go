package common

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

type closeSignal struct {
	once   sync.Once
	closed chan struct{}
}

func (s *closeSignal) Close() error { s.once.Do(func() { close(s.closed) }); return nil }

func TestStreamCancellationUnblocksProducer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &closeSignal{closed: make(chan struct{})}
	source := make(chan service.StreamChunk, 1)
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		defer close(source)
		for i := 0; i < 1000; i++ {
			source <- service.StreamChunk{Content: "x"}
		}
	}()
	out := StreamWithContext(ctx, source, body)
	// Never read from out before cancelling: exercise a blocked downstream.
	cancel()
	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("producer/limiter stranded after cancellation")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("HTTP body not closed")
	}
	for range out {
	}
}

func TestContentConversionPreservesMediaAndToolOrder(t *testing.T) {
	messages := ConvertContentBlocksToOpenAI("user", []service.ContentBlock{
		{Type: "text", Text: "additional context"},
		{Type: "image", Source: &service.MediaSource{Type: "base64", MediaType: "image/png", Data: "aW1n"}},
		{Type: "tool_result", ToolUseID: "call_1", Content: "result"},
	})
	if len(messages) != 2 || messages[0]["role"] != "tool" || messages[0]["tool_call_id"] != "call_1" {
		t.Fatalf("tool result adjacency lost: %#v", messages)
	}
	parts := messages[1]["content"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["image_url"].(map[string]any)["url"] != "data:image/png;base64,aW1n" {
		t.Fatalf("image lost: %#v", parts)
	}
	call := ConvertContentBlocksToOpenAI("assistant", []service.ContentBlock{{Type: "tool_use", ID: "call_1", Name: "ping"}})
	if call[0]["tool_calls"].([]map[string]any)[0]["function"].(map[string]any)["arguments"] != "{}" {
		t.Fatal("nil arguments encoded as null")
	}
}

func TestParseToolArguments(t *testing.T) {
	for _, tt := range []struct {
		raw   string
		valid bool
	}{{"", true}, {"{}", true}, {`{"a":1}`, true}, {"null", false}, {"[]", false}, {`{"a":`, false}} {
		t.Run(tt.raw, func(t *testing.T) {
			args, err := ParseToolArguments(tt.raw)
			if (err == nil) != tt.valid || (tt.valid && args == nil) {
				t.Fatalf("args=%v error=%v", args, err)
			}
		})
	}
}
