package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/rakunlabs/at/internal/service"
)

// MCP resources and server instructions, offered the way OpenCode does:
// two tools (list_mcp_resources, read_mcp_resource) over every connected MCP
// server that declares the resources capability, and each server's
// initialize instructions in the system prompt.
const (
	mcpListResourcesTool = "list_mcp_resources"
	mcpReadResourceTool  = "read_mcp_resource"
	// Bounds what one resource read or instruction block adds to context.
	mcpResourceMaxBytes     = 256 << 10
	mcpInstructionsMaxBytes = 8 << 10
)

var mcpResourceToolDefs = []service.Tool{
	{
		Name:        mcpListResourcesTool,
		Description: "List documents, records and other data exposed by the connected MCP servers as resources (not tools). Returns each resource's server, URI, name, description and type, plus URI templates.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{"type": "string", "description": "Only list this MCP server's resources. Omit to list every server."},
			},
		},
	},
	{
		Name:        mcpReadResourceTool,
		Description: "Read one MCP resource by its URI (from list_mcp_resources, an expanded URI template, or the user). Returns its text content; binary content is described, not inlined.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{"type": "string", "description": "The server field of the listed resource. Omit to try every server."},
				"uri":    map[string]any{"type": "string", "description": "Exact resource URI."},
			},
			"required": []string{"uri"},
		},
	},
}

// mcpFeatureServer is one connected MCP server that may expose resources.
type mcpFeatureServer struct {
	name     string
	features service.MCPServerFeatures
}

// mcpFeatureSet collects the resource-capable servers and instructions of one
// tool surface (an agent run or an MCP set runtime).
type mcpFeatureSet struct {
	mu           sync.Mutex
	servers      []mcpFeatureServer
	prompts      []mcpFeatureServer
	names        map[string]bool
	instructions []string
}

// add records client when it supports server features; label is used when the
// server reports no name. Names are made unique.
func (f *mcpFeatureSet) add(client service.MCPClient, label string) {
	features, ok := client.(service.MCPServerFeatures)
	if !ok {
		return
	}
	name := strings.TrimSpace(features.ServerName())
	if name == "" {
		name = label
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.names == nil {
		f.names = map[string]bool{}
	}
	base, n := name, 2
	for f.names[name] {
		name = fmt.Sprintf("%s-%d", base, n)
		n++
	}
	f.names[name] = true
	if text := strings.TrimSpace(features.Instructions()); text != "" {
		if len(text) > mcpInstructionsMaxBytes {
			text = truncateUTF8(text, mcpInstructionsMaxBytes) + "\n[instructions truncated]"
		}
		f.instructions = append(f.instructions, fmt.Sprintf("<mcp_instructions server=%q>\n%s\n</mcp_instructions>", name, text))
	}
	caps := features.Capabilities()
	if caps.Resources {
		f.servers = append(f.servers, mcpFeatureServer{name: name, features: features})
	}
	if caps.Prompts {
		f.prompts = append(f.prompts, mcpFeatureServer{name: name, features: features})
	}
}

// mcpPromptInfo is one prompt offered as a slash command: /server:name.
type mcpPromptInfo struct {
	Server      string                      `json:"server"`
	Name        string                      `json:"name"`
	Title       string                      `json:"title,omitempty"`
	Description string                      `json:"description,omitempty"`
	Arguments   []service.MCPPromptArgument `json:"arguments,omitempty"`
}

// Prompts lists the prompts of every prompt-capable server. A failing server
// is skipped so one broken upstream does not hide the rest.
func (f *mcpFeatureSet) Prompts(ctx context.Context) []mcpPromptInfo {
	f.mu.Lock()
	servers := append([]mcpFeatureServer(nil), f.prompts...)
	f.mu.Unlock()
	var out []mcpPromptInfo
	for _, s := range servers {
		prompts, err := s.features.ListPrompts(ctx)
		if err != nil {
			continue
		}
		for _, p := range prompts {
			out = append(out, mcpPromptInfo{Server: s.name, Name: p.Name, Title: p.Title, Description: p.Description, Arguments: p.Arguments})
		}
	}
	return out
}

// GetPrompt renders one prompt as text.
func (f *mcpFeatureSet) GetPrompt(ctx context.Context, server, name string, args map[string]string) (string, error) {
	f.mu.Lock()
	var target service.MCPServerFeatures
	for _, s := range f.prompts {
		if s.name == server {
			target = s.features
		}
	}
	f.mu.Unlock()
	if target == nil {
		return "", fmt.Errorf("MCP server %q has no prompts", server)
	}
	_, messages, err := target.GetPrompt(ctx, name, args)
	if err != nil {
		return "", err
	}
	return service.MCPPromptText(messages), nil
}

// Tools returns the resource tools when any server exposes resources.
func (f *mcpFeatureSet) Tools() []service.Tool {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.servers) == 0 {
		return nil
	}
	return append([]service.Tool(nil), mcpResourceToolDefs...)
}

