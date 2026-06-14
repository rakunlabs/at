package server

import (
	"testing"
)

func TestSkillExportFromSkillMD(t *testing.T) {
	content := `---
name: test-skill
description: A test skill
category: Utilities
tags:
  - x
version: 2.0.0
author: Jane Doe
license: Apache-2.0
---

Do useful things.

## Tools

` + "```json\n" + `[
  {"name": "tool_a", "description": "does a", "inputSchema": {"type": "object"}, "handler": "return 1;", "handler_type": "js"}
]` + "\n```\n"

	export, err := skillExportFromSkillMD([]byte(content))
	if err != nil {
		t.Fatalf("skillExportFromSkillMD: %v", err)
	}

	if export.Name != "test-skill" {
		t.Errorf("name = %q, want test-skill", export.Name)
	}
	if export.Version != "2.0.0" {
		t.Errorf("version = %q, want 2.0.0", export.Version)
	}
	if export.Author != "Jane Doe" {
		t.Errorf("author = %q, want Jane Doe", export.Author)
	}
	if export.License != "Apache-2.0" {
		t.Errorf("license = %q, want Apache-2.0", export.License)
	}
	if export.Category != "Utilities" {
		t.Errorf("category = %q, want Utilities", export.Category)
	}
	if len(export.Tools) != 1 || export.Tools[0].Name != "tool_a" {
		t.Fatalf("tools = %+v, want one tool named tool_a", export.Tools)
	}
	if export.Tools[0].Handler != "return 1;" {
		t.Errorf("tool handler = %q, want return 1;", export.Tools[0].Handler)
	}
	if export.SystemPrompt == "" || export.SystemPrompt != "Do useful things." {
		t.Errorf("system prompt = %q, want body without tools section", export.SystemPrompt)
	}
}

func TestParseSkillPayload_JSON(t *testing.T) {
	payload := []byte(`{
		"name": "json-skill",
		"description": "from json",
		"version": "0.3.1",
		"author": "AT Team",
		"license": "MIT",
		"system_prompt": "prompt",
		"tools": []
	}`)

	export, err := parseSkillPayload("https://example.com/skill.json", payload)
	if err != nil {
		t.Fatalf("parseSkillPayload: %v", err)
	}

	if export.Name != "json-skill" {
		t.Errorf("name = %q, want json-skill", export.Name)
	}
	if export.Version != "0.3.1" || export.Author != "AT Team" || export.License != "MIT" {
		t.Errorf("provenance = %q/%q/%q, want 0.3.1/AT Team/MIT", export.Version, export.Author, export.License)
	}
}

func TestParseSkillPayload_SkillMDNameFromURL(t *testing.T) {
	payload := []byte("---\ndescription: no name in frontmatter\n---\n\nBody.\n")

	export, err := parseSkillPayload("https://example.com/skills/my-skill/SKILL.md", payload)
	if err != nil {
		t.Fatalf("parseSkillPayload: %v", err)
	}
	if export.Name != "my-skill" {
		t.Errorf("name = %q, want my-skill (derived from URL)", export.Name)
	}
}

func TestNegotiateMCPProtocolVersion(t *testing.T) {
	tests := []struct {
		name   string
		params any
		want   string
	}{
		{"supported latest", map[string]any{"protocolVersion": "2025-06-18"}, "2025-06-18"},
		{"supported streamable", map[string]any{"protocolVersion": "2025-03-26"}, "2025-03-26"},
		{"baseline", map[string]any{"protocolVersion": "2024-11-05"}, "2024-11-05"},
		{"unknown future version", map[string]any{"protocolVersion": "2099-01-01"}, "2024-11-05"},
		{"missing params", nil, "2024-11-05"},
		{"wrong type", "nope", "2024-11-05"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := negotiateMCPProtocolVersion(tt.params); got != tt.want {
				t.Errorf("negotiateMCPProtocolVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
