package server

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func toolMessage(content string) []OpenAIMessage {
	return []OpenAIMessage{
		{Role: "user", Content: json.RawMessage(`"take a screenshot"`)},
		{Role: "assistant", ToolCalls: []OpenAIToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: OpenAIFunctionCall{Name: "screenshot", Arguments: "{}"},
		}}},
		{Role: "tool", ToolCallID: "call_1", Content: json.RawMessage(content)},
	}
}

func toolResultBlock(t *testing.T, messages []service.Message) service.ContentBlock {
	t.Helper()

	for _, m := range messages {
		blocks, ok := m.Content.([]service.ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				return b
			}
		}
	}
	t.Fatalf("no tool_result block in %+v", messages)

	return service.ContentBlock{}
}

// A text-only tool result must produce exactly the string it always did, so the
// change is invisible to every existing client.
func TestToolResultTextOnlyIsUnchanged(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"plain string", `"exit code 0"`, "exit code 0"},
		{"single text part", `[{"type":"text","text":"exit code 0"}]`, "exit code 0"},
		{"concatenated text parts", `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, "ab"},
		{"empty", `""`, ""},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, messages := translateOpenAIToAnthropic(toolMessage(tt.content))
			block := toolResultBlock(t, messages)

			got, isString := block.Content.(string)
			if !isString {
				t.Fatalf("a text-only result must stay a string, got %T", block.Content)
			}
			if got != tt.want {
				t.Fatalf("content = %q want %q", got, tt.want)
			}
		})
	}
}

// The defect this fixes: a non-text part in a tool result was concatenated away.
func TestToolResultPreservesImagePart(t *testing.T) {
	const content = `[
		{"type":"text","text":"here it is"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,aGVsbG8="}}
	]`

	_, messages := translateOpenAIToAnthropic(toolMessage(content))
	block := toolResultBlock(t, messages)

	blocks, structured := block.ContentBlocks()
	if !structured {
		t.Fatalf("a multimodal result must be structured, got %T", block.Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v want text + image", blocks)
	}
	if blocks[0].Type != "text" || blocks[0].Text != "here it is" {
		t.Fatalf("text part lost: %+v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Source == nil {
		t.Fatalf("image part lost: %+v", blocks[1])
	}
	if blocks[1].Source.MediaType != "image/png" || blocks[1].Source.Data != "aGVsbG8=" {
		t.Fatalf("image source = %+v", blocks[1].Source)
	}
}

// Anthropic-shaped parts are accepted verbatim, which is the form an
// Anthropic-native client sends.
func TestToolResultPreservesAnthropicSourceBlock(t *testing.T) {
	const content = `[
		{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"Zm9v"}}
	]`

	_, messages := translateOpenAIToAnthropic(toolMessage(content))
	blocks, structured := toolResultBlock(t, messages).ContentBlocks()
	if !structured || len(blocks) != 1 {
		t.Fatalf("blocks = %+v structured=%v", blocks, structured)
	}
	if blocks[0].Source == nil || blocks[0].Source.MediaType != "image/jpeg" {
		t.Fatalf("source = %+v", blocks[0].Source)
	}
}

func TestMediaSourceFromURL(t *testing.T) {
	t.Run("data url becomes inline base64", func(t *testing.T) {
		got := mediaSourceFromURL("data:image/webp;base64,QUJD")
		if got == nil || got.Type != "base64" || got.MediaType != "image/webp" || got.Data != "QUJD" {
			t.Fatalf("source = %+v", got)
		}
	})

	t.Run("remote url is passed through as a reference", func(t *testing.T) {
		got := mediaSourceFromURL("https://example.test/a.png")
		if got == nil || got.Type != "url" || got.URL != "https://example.test/a.png" {
			t.Fatalf("source = %+v", got)
		}
	})

	t.Run("malformed data urls are refused rather than guessed", func(t *testing.T) {
		for _, raw := range []string{"data:image/png;base64", "data:image/png,notbase64", "data:image/png;base64,"} {
			if got := mediaSourceFromURL(raw); got != nil {
				t.Fatalf("%q produced %+v, want nil", raw, got)
			}
		}
	})
}
