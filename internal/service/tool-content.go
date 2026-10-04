package service

import (
	"context"
	"strings"
	"sync"
)

// ToolContentCollector gathers non-text MCP content (images, audio) produced
// while one tool call runs. Tool executors return a string, which is all the
// agent loops can carry; a caller that can deliver richer content — the
// gateway MCP endpoint — installs a collector and appends what it gathered to
// the text result. Without a collector the content is simply not offered.
type ToolContentCollector struct {
	mu      sync.Mutex
	content []ToolContent
}

type toolContentCollectorKey struct{}

// ContextWithToolContentCollector installs a fresh collector.
func ContextWithToolContentCollector(ctx context.Context) (context.Context, *ToolContentCollector) {
	collector := &ToolContentCollector{}
	return context.WithValue(ctx, toolContentCollectorKey{}, collector), collector
}

// ToolContentCollectorFromContext reports the active collector, if any.
func ToolContentCollectorFromContext(ctx context.Context) *ToolContentCollector {
	collector, _ := ctx.Value(toolContentCollectorKey{}).(*ToolContentCollector)
	return collector
}

// AddToolContent appends content to the active collector and reports whether
// one was present.
func AddToolContent(ctx context.Context, content ...ToolContent) bool {
	collector := ToolContentCollectorFromContext(ctx)
	if collector == nil {
		return false
	}
	collector.mu.Lock()
	defer collector.mu.Unlock()
	collector.content = append(collector.content, content...)
	return true
}

// Content returns the gathered content in arrival order.
func (c *ToolContentCollector) Content() []ToolContent {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ToolContent(nil), c.content...)
}

// toolResultText joins the text blocks of an MCP tool result and hands any
// image or audio blocks to the active collector. Upstream servers routinely
// split a result over several text blocks, so keeping only the first lost the
// rest of the answer.
func toolResultText(ctx context.Context, result CallToolResult) string {
	var text []string
	for _, block := range result.Content {
		switch block.Type {
		case "text", "":
			if block.Text != "" {
				text = append(text, block.Text)
			}
		case "image", "audio":
			if block.Data != "" && block.MimeType != "" {
				AddToolContent(ctx, ToolContent{Type: block.Type, Data: block.Data, MimeType: block.MimeType})
			}
		}
	}
	return strings.Join(text, "\n")
}
