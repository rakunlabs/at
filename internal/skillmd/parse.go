package skillmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillMD represents a parsed SKILL.md file (YAML frontmatter + markdown body).
type SkillMD struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	Category      string            `yaml:"category"`
	Tags          []string          `yaml:"tags"`
	License       string            `yaml:"license"`
	Compatibility string            `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
	Body          string            // markdown content after frontmatter
}

// Parse splits a SKILL.md file on --- delimiters, YAML-unmarshals the
// frontmatter, and keeps the body as-is.
func Parse(data []byte) (*SkillMD, error) {
	// Trim leading whitespace/BOM.
	data = bytes.TrimLeft(data, "\xef\xbb\xbf \t\n\r")

	if !bytes.HasPrefix(data, []byte("---")) {
		// No frontmatter — treat entire content as body.
		return &SkillMD{Body: string(data)}, nil
	}

	// Find closing --- delimiter.
	rest := data[3:] // skip opening ---
	rest = bytes.TrimLeft(rest, " \t")
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	}

	var frontmatter, body []byte
	if bytes.HasPrefix(rest, []byte("---")) {
		// Empty frontmatter: ---\n---
		frontmatter = nil
		body = rest[3:]
	} else {
		idx := bytes.Index(rest, []byte("\n---"))
		if idx < 0 {
			return nil, fmt.Errorf("skillmd: closing --- delimiter not found")
		}
		frontmatter = rest[:idx]
		body = rest[idx+4:] // skip \n---
	}
	// Trim leading newline from body.
	body = bytes.TrimLeft(body, "\r\n")

	var s SkillMD
	if err := yaml.Unmarshal(frontmatter, &s); err != nil {
		return nil, fmt.Errorf("skillmd: parse frontmatter: %w", err)
	}

	s.Body = string(body)

	return &s, nil
}

// ParseWithTools parses a SKILL.md and additionally extracts tools from a
// ## Tools section in the body. The tools section must contain a JSON code
// block with an array of tool definitions. The body returned in SkillMD.Body
// has the ## Tools section stripped so it only contains the system prompt.
func ParseWithTools(data []byte) (*SkillMD, []ToolDef, error) {
	s, err := Parse(data)
	if err != nil {
		return nil, nil, err
	}

	tools, cleanBody := extractToolsSection(s.Body)
	s.Body = cleanBody

	return s, tools, nil
}

// extractToolsSection finds a ## Tools heading in the body, extracts the JSON
// code block beneath it, and returns the parsed tools plus the body with the
// tools section removed.
func extractToolsSection(body string) ([]ToolDef, string) {
	// Find the ## Tools heading (case-insensitive match on "tools").
	toolsIdx := -1
	for _, marker := range []string{"## Tools\n", "## Tools\r\n"} {
		idx := strings.Index(body, marker)
		if idx >= 0 {
			toolsIdx = idx
			break
		}
	}

	if toolsIdx < 0 {
		return nil, body
	}

	// Everything before ## Tools is the system prompt.
	before := strings.TrimRight(body[:toolsIdx], "\n\r ")
	after := body[toolsIdx:]

	// Find JSON code block within the tools section.
	codeStart := strings.Index(after, "```json\n")
	if codeStart < 0 {
		codeStart = strings.Index(after, "```json\r\n")
	}
	if codeStart < 0 {
		// No code block found — return body as-is.
		return nil, body
	}

	jsonStart := strings.Index(after[codeStart:], "\n")
	if jsonStart < 0 {
		return nil, body
	}
	jsonStart += codeStart + 1 // skip past the ```json\n line

	codeEnd := strings.Index(after[jsonStart:], "\n```")
	if codeEnd < 0 {
		return nil, body
	}

	jsonBlock := after[jsonStart : jsonStart+codeEnd]

	var tools []ToolDef
	if err := json.Unmarshal([]byte(jsonBlock), &tools); err != nil {
		// If JSON is invalid, return body unchanged.
		return nil, body
	}

	return tools, before
}
