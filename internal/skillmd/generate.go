package skillmd

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ToolDef matches the service.Tool struct for portable serialization.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
	Handler     string         `json:"handler,omitempty"`
	HandlerType string         `json:"handler_type,omitempty"`
}

// Generate produces the markdown representation of a skill:
// YAML frontmatter (metadata) separated by --- delimiters, followed by
// the system prompt body. If tools are provided, they are appended as a
// ## Tools section with a JSON code block at the end of the body.
func Generate(s *SkillMD, tools []ToolDef) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("skillmd: nil skill")
	}

	yamlBytes, err := yaml.Marshal(s.frontmatterOnly())
	if err != nil {
		return nil, fmt.Errorf("skillmd: marshal frontmatter: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(yamlBytes)
	buf.WriteString("---\n")

	if s.Body != "" {
		buf.WriteString("\n")
		buf.WriteString(s.Body)
		// Ensure trailing newline before tools section.
		if len(s.Body) > 0 && s.Body[len(s.Body)-1] != '\n' {
			buf.WriteString("\n")
		}
	}

	if len(tools) > 0 {
		toolsJSON, err := json.MarshalIndent(tools, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("skillmd: marshal tools: %w", err)
		}

		if s.Body != "" {
			buf.WriteString("\n")
		}
		buf.WriteString("## Tools\n\n")
		buf.WriteString("```json\n")
		buf.Write(toolsJSON)
		buf.WriteString("\n```\n")
	}

	return buf.Bytes(), nil
}

// frontmatterOnly returns a YAML-serializable struct that excludes the Body field.
func (s *SkillMD) frontmatterOnly() *skillFrontmatter {
	return &skillFrontmatter{
		Name:          s.Name,
		Description:   s.Description,
		Category:      s.Category,
		Tags:          s.Tags,
		License:       s.License,
		Compatibility: s.Compatibility,
		Metadata:      s.Metadata,
	}
}

type skillFrontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description,omitempty"`
	Category      string            `yaml:"category,omitempty"`
	Tags          []string          `yaml:"tags,omitempty"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
}
