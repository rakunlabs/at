package agentmd

import (
	"strings"
	"testing"
)

func TestGenerate_FullAgent(t *testing.T) {
	a := &AgentMD{
		Name:          "my-researcher",
		Description:   "Research agent",
		Provider:      "openai",
		Model:         "gpt-4o",
		Skills:        []string{"web-scraper", "code-reviewer"},
		MCPSets:       []string{"my-toolbox"},
		BuiltinTools:  []string{"web_search"},
		MaxIterations: 10,
		ToolTimeout:   30,
		AvatarSeed:    "researcher",
		SystemPrompt:  "You are a research agent.\n\n## Instructions\n1. Verify sources\n",
	}

	data, err := Generate(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := string(data)

	if !strings.HasPrefix(s, "---\n") {
		t.Error("output should start with ---")
	}
	if !strings.Contains(s, "name: my-researcher") {
		t.Error("output should contain name")
	}
	if !strings.Contains(s, "provider: openai") {
		t.Error("output should contain provider")
	}
	if !strings.Contains(s, "model: gpt-4o") {
		t.Error("output should contain model")
	}
	if !strings.Contains(s, "- web-scraper") {
		t.Error("output should contain skill web-scraper")
	}
	if !strings.Contains(s, "- my-toolbox") {
		t.Error("output should contain mcp_set my-toolbox")
	}
	if !strings.Contains(s, "You are a research agent.") {
		t.Error("output should contain system prompt")
	}

	// Verify roundtrip.
	parsed, err := Parse(data)
	if err != nil {
		t.Fatalf("roundtrip parse failed: %v", err)
	}
	if parsed.Name != a.Name {
		t.Errorf("roundtrip name = %q, want %q", parsed.Name, a.Name)
	}
	if parsed.Provider != a.Provider {
		t.Errorf("roundtrip provider = %q, want %q", parsed.Provider, a.Provider)
	}
	if len(parsed.Skills) != len(a.Skills) {
		t.Errorf("roundtrip skills count = %d, want %d", len(parsed.Skills), len(a.Skills))
	}
	if parsed.SystemPrompt != a.SystemPrompt {
		t.Errorf("roundtrip system prompt = %q, want %q", parsed.SystemPrompt, a.SystemPrompt)
	}
}

func TestGenerate_EmptyPrompt(t *testing.T) {
	a := &AgentMD{
		Name:     "minimal",
		Provider: "anthropic",
	}

	data, err := Generate(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		t.Error("output should start with ---")
	}
	if !strings.HasSuffix(s, "---\n") {
		t.Error("output without system prompt should end with ---\\n")
	}
	if strings.Contains(s, "system_prompt") {
		t.Error("system prompt field should not appear in YAML frontmatter")
	}
}

func TestGenerate_Nil(t *testing.T) {
	_, err := Generate(nil)
	if err == nil {
		t.Fatal("expected error for nil agent")
	}
}

func TestGenerate_OmitsEmptyFields(t *testing.T) {
	a := &AgentMD{
		Name:         "test",
		Provider:     "openai",
		SystemPrompt: "Hello",
	}

	data, err := Generate(a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s := string(data)
	if strings.Contains(s, "skills:") {
		t.Error("empty skills should be omitted")
	}
	if strings.Contains(s, "mcp_sets:") {
		t.Error("empty mcp_sets should be omitted")
	}
	if strings.Contains(s, "builtin_tools:") {
		t.Error("empty builtin_tools should be omitted")
	}
	if strings.Contains(s, "avatar_seed:") {
		t.Error("empty avatar_seed should be omitted")
	}
}
