package service

import (
	"context"
	"testing"
)

func TestToolResultText(t *testing.T) {
	result := CallToolResult{Content: []ToolContent{
		{Type: "text", Text: "first"},
		{Type: "image", Data: "aW1n", MimeType: "image/png"},
		{Type: "text", Text: "second"},
		{Type: "image", Data: "", MimeType: "image/png"},
		{Type: "resource"},
	}}

	if got := toolResultText(context.Background(), result); got != "first\nsecond" {
		t.Fatalf("text without collector = %q", got)
	}

	ctx, collector := ContextWithToolContentCollector(context.Background())
	if got := toolResultText(ctx, result); got != "first\nsecond" {
		t.Fatalf("text = %q", got)
	}
	content := collector.Content()
	if len(content) != 1 || content[0].Type != "image" || content[0].Data != "aW1n" || content[0].MimeType != "image/png" {
		t.Fatalf("collected = %+v", content)
	}
	if AddToolContent(context.Background(), ToolContent{Type: "image"}) {
		t.Fatal("content accepted without a collector")
	}
}
