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

// MCPInlineTool is executable tool configuration owned by an MCP set rather
// than by a documentation skill. SourceSkillID is provenance used by the
// execution policy; it does not make the skill itself executable.
type MCPInlineTool struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	InputSchema   map[string]any `json:"inputSchema"`
	Handler       string         `json:"handler"`
	HandlerType   string         `json:"handler_type,omitempty"`
	SourceSkillID string         `json:"source_skill_id,omitempty"`
}

// MCPServerConfig holds the configuration for a general MCP server endpoint.
type MCPServerConfig struct {
	Description string `json:"description,omitempty"`

	// Custom HTTP tools.
	HTTPTools []MCPHTTPTool `json:"http_tools,omitempty"`

	// InlineTools are legacy JS/bash handlers migrated out of skills. They run
	// through MCP-set admission and the normal handler execution policy.
	InlineTools []MCPInlineTool `json:"inline_tools,omitempty"`

	// Upstream MCP servers to proxy tools from.
	MCPUpstreams []MCPUpstream `json:"mcp_upstreams,omitempty"`

	// Deprecated: retained only to decode existing records. MCP runtimes ignore it.
	EnabledSkills []string `json:"enabled_skills,omitempty"`

	// Builtin tools — names of server-side builtin tools to expose.
	EnabledBuiltinTools []string `json:"enabled_builtin_tools,omitempty"`

	// Workflow tools — IDs of workflows to expose as individual named tools.
	WorkflowIDs []string `json:"workflow_ids,omitempty"`

	// ImageGeneration configures the generate_image built-in on this endpoint.
	ImageGeneration *ImageGenerationConfig `json:"image_generation,omitempty"`

	// Raw WebSocket passthrough (optional). When set, the gateway exposes
	// GET /gateway/v1/mcp/{name}/ws and transparently proxies WebSocket
	// frames to the upstream URL. Useful when an installed MCP program also
	// serves a non-MCP WebSocket (event stream, control channel, …) that
	// external clients should reach through AT's auth layer.
	WSUpstream *WSUpstream `json:"ws_upstream,omitempty"`
}

// ImageGenerationConfig pins the provider (and optionally the model) the
// generate_image built-in uses, so the calling model does not have to guess a
// provider key. Pinned fields are removed from the tool schema; Size, Quality
// and Background are defaults the caller may still override.
type ImageGenerationConfig struct {
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Size       string `json:"size,omitempty"`
	Quality    string `json:"quality,omitempty"`
	Background string `json:"background,omitempty"`
}

// WSUpstream configures raw WebSocket passthrough for a gateway MCP endpoint.
type WSUpstream struct {
	URL     string            `json:"url"`               // ws:// or wss:// (http/https also accepted)
	Headers map[string]string `json:"headers,omitempty"` // injected on dial; values support {{var:key}} references

	// PassQueryParams optionally limits which client query parameters are
	// forwarded raw to the upstream. When empty, all client query parameters
	// except AT's auth token are forwarded for backward compatibility.
	PassQueryParams []string `json:"pass_query_params,omitempty"`

	// PassHeaders names client request headers that should be explicitly copied
	// to the upstream. Authorization and Cookie are never forwarded from the
	// client; configure upstream auth via Headers instead.
	PassHeaders []string `json:"pass_headers,omitempty"`
}

// MCPUpstream represents an upstream MCP server — either HTTP or stdio (local command).
type MCPUpstream struct {
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// Auth makes an HTTP upstream authenticate with OAuth 2.1 (the MCP
	// authorization spec). Nil keeps the static Headers behaviour.
	Auth *MCPUpstreamAuth `json:"auth,omitempty"`
}

// MCP upstream account sources, tried in the order the upstream lists them.
const (
	// MCPAccountUser uses the personal connection of the account the run
	// executes as (for a personal gateway API token, the token's owner).
	MCPAccountUser = "user"
	// MCPAccountAgent uses the connection the running agent binds for this
	// upstream's provider (AgentConfig.Connections / SkillRef.Connections).
	MCPAccountAgent = "agent"
	// MCPAccountShared uses SharedConnectionID, one workspace connection.
	MCPAccountShared = "shared"
)

// MCPUpstreamAuth describes how an HTTP upstream obtains its access token.
//
// The upstream's identity for credentials is Provider: every connection that
// authorizes this server is stored with Connection.Provider == Provider, so
// one person's GitHub MCP authorization is found by every set that points at
// the same server, and an agent binds it the same way it binds a skill
// connection.
type MCPUpstreamAuth struct {
	Type string `json:"type"` // "oauth2"

	// Provider is the connection provider key, e.g. "mcp-github". Derived
	// from the upstream host when empty.
	Provider string `json:"provider,omitempty"`

	// Accounts is the ordered list of account sources (user, agent, shared).
	// The first source that yields a usable connection wins; a source that is
	// not listed is never consulted, so a shared account is never used unless
	// it is named here. Empty means ["user"].
	Accounts []string `json:"accounts,omitempty"`

	// SharedConnectionID names the workspace connection used by the shared
	// source.
	SharedConnectionID string `json:"shared_connection_id,omitempty"`

	// Scopes requested at authorization. Empty uses the scopes the server
	// advertises in its protected resource metadata.
	Scopes []string `json:"scopes,omitempty"`

	// ClientID is a pre-registered OAuth client. Leave empty for servers that
	// support dynamic client registration (RFC 7591). A confidential client's
	// secret belongs in encrypted connection credentials, never the MCP config.
	ClientID string `json:"client_id,omitempty"`

	// AuthorizationServer overrides discovery (an issuer URL). Needed only
	// for servers that publish no protected resource metadata.
	AuthorizationServer string `json:"authorization_server,omitempty"`
}

// MCPServer represents a named, gateway-facing MCP endpoint.
type MCPServer struct {
	WorkspaceID string          `json:"workspace_id"`
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Public      bool            `json:"public"`
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
	WorkspaceID string          `json:"workspace_id"`
	OwnerUserID string          `json:"owner_user_id,omitempty"`
	Scope       string          `json:"scope"`
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

type MCPSetPublisher interface {
	PublishMCPSetToWorkspace(ctx context.Context, id, by string) (*MCPSet, error)
}