// Owns reports whether name is one of the resource tools this set serves.
func (f *mcpFeatureSet) Owns(name string) bool {
	if name != mcpListResourcesTool && name != mcpReadResourceTool {
		return false
	}
	return len(f.Tools()) > 0
}

// Prompt is the system-prompt section carrying server instructions.
func (f *mcpFeatureSet) Prompt() string {
	if f == nil {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.instructions) == 0 {
		return ""
	}
	return "## MCP server instructions\nThe connected MCP servers provided these usage instructions for their tools.\n\n" + strings.Join(f.instructions, "\n\n")
}

func (f *mcpFeatureSet) selected(server string) ([]mcpFeatureServer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if server == "" {
		return append([]mcpFeatureServer(nil), f.servers...), nil
	}
	for _, s := range f.servers {
		if s.name == server {
			return []mcpFeatureServer{s}, nil
		}
	}
	names := make([]string, 0, len(f.servers))
	for _, s := range f.servers {
		names = append(names, s.name)
	}
	return nil, fmt.Errorf("unknown MCP server %q; servers with resources: %s", server, strings.Join(names, ", "))
}

// Call runs list_mcp_resources or read_mcp_resource.
func (f *mcpFeatureSet) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	server, _ := args["server"].(string)
	servers, err := f.selected(strings.TrimSpace(server))
	if err != nil {
		return "", err
	}
	switch name {
	case mcpListResourcesTool:
		return listMCPResources(ctx, servers)
	case mcpReadResourceTool:
		uri, _ := args["uri"].(string)
		if strings.TrimSpace(uri) == "" {
			return "", fmt.Errorf("uri is required")
		}
		return readMCPResource(ctx, servers, strings.TrimSpace(uri))
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func listMCPResources(ctx context.Context, servers []mcpFeatureServer) (string, error) {
	type resource struct {
		Server string `json:"server"`
		service.MCPResource
	}
	type template struct {
		Server string `json:"server"`
		service.MCPResourceTemplate
	}
	out := struct {
		Resources []resource `json:"resources"`
		Templates []template `json:"templates,omitempty"`
		Errors    []string   `json:"errors,omitempty"`
	}{Resources: []resource{}}
	for _, s := range servers {
		resources, templates, err := s.features.ListResources(ctx)
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s: %v", s.name, err))
			continue
		}
		for _, r := range resources {
			out.Resources = append(out.Resources, resource{Server: s.name, MCPResource: r})
		}
		for _, t := range templates {
			out.Templates = append(out.Templates, template{Server: s.name, MCPResourceTemplate: t})
		}
	}
	if len(out.Resources) == 0 && len(out.Templates) == 0 && len(out.Errors) > 0 {
		return "", fmt.Errorf("list MCP resources: %s", strings.Join(out.Errors, "; "))
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func readMCPResource(ctx context.Context, servers []mcpFeatureServer, uri string) (string, error) {
	var errs []string
	for _, s := range servers {
		contents, err := s.features.ReadResource(ctx, uri)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", s.name, err))
			continue
		}
		return mcpResourceText(contents), nil
	}
	if len(errs) == 0 {
		return "", fmt.Errorf("no MCP server exposes resources")
	}
	return "", fmt.Errorf("read MCP resource %q: %s", uri, strings.Join(errs, "; "))
}

func mcpResourceText(contents []service.MCPResourceContents) string {
	var b strings.Builder
	for i, c := range contents {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if len(contents) > 1 || c.URI != "" {
			fmt.Fprintf(&b, "--- %s", c.URI)
			if c.MimeType != "" {
				fmt.Fprintf(&b, " (%s)", c.MimeType)
			}
			b.WriteString(" ---\n")
		}
		switch {
		case c.Text != "":
			b.WriteString(c.Text)
		case c.Blob != "":
			fmt.Fprintf(&b, "[binary content, %s, about %d bytes; not inlined]", firstNonEmpty(c.MimeType, "unknown type"), len(c.Blob)*3/4)
		default:
			b.WriteString("[empty]")
		}
		if b.Len() > mcpResourceMaxBytes {
			return truncateUTF8(b.String(), mcpResourceMaxBytes) + fmt.Sprintf("\n\n[resource content truncated at %d KiB]", mcpResourceMaxBytes>>10)
		}
	}
	return b.String()
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}
