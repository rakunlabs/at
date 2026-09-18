package bedrock

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// A string tool result must still produce exactly one text block.
func TestConverseToolResultStringUnchanged(t *testing.T) {
	got := converseToolResultContent(service.ContentBlock{
		Type:      "tool_result",
		ToolUseID: "call_1",
		Content:   "exit code 0",
	})

	if len(got) != 1 || got[0].Text == nil || *got[0].Text != "exit code 0" {
		t.Fatalf("content = %+v", got)
	}
	if got[0].Image != nil || got[0].Document != nil {
		t.Fatalf("text result must not carry media: %+v", got[0])
	}
}

func TestConverseToolResultExpandsStructuredContent(t *testing.T) {
	got := converseToolResultContent(service.ContentBlock{
		Type:      "tool_result",
		ToolUseID: "call_1",
		Content: []service.ContentBlock{
			{Type: "text", Text: "here it is"},
			{Type: "image", Source: &service.MediaSource{Type: "base64", MediaType: "image/png", Data: "aGVsbG8="}},
			{Type: "document", Source: &service.MediaSource{Type: "base64", MediaType: "application/pdf", Data: "JVBERg=="}},
		},
	})

	if len(got) != 3 {
		t.Fatalf("content = %+v want three blocks", got)
	}
	if got[0].Text == nil || *got[0].Text != "here it is" {
		t.Fatalf("text block = %+v", got[0])
	}
	if got[1].Image == nil || got[1].Image.Source.Bytes != "aGVsbG8=" {
		t.Fatalf("image block = %+v", got[1])
	}
	if got[2].Document == nil || got[2].Document.Source.Bytes != "JVBERg==" {
		t.Fatalf("document block = %+v", got[2])
	}
}

// Converse rejects an empty tool result, so a structured result that yields no
// usable parts must still send something well-formed.
func TestConverseToolResultNeverEmpty(t *testing.T) {
	got := converseToolResultContent(service.ContentBlock{
		Type:      "tool_result",
		ToolUseID: "call_1",
		Content:   []service.ContentBlock{{Type: "image"}}, // no source
	})

	if len(got) != 1 || got[0].Text == nil || *got[0].Text != "" {
		t.Fatalf("content = %+v want a single empty text block", got)
	}
}
