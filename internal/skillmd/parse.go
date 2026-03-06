package skillmd

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// SkillMD represents a parsed SKILL.md file (YAML frontmatter + markdown body).
type SkillMD struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
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
