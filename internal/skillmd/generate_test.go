package skillmd

import (
	"strings"
	"testing"
)

func TestGenerate_RejectsExecutableTools(t *testing.T) {
	s := &SkillMD{
		Name:        "web-scraper",
		Description: "Scrapes web pages for content",
		License:     "MIT",
		Body:        "You are a web scraping skill.\n",
	}
	tools := []ToolDef{
		{
			Name:        "scrape_url",
			Description: "Scrape content from a URL",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string"},
				},
				"required": []any{"url"},
			},
			Handler:     "async function(args) { return args.url; }",
			HandlerType: "js",
		},
	}

	if _, err := Generate(s, tools); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Generate() error = %v, want unsupported tools", err)
	}
}

func TestGenerate_NoTools(t *testing.T) {
	s := &SkillMD{
		Name: "simple",
		Body: "Just a system prompt.",
	}

	data, err := Generate(s, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := string(data)
	if strings.Contains(out, "## Tools") {
		t.Error("output should not contain ## Tools when no tools")
	}
	if !strings.Contains(out, "Just a system prompt.") {
		t.Error("output should contain body")
	}
}

func TestGenerate_Nil(t *testing.T) {
	_, err := Generate(nil, nil)
	if err == nil {
		t.Fatal("expected error for nil skill")
	}
}

func TestGenerate_OmitsEmptyFields(t *testing.T) {
	s := &SkillMD{
		Name: "minimal",
		Body: "Hello",
	}

	data, err := Generate(s, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := string(data)
	if strings.Contains(out, "license:") {
		t.Error("empty license should be omitted")
	}
	if strings.Contains(out, "compatibility:") {
		t.Error("empty compatibility should be omitted")
	}
	if strings.Contains(out, "metadata:") {
		t.Error("empty metadata should be omitted")
	}
}

func TestGenerate_Roundtrip(t *testing.T) {
	s := &SkillMD{
		Name:        "roundtrip-skill",
		Description: "Test roundtrip",
		Body:        "System prompt content.\n",
	}
	data, err := Generate(s, nil)
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	parsed, parsedTools, err := ParseWithTools(data)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if parsed.Name != s.Name {
		t.Errorf("roundtrip name = %q, want %q", parsed.Name, s.Name)
	}
	if parsed.Description != s.Description {
		t.Errorf("roundtrip description = %q, want %q", parsed.Description, s.Description)
	}
	if strings.TrimSpace(parsed.Body) != strings.TrimSpace(s.Body) {
		t.Errorf("roundtrip body = %q, want %q", parsed.Body, s.Body)
	}
	if len(parsedTools) != 0 {
		t.Fatalf("roundtrip tools count = %d, want 0", len(parsedTools))
	}
}

func TestGenerate_RoundtripProvenance(t *testing.T) {
	s := &SkillMD{
		Name:        "provenance-skill",
		Description: "Carries attribution",
		Category:    "Utilities",
		Tags:        []string{"a", "b"},
		Version:     "1.2.3",
		Author:      "Jane Doe",
		License:     "MIT",
		Body:        "Prompt.\n",
	}

	data, err := Generate(s, nil)
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	parsed, err := Parse(data)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if parsed.Version != "1.2.3" {
		t.Errorf("roundtrip version = %q, want %q", parsed.Version, "1.2.3")
	}
	if parsed.Author != "Jane Doe" {
		t.Errorf("roundtrip author = %q, want %q", parsed.Author, "Jane Doe")
	}
	if parsed.License != "MIT" {
		t.Errorf("roundtrip license = %q, want %q", parsed.License, "MIT")
	}
	if parsed.Category != "Utilities" {
		t.Errorf("roundtrip category = %q, want %q", parsed.Category, "Utilities")
	}
	if len(parsed.Tags) != 2 {
		t.Errorf("roundtrip tags = %v, want 2 entries", parsed.Tags)
	}
}
