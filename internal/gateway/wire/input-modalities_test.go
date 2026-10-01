package wire

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestRequestInputModalities(t *testing.T) {
	msgs := []OpenAIMessage{
		{Role: "system", Content: json.RawMessage(`"be brief"`)},
		{Role: "user", Content: json.RawMessage(`[
			{"type":"text","text":"read"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,AA"}},
			{"type":"file","file":{"filename":"a.pdf","file_data":"data:application/pdf;base64,AA"}},
			{"type":"file","file":{"filename":"notes.txt","file_data":"data:text/plain;base64,AA"}},
			{"type":"input_file","filename":"q.sql","file_data":"data:application/sql;base64,AA"},
			{"type":"input_file","filename":"data.json","file_data":"data:application/json;base64,AA"},
			{"type":"input_file","filename":"report.docx"}
		]`)},
		{Role: "tool", ToolCallID: "c1", Content: json.RawMessage(`[{"type":"input_audio","input_audio":{"data":"AA","format":"wav"}}]`)},
	}
	if got := RequestInputModalities(msgs); !slices.Equal(got, []string{"image", "pdf", "audio"}) {
		t.Fatalf("modalities = %v", got)
	}
	if got := RequestInputModalities([]OpenAIMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}}); len(got) != 0 {
		t.Fatalf("text-only = %v", got)
	}

	anth := []AnthropicMessage{{Role: "user", Content: json.RawMessage(`[
		{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AA"}},
		{"type":"tool_result","tool_use_id":"x","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA"}}]}
	]`)}}
	if got := AnthropicInputModalities(anth); !slices.Equal(got, []string{"image", "pdf"}) {
		t.Fatalf("anthropic modalities = %v", got)
	}
}
