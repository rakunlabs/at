package skillmd

import (
	"testing"
)

func TestParse_ValidSkillMD(t *testing.T) {
	input := `---
name: web_search
description: Search the web for information
license: MIT
compatibility: claude
metadata:
  author: test
  version: "1.0"
---
# Web Search Skill

Use this skill to search the web.

## Instructions
1. Parse the query
2. Return results
`
	s, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.Name != "web_search" {
		t.Errorf("name = %q, want %q", s.Name, "web_search")
	}
	if s.Description != "Search the web for information" {
		t.Errorf("description = %q, want %q", s.Description, "Search the web for information")
	}
	if s.License != "MIT" {
		t.Errorf("license = %q, want %q", s.License, "MIT")
	}
	if s.Compatibility != "claude" {
		t.Errorf("compatibility = %q, want %q", s.Compatibility, "claude")
	}
	if s.Metadata["author"] != "test" {
		t.Errorf("metadata[author] = %q, want %q", s.Metadata["author"], "test")
	}
	if s.Body == "" {
		t.Error("body should not be empty")
	}
	if s.Body[:len("# Web Search Skill")] != "# Web Search Skill" {
		t.Errorf("body should start with heading, got %q", s.Body[:40])
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	input := `# Just Markdown

Some content here.`
	s, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.Name != "" {
		t.Errorf("name should be empty, got %q", s.Name)
	}
	if s.Body != input {
		t.Errorf("body should be entire input")
	}
}

func TestParse_MissingClosingDelimiter(t *testing.T) {
	input := `---
name: broken
description: no closing delimiter
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing closing delimiter")
	}
}

func TestParse_EmptyFrontmatter(t *testing.T) {
	input := `---
---
# Body only`
	s, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.Name != "" {
		t.Errorf("name should be empty, got %q", s.Name)
	}
	if s.Body != "# Body only" {
		t.Errorf("body = %q, want %q", s.Body, "# Body only")
	}
}

func TestParse_MinimalFrontmatter(t *testing.T) {
	input := `---
name: minimal
---
Instructions here.`
	s, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.Name != "minimal" {
		t.Errorf("name = %q, want %q", s.Name, "minimal")
	}
	if s.Body != "Instructions here." {
		t.Errorf("body = %q, want %q", s.Body, "Instructions here.")
	}
}
