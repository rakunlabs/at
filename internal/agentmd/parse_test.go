package agentmd

import (
	"testing"
)

func TestParse_FullAgent(t *testing.T) {
	input := `---
name: my-researcher
description: Research agent for web tasks
provider: openai
model: gpt-4o
skills:
  - web-scraper
  - code-reviewer
mcp_sets:
  - my-toolbox
builtin_tools:
  - web_search
max_iterations: 10
tool_timeout: 30
avatar_seed: researcher
confirmation_required_tools:
  - delete_file
---

You are a research agent that specializes in finding and synthesizing information from the web.

## Instructions
1. Always verify sources
2. Provide citations
`

	a, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.Name != "my-researcher" {
		t.Errorf("name = %q, want %q", a.Name, "my-researcher")
	}
	if a.Description != "Research agent for web tasks" {
		t.Errorf("description = %q, want %q", a.Description, "Research agent for web tasks")
	}
	if a.Provider != "openai" {
		t.Errorf("provider = %q, want %q", a.Provider, "openai")
	}
	if a.Model != "gpt-4o" {
		t.Errorf("model = %q, want %q", a.Model, "gpt-4o")
	}
	if len(a.Skills) != 2 || a.Skills[0] != "web-scraper" {
		t.Errorf("skills = %v, want [web-scraper code-reviewer]", a.Skills)
	}
	if len(a.MCPSets) != 1 || a.MCPSets[0] != "my-toolbox" {
		t.Errorf("mcp_sets = %v, want [my-toolbox]", a.MCPSets)
	}
	if len(a.BuiltinTools) != 1 || a.BuiltinTools[0] != "web_search" {
		t.Errorf("builtin_tools = %v, want [web_search]", a.BuiltinTools)
	}
	if a.MaxIterations != 10 {
		t.Errorf("max_iterations = %d, want 10", a.MaxIterations)
	}
	if a.ToolTimeout != 30 {
		t.Errorf("tool_timeout = %d, want 30", a.ToolTimeout)
	}
	if a.AvatarSeed != "researcher" {
		t.Errorf("avatar_seed = %q, want %q", a.AvatarSeed, "researcher")
	}
	if len(a.ConfirmationRequiredTools) != 1 || a.ConfirmationRequiredTools[0] != "delete_file" {
		t.Errorf("confirmation_required_tools = %v, want [delete_file]", a.ConfirmationRequiredTools)
	}
	if a.SystemPrompt == "" {
		t.Error("system prompt should not be empty")
	}
	want := "You are a research agent"
	if len(a.SystemPrompt) < len(want) || a.SystemPrompt[:len(want)] != want {
		t.Errorf("system prompt should start with %q, got %q", want, a.SystemPrompt[:40])
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	input := `You are a helpful assistant.`
	a, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.Name != "" {
		t.Errorf("name should be empty, got %q", a.Name)
	}
	if a.SystemPrompt != input {
		t.Errorf("system prompt should be entire input")
	}
}

func TestParse_MissingClosingDelimiter(t *testing.T) {
	input := `---
name: broken
provider: openai
`
	_, err := Parse([]byte(input))
	if err == nil {
		t.Fatal("expected error for missing closing delimiter")
	}
}

func TestParse_EmptyFrontmatter(t *testing.T) {
	input := `---
---
You are an agent.`
	a, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.Name != "" {
		t.Errorf("name should be empty, got %q", a.Name)
	}
	if a.SystemPrompt != "You are an agent." {
		t.Errorf("system prompt = %q, want %q", a.SystemPrompt, "You are an agent.")
	}
}

func TestParse_MinimalFrontmatter(t *testing.T) {
	input := `---
name: simple-agent
provider: anthropic
---
Do your best.`
	a, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a.Name != "simple-agent" {
		t.Errorf("name = %q, want %q", a.Name, "simple-agent")
	}
	if a.Provider != "anthropic" {
		t.Errorf("provider = %q, want %q", a.Provider, "anthropic")
	}
	if a.SystemPrompt != "Do your best." {
		t.Errorf("system prompt = %q, want %q", a.SystemPrompt, "Do your best.")
	}
}
