package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

const LegacySkillToolsHeading = "## Legacy tool references"

// NormalizeDocumentationSkill preserves legacy executable tool definitions as
// Markdown reference material, then clears them. Skills are documentation: no
// runtime may derive executable capabilities from their contents.
func NormalizeDocumentationSkill(skill *Skill) error {
	if skill == nil || len(skill.Tools) == 0 {
		return nil
	}
	var b strings.Builder
	if strings.TrimSpace(skill.SystemPrompt) != "" {
		b.WriteString(strings.TrimRight(skill.SystemPrompt, "\n"))
		b.WriteString("\n\n")
	}
	b.WriteString(LegacySkillToolsHeading)
	b.WriteString("\n\nThese legacy definitions are documentation only. They are not registered or executed as tools. Use capabilities already attached to the agent, such as built-in or MCP tools.\n")
	for _, tool := range skill.Tools {
		fmt.Fprintf(&b, "\n### `%s`\n\n", tool.Name)
		if tool.Description != "" {
			b.WriteString(tool.Description)
			b.WriteString("\n")
		}
		if tool.InputSchema != nil {
			schema, err := json.MarshalIndent(tool.InputSchema, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal legacy skill tool %q schema: %w", tool.Name, err)
			}
			b.WriteString("\nInputs:\n\n```json\n")
			b.Write(schema)
			b.WriteString("\n```\n")
		}
		if tool.Handler != "" {
			language := "javascript"
			if tool.HandlerType == "bash" {
				language = "bash"
			}
			b.WriteString("\nReference implementation:\n\n```")
			b.WriteString(language)
			b.WriteString("\n")
			b.WriteString(tool.Handler)
			if !strings.HasSuffix(tool.Handler, "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n")
		}
	}
	skill.SystemPrompt = strings.TrimSpace(b.String())
	skill.Tools = nil
	return nil
}
