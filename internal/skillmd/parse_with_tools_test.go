package skillmd

import (
	"testing"
)

func TestParseWithTools_FullSkill(t *testing.T) {
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
	if s.Body != "You are a web scraping skill." {
		t.Errorf("body = %q, want %q", s.Body, "You are a web scraping skill.")
	}
	if len(tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(tools))
	}
	if tools[0].Name != "scrape_url" {
		t.Errorf("tool name = %q, want %q", tools[0].Name, "scrape_url")
	}
	if tools[0].HandlerType != "js" {
		t.Errorf("tool handler_type = %q, want %q", tools[0].HandlerType, "js")
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

func TestParseWithTools_MultipleTools(t *testing.T) {
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

	if len(tools) != 2 {
		t.Fatalf("tools count = %d, want 2", len(tools))
	}
	if tools[0].Name != "tool_a" {
		t.Errorf("first tool name = %q, want %q", tools[0].Name, "tool_a")
	}
	if tools[1].Name != "tool_b" {
		t.Errorf("second tool name = %q, want %q", tools[1].Name, "tool_b")
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
