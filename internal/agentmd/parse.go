package agentmd

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// AgentMD represents a parsed agent .md file (YAML frontmatter + system prompt body).
type AgentMD struct {
	Name                      string   `yaml:"name"`
	Description               string   `yaml:"description,omitempty"`
	Provider                  string   `yaml:"provider"`
	Model                     string   `yaml:"model,omitempty"`
	Skills                    []string `yaml:"skills,omitempty"`
	MCPSets                   []string `yaml:"mcp_sets,omitempty"`
	MCPs                      []string `yaml:"mcp_urls,omitempty"`
	Workflows                 []string `yaml:"workflows,omitempty"`
	BuiltinTools              []string `yaml:"builtin_tools,omitempty"`
	MaxIterations             int      `yaml:"max_iterations,omitempty"`
	ToolTimeout               int      `yaml:"tool_timeout,omitempty"`
	ConfirmationRequiredTools []string `yaml:"confirmation_required_tools,omitempty"`
	AvatarSeed                string   `yaml:"avatar_seed,omitempty"`
	SystemPrompt              string   // markdown body after frontmatter
}

// Parse splits an agent .md file on --- delimiters, YAML-unmarshals the
// frontmatter into config fields, and keeps the body as the system prompt.
func Parse(data []byte) (*AgentMD, error) {
	// Trim leading whitespace/BOM.
	data = bytes.TrimLeft(data, "\xef\xbb\xbf \t\n\r")

	if !bytes.HasPrefix(data, []byte("---")) {
		// No frontmatter — treat entire content as system prompt.
		return &AgentMD{SystemPrompt: string(data)}, nil
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
			return nil, fmt.Errorf("agentmd: closing --- delimiter not found")
		}
		frontmatter = rest[:idx]
		body = rest[idx+4:] // skip \n---
	}
	// Trim leading newline from body.
	body = bytes.TrimLeft(body, "\r\n")

	var a AgentMD
	if err := yaml.Unmarshal(frontmatter, &a); err != nil {
		return nil, fmt.Errorf("agentmd: parse frontmatter: %w", err)
	}

	a.SystemPrompt = string(body)

	return &a, nil
}
