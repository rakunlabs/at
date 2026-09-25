package skillmd

import (
	"strings"
	"testing"
)

func TestParseWithTools_PreservesToolLookingMarkdown(t *testing.T) {
	input := `---
name: web-scraper
description: Scrapes web pages
---

You are a web scraping skill.

## Tools

` + "```json\n" + `[
  {
    "name": "scrape_url",
    "description": "Scrape a URL",
    "inputSchema": {"type": "object"},
    "handler": "async function(args) { return args.url; }",
    "handler_type": "js"
  }
]
` + "```\n"

	s, tools, err := ParseWithTools([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.Name != "web-scraper" {
		t.Errorf("name = %q, want %q", s.Name, "web-scraper")
	}
	if s.Body != input[strings.Index(input, "You are a web scraping skill."):] {
		t.Errorf("body did not preserve Markdown verbatim: %q", s.Body)
	}
	if len(tools) != 0 {
		t.Fatalf("tools count = %d, want 0", len(tools))
	}
}

func TestParseWithTools_NoToolsSection(t *testing.T) {
	input := `---
name: simple
---

Just a system prompt.`

	s, tools, err := ParseWithTools([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.Name != "simple" {
		t.Errorf("name = %q, want %q", s.Name, "simple")
	}
	if s.Body != "Just a system prompt." {
		t.Errorf("body = %q, want %q", s.Body, "Just a system prompt.")
	}
	if len(tools) != 0 {
		t.Errorf("tools count = %d, want 0", len(tools))
	}
}

func TestParseWithTools_DoesNotInterpretMultipleTools(t *testing.T) {
	input := `---
name: multi-tool
---

System prompt here.

## Tools

` + "```json\n" + `[
  {"name": "tool_a", "description": "First tool"},
  {"name": "tool_b", "description": "Second tool"}
]
` + "```\n"

	_, tools, err := ParseWithTools([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tools) != 0 {
		t.Fatalf("tools count = %d, want 0", len(tools))
	}
}

func TestParseWithTools_InvalidJSON(t *testing.T) {
	input := `---
name: bad-json
---

Prompt text.

## Tools

` + "```json\n" + `this is not valid json
` + "```\n"

	s, tools, err := ParseWithTools([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Invalid JSON should be gracefully handled — body returned as-is.
	if s.Name != "bad-json" {
		t.Errorf("name = %q, want %q", s.Name, "bad-json")
	}
	if len(tools) != 0 {
		t.Errorf("tools count = %d, want 0 (invalid JSON)", len(tools))
	}
}
