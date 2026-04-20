package agentmd

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Generate produces the markdown representation of an agent:
// YAML frontmatter (config) separated by --- delimiters, followed by
// the system prompt as the markdown body.
func Generate(a *AgentMD) ([]byte, error) {
	if a == nil {
		return nil, fmt.Errorf("agentmd: nil agent")
	}

	// Build a frontmatter-only struct to avoid serializing SystemPrompt into YAML.
	type frontmatter struct {
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
	}

	fm := frontmatter{
		Name:                      a.Name,
		Description:               a.Description,
		Provider:                  a.Provider,
		Model:                     a.Model,
		Skills:                    a.Skills,
		MCPSets:                   a.MCPSets,
		MCPs:                      a.MCPs,
		Workflows:                 a.Workflows,
		BuiltinTools:              a.BuiltinTools,
		MaxIterations:             a.MaxIterations,
		ToolTimeout:               a.ToolTimeout,
		ConfirmationRequiredTools: a.ConfirmationRequiredTools,
		AvatarSeed:                a.AvatarSeed,
	}

	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("agentmd: marshal frontmatter: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(yamlBytes)
	buf.WriteString("---\n")

	if a.SystemPrompt != "" {
		buf.WriteString("\n")
		buf.WriteString(a.SystemPrompt)
		// Ensure trailing newline.
		if len(a.SystemPrompt) > 0 && a.SystemPrompt[len(a.SystemPrompt)-1] != '\n' {
			buf.WriteString("\n")
		}
	}

	return buf.Bytes(), nil
}
