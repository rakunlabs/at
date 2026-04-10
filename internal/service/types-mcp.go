package service

import (
	"context"

	"github.com/rakunlabs/query"
)

// ─── General MCP Servers ───

// MCPHTTPTool defines a custom HTTP-based tool exposed via an MCP server.
type MCPHTTPTool struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers,omitempty"`
	BodyTemplate string            `json:"body_template,omitempty"`
	InputSchema  map[string]any    `json:"input_schema"`
}

// MCPServerConfig holds the configuration for a general MCP server endpoint.
type MCPServerConfig struct {
	Description string `json:"description,omitempty"`

	// RAG tool integration (optional).
	EnabledRAGTools   []string `json:"enabled_rag_tools,omitempty"`
	CollectionIDs     []string `json:"collection_ids,omitempty"`
	FetchMode         string   `json:"fetch_mode,omitempty"`
	GitCacheDir       string   `json:"git_cache_dir,omitempty"`
	DefaultNumResults int      `json:"default_num_results,omitempty"`
	TokenVariable     string   `json:"token_variable,omitempty"`
	TokenUser         string   `json:"token_user,omitempty"`
	SSHKeyVariable    string   `json:"ssh_key_variable,omitempty"`

	// Custom HTTP tools.
	HTTPTools []MCPHTTPTool `json:"http_tools,omitempty"`

	// Upstream MCP servers to proxy tools from.
	MCPUpstreams []MCPUpstream `json:"mcp_upstreams,omitempty"`

	// Skill tools — names of skills whose tools should be exposed.
	EnabledSkills []string `json:"enabled_skills,omitempty"`

	// Builtin tools — names of server-side builtin tools to expose.
	EnabledBuiltinTools []string `json:"enabled_builtin_tools,omitempty"`

	// Workflow tools — IDs of workflows to expose as individual named tools.
	WorkflowIDs []string `json:"workflow_ids,omitempty"`
}

// MCPUpstream represents an upstream MCP server — either HTTP or stdio (local command).
type MCPUpstream struct {
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// MCPServer represents a named, gateway-facing MCP endpoint.
type MCPServer struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Config      MCPServerConfig `json:"config"`
	Servers     []string        `json:"servers,omitempty"`
	URLs        []string        `json:"urls,omitempty"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	CreatedBy   string          `json:"created_by"`
	UpdatedBy   string          `json:"updated_by"`
}

// MCPServerStorer defines CRUD operations for general MCP server configurations.
type MCPServerStorer interface {
	ListMCPServers(ctx context.Context, q *query.Query) (*ListResult[MCPServer], error)
	GetMCPServer(ctx context.Context, id string) (*MCPServer, error)
	GetMCPServerByName(ctx context.Context, name string) (*MCPServer, error)
	CreateMCPServer(ctx context.Context, s MCPServer) (*MCPServer, error)
	UpdateMCPServer(ctx context.Context, id string, s MCPServer) (*MCPServer, error)
	DeleteMCPServer(ctx context.Context, id string) error
}

// ─── MCP Sets (Internal MCPs) ───

// MCPSet represents an internal MCP configuration that agents use.
type MCPSet struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	Config      MCPServerConfig `json:"config"`
	Servers     []string        `json:"servers"`
	URLs        []string        `json:"urls"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	CreatedBy   string          `json:"created_by"`
	UpdatedBy   string          `json:"updated_by"`
}

// MCPSetStorer defines CRUD operations for MCP set configurations.
type MCPSetStorer interface {
	ListMCPSets(ctx context.Context, q *query.Query) (*ListResult[MCPSet], error)
	GetMCPSet(ctx context.Context, id string) (*MCPSet, error)
	GetMCPSetByName(ctx context.Context, name string) (*MCPSet, error)
	CreateMCPSet(ctx context.Context, s MCPSet) (*MCPSet, error)
	UpdateMCPSet(ctx context.Context, id string, s MCPSet) (*MCPSet, error)
	DeleteMCPSet(ctx context.Context, id string) error
}
