package skillmd

import (
	"bytes"
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
// the Markdown body. Executable tool definitions are deliberately rejected;
// callers migrating legacy records must first preserve them as inert Markdown.
func Generate(s *SkillMD, tools []ToolDef) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("skillmd: nil skill")
	}
	if len(tools) > 0 {
		return nil, fmt.Errorf("skillmd: executable tool definitions are not supported")
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
		// Keep generated files POSIX-friendly.
		if len(s.Body) > 0 && s.Body[len(s.Body)-1] != '\n' {
			buf.WriteString("\n")
		}
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
		Version:       s.Version,
		Author:        s.Author,
		License:       s.License,
		Compatibility: s.Compatibility,
		Metadata:      s.Metadata,
		Context:       s.Context,
		Agent:         s.Agent,
	}
}

type skillFrontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description,omitempty"`
	Category      string            `yaml:"category,omitempty"`
	Tags          []string          `yaml:"tags,omitempty"`
	Version       string            `yaml:"version,omitempty"`
	Author        string            `yaml:"author,omitempty"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`
	Context       string            `yaml:"context,omitempty"`
	Agent         string            `yaml:"agent,omitempty"`
}
