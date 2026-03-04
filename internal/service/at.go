package service

import (
	"context"
	"net/http"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// ListMeta contains pagination metadata.
type ListMeta struct {
	Total  uint64 `json:"total,omitempty"`
	Offset uint64 `json:"offset,omitempty"`
	Limit  uint64 `json:"limit,omitempty"`
}

// ListResult is a generic paginated response.
type ListResult[T any] struct {
	Data []T      `json:"data"`
	Meta ListMeta `json:"meta"`
}

// Generic LLM Interface
type LLMProvider interface {
	// Chat sends messages to the LLM and returns a response.
	// The model parameter allows per-request model override;
	// if empty, the provider's default model is used.
	Chat(ctx context.Context, model string, messages []Message, tools []Tool) (*LLMResponse, error)
}

// LLMStreamProvider is optionally implemented by providers that support
// true server-sent event (SSE) streaming. The gateway checks for this
// interface via type assertion; if a provider doesn't implement it,
// the gateway falls back to calling Chat() and fake-streaming the result.
type LLMStreamProvider interface {
	ChatStream(ctx context.Context, model string, messages []Message, tools []Tool) (<-chan StreamChunk, http.Header, error)

	// Proxy forwards a raw HTTP request to the provider's API.
	// The path is relative to the provider's base URL.
	Proxy(w http.ResponseWriter, r *http.Request, path string) error
}

// InlineImage represents a base64-encoded image returned by a provider (e.g. Gemini).
type InlineImage struct {
	MimeType string // e.g. "image/png"
	Data     string // base64-encoded
}

// StreamChunk represents a single chunk in a streaming response.
type StreamChunk struct {
	// Content is the text delta for this chunk (may be empty).
	Content string

	// ReasoningContent is the reasoning/thinking text delta for this chunk.
	// Populated by providers that support thinking tokens (e.g. Gemini 2.5+
	// thinking models, Anthropic extended thinking).
	ReasoningContent string

	// InlineImages contains any base64-encoded images in this chunk (e.g. from Gemini image generation).
	InlineImages []InlineImage

	// ToolCalls contains tool call deltas for this chunk.
	ToolCalls []ToolCall

	// FinishReason is set on the final chunk: "stop" or "tool_calls".
	// Empty string means this is not the final chunk.
	FinishReason string

	// Usage, when non-nil, contains the final token usage statistics for
	// the entire streamed response. Providers set this on the last chunk.
	Usage *Usage

	// Error, if non-nil, indicates the stream encountered an error.
	Error error
}

// ProviderRecord represents a provider configuration stored in the database.
type ProviderRecord struct {
	ID        string           `json:"id"`
	Key       string           `json:"key"`
	Config    config.LLMConfig `json:"config"`
	CreatedAt string           `json:"created_at"`
	UpdatedAt string           `json:"updated_at"`
	CreatedBy string           `json:"created_by"`
	UpdatedBy string           `json:"updated_by"`
}

// ProviderStorer defines CRUD operations for provider configurations
// stored in a persistent backend (e.g., PostgreSQL).
type ProviderStorer interface {
	ListProviders(ctx context.Context, q *query.Query) (*ListResult[ProviderRecord], error)
	GetProvider(ctx context.Context, key string) (*ProviderRecord, error)
	CreateProvider(ctx context.Context, record ProviderRecord) (*ProviderRecord, error)
	UpdateProvider(ctx context.Context, key string, record ProviderRecord) (*ProviderRecord, error)
	DeleteProvider(ctx context.Context, key string) error
}

// KeyRotator is optionally implemented by stores that support encryption
// key rotation for provider credentials. The method decrypts all provider
// configs with the current key, re-encrypts them with newKey, and updates
// the rows atomically within a transaction. Passing nil as newKey disables
// encryption (all values are stored as plaintext).
type KeyRotator interface {
	RotateEncryptionKey(ctx context.Context, newKey []byte) error
}

// EncryptionKeyUpdater is optionally implemented by stores that support
// updating the in-memory encryption key without re-encrypting database rows.
// This is used by peer instances in a cluster when they receive a key rotation
// broadcast from the instance that performed the actual DB rotation.
type EncryptionKeyUpdater interface {
	SetEncryptionKey(newKey []byte)
}

// ─── API Token Management ───

// Restriction mode constants for APIToken allowed_*_mode fields.
// "" (empty) and "all" mean unrestricted, "none" means deny all,
// "list" means only items in the corresponding slice are allowed.
const (
	AccessModeAll  = "all"
	AccessModeNone = "none"
	AccessModeList = "list"
)

// APIToken represents a bearer token stored in the database for gateway auth.
type APIToken struct {
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	TokenPrefix          string                 `json:"token_prefix"`           // first 8 chars for display (e.g. "at_xxxx…")
	AllowedProvidersMode string                 `json:"allowed_providers_mode"` // "all" (default/""), "none", or "list"
	AllowedProviders     types.Slice[string]    `json:"allowed_providers"`      // used when mode = "list"
	AllowedModelsMode    string                 `json:"allowed_models_mode"`    // "all" (default/""), "none", or "list"
	AllowedModels        types.Slice[string]    `json:"allowed_models"`         // used when mode = "list" ("provider/model" format)
	AllowedWebhooksMode  string                 `json:"allowed_webhooks_mode"`  // "all" (default/""), "none", or "list"
	AllowedWebhooks      types.Slice[string]    `json:"allowed_webhooks"`       // used when mode = "list" (trigger IDs or aliases)
	AllowedRAGMCPsMode   string                 `json:"allowed_rag_mcps_mode"`  // "all" (default/""), "none", or "list"
	AllowedRAGMCPs       types.Slice[string]    `json:"allowed_rag_mcps"`       // used when mode = "list" (server names)
	ExpiresAt            types.Null[types.Time] `json:"expires_at"`             // zero value = no expiry
	TotalTokenLimit      types.Null[int64]      `json:"total_token_limit"`      // max total tokens allowed (across all models); nil = unlimited
	LimitResetInterval   types.Null[string]     `json:"limit_reset_interval"`   // "daily", "weekly", "monthly", or nil = manual only
	LastResetAt          types.Null[types.Time] `json:"last_reset_at"`          // last time usage counters were reset
	CreatedAt            types.Time             `json:"created_at"`
	LastUsedAt           types.Null[types.Time] `json:"last_used_at"`
	CreatedBy            string                 `json:"created_by"`
	UpdatedBy            string                 `json:"updated_by"`
}

// ResolveAccessMode returns the effective mode for a restriction field.
// It handles backward compatibility: if mode is empty but the slice has items,
// it returns "list"; otherwise empty is treated as "all".
func ResolveAccessMode(mode string, items []string) string {
	if mode != "" {
		return mode
	}
	// Backward compat: old tokens have no mode but may have a populated list.
	if len(items) > 0 {
		return AccessModeList
	}
	return AccessModeAll
}

// APITokenStorer defines CRUD operations for API tokens.
type APITokenStorer interface {
	ListAPITokens(ctx context.Context, q *query.Query) (*ListResult[APIToken], error)
	GetAPITokenByHash(ctx context.Context, hash string) (*APIToken, error)
	CreateAPIToken(ctx context.Context, token APIToken, tokenHash string) (*APIToken, error)
	UpdateAPIToken(ctx context.Context, id string, token APIToken) (*APIToken, error)
	DeleteAPIToken(ctx context.Context, id string) error
	UpdateLastUsed(ctx context.Context, id string) error
}

// ─── Token Usage Tracking ───

// TokenUsage represents cumulative usage statistics for a single API token + model combination.
type TokenUsage struct {
	TokenID          string     `json:"token_id"`
	Model            string     `json:"model"`
	PromptTokens     int64      `json:"prompt_tokens"`
	CompletionTokens int64      `json:"completion_tokens"`
	TotalTokens      int64      `json:"total_tokens"`
	RequestCount     int64      `json:"request_count"`
	LastRequestAt    types.Time `json:"last_request_at"`
}

// TokenUsageStorer defines operations for recording and querying per-token usage.
type TokenUsageStorer interface {
	// RecordUsage atomically increments usage counters for a token+model pair.
	RecordUsage(ctx context.Context, tokenID, model string, usage Usage) error
	// GetTokenUsage returns per-model usage breakdown for a token.
	GetTokenUsage(ctx context.Context, tokenID string) ([]TokenUsage, error)
	// GetTokenTotalUsage returns the sum of total_tokens across all models for a token.
	GetTokenTotalUsage(ctx context.Context, tokenID string) (int64, error)
	// ResetTokenUsage deletes all usage rows for a token and updates last_reset_at.
	ResetTokenUsage(ctx context.Context, tokenID string) error
}

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // Can be string or array of content blocks
}

type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
	Source    *MediaSource   `json:"source,omitempty"` // For media content blocks (images, documents, audio, video — Anthropic format)
	// ThoughtSignature is an opaque token from Gemini thinking models (2.5+)
	// that preserves the model's reasoning state across function-calling turns.
	// It must be echoed back on the corresponding tool_use content block.
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// MediaSource represents a media source for content blocks (images, documents, audio, video).
// Used by Anthropic-format content blocks where the source contains base64-encoded data
// or a URL reference.
type MediaSource struct {
	Type      string `json:"type"`                 // "base64" or "url"
	MediaType string `json:"media_type,omitempty"` // e.g. "image/png", "application/pdf", "audio/wav"
	Data      string `json:"data,omitempty"`       // base64-encoded data (when type="base64")
	URL       string `json:"url,omitempty"`        // URL reference (when type="url")
}

// Usage contains token usage statistics from the upstream provider.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type LLMResponse struct {
	Content          string
	ReasoningContent string
	InlineImages     []InlineImage
	ToolCalls        []ToolCall
	Finished         bool
	Usage            Usage
	Header           http.Header
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
	// ThoughtSignature is an opaque token from Gemini thinking models that
	// preserves the model's reasoning state across function-calling turns.
	// It must be echoed back in the subsequent request for the model to
	// maintain context continuity.
	ThoughtSignature string
}

// ─── Workflow Management ───

// WorkflowGraph is the full graph definition (nodes + edges) stored as JSON.
type WorkflowGraph struct {
	Nodes []WorkflowNode `json:"nodes"`
	Edges []WorkflowEdge `json:"edges"`
}

// WorkflowNode represents a single node in a workflow graph.
type WorkflowNode struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`                  // "input", "output", "llm_call", "template", "conditional", "loop", "mcp_tool", "code", "http_request"
	Position   WorkflowPos    `json:"position"`              // {x, y} for the visual editor
	Data       map[string]any `json:"data"`                  // node-type-specific configuration
	Width      *float64       `json:"width,omitempty"`       // visual width (groups, sticky notes)
	Height     *float64       `json:"height,omitempty"`      // visual height (groups, sticky notes)
	ParentID   string         `json:"parent_id,omitempty"`   // parent group node ID
	ZIndex     *int           `json:"z_index,omitempty"`     // layer ordering (groups = 0, nodes = 1)
	NodeNumber *int           `json:"node_number,omitempty"` // user-visible sequential number for logging
}

// WorkflowPos is the x/y position of a node in the visual editor.
type WorkflowPos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// WorkflowEdge connects two nodes via their handles/ports.
type WorkflowEdge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`        // source node ID
	Target       string `json:"target"`        // target node ID
	SourceHandle string `json:"source_handle"` // output port name on source
	TargetHandle string `json:"target_handle"` // input port name on target
}

// Workflow represents a saved workflow definition.
type Workflow struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	Graph         WorkflowGraph `json:"graph"`
	ActiveVersion *int          `json:"active_version,omitempty"`
	CreatedAt     string        `json:"created_at"`
	UpdatedAt     string        `json:"updated_at"`
	CreatedBy     string        `json:"created_by"`
	UpdatedBy     string        `json:"updated_by"`
}

// WorkflowVersion represents an immutable snapshot of a workflow at a point in time.
type WorkflowVersion struct {
	ID          string        `json:"id"`
	WorkflowID  string        `json:"workflow_id"`
	Version     int           `json:"version"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Graph       WorkflowGraph `json:"graph"`
	CreatedAt   string        `json:"created_at"`
	CreatedBy   string        `json:"created_by"`
}

// WorkflowStorer defines CRUD operations for workflow definitions.
type WorkflowStorer interface {
	ListWorkflows(ctx context.Context, q *query.Query) (*ListResult[Workflow], error)
	GetWorkflow(ctx context.Context, id string) (*Workflow, error)
	CreateWorkflow(ctx context.Context, w Workflow) (*Workflow, error)
	UpdateWorkflow(ctx context.Context, id string, w Workflow) (*Workflow, error)
	DeleteWorkflow(ctx context.Context, id string) error
}

// WorkflowVersionStorer defines operations for workflow version history.
type WorkflowVersionStorer interface {
	ListWorkflowVersions(ctx context.Context, workflowID string) ([]WorkflowVersion, error)
	GetWorkflowVersion(ctx context.Context, workflowID string, version int) (*WorkflowVersion, error)
	CreateWorkflowVersion(ctx context.Context, v WorkflowVersion) (*WorkflowVersion, error)
	SetActiveVersion(ctx context.Context, workflowID string, version int) error
}

// ─── Trigger Management ───

// Trigger represents a workflow trigger (HTTP webhook or cron schedule).
// Multiple triggers can reference the same workflow.
type Trigger struct {
	ID         string         `json:"id"`
	WorkflowID string         `json:"workflow_id"`
	Type       string         `json:"type"`            // "http" or "cron"
	Config     map[string]any `json:"config"`          // type-specific configuration
	Alias      string         `json:"alias,omitempty"` // optional human-friendly alias (unique)
	Public     bool           `json:"public"`          // if true, no auth required; if false, Bearer token required
	Enabled    bool           `json:"enabled"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
	CreatedBy  string         `json:"created_by"`
	UpdatedBy  string         `json:"updated_by"`
}

// TriggerStorer defines CRUD operations for workflow triggers.
type TriggerStorer interface {
	ListAllTriggers(ctx context.Context) ([]Trigger, error)
	ListTriggers(ctx context.Context, workflowID string) ([]Trigger, error)
	GetTrigger(ctx context.Context, id string) (*Trigger, error)
	GetTriggerByAlias(ctx context.Context, alias string) (*Trigger, error)
	CreateTrigger(ctx context.Context, t Trigger) (*Trigger, error)
	UpdateTrigger(ctx context.Context, id string, t Trigger) (*Trigger, error)
	DeleteTrigger(ctx context.Context, id string) error
	ListEnabledCronTriggers(ctx context.Context) ([]Trigger, error)
}

// ─── Skill Management ───

// Skill represents a reusable skill that bundles a system prompt fragment
// and a set of tools. Skills can be attached to agent_call workflow nodes
// to provide the agent with domain-specific capabilities.
type Skill struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	SystemPrompt string `json:"system_prompt"` // Prompt fragment appended to the agent's system prompt
	Tools        []Tool `json:"tools"`         // Built-in tool definitions (may include JS handlers)
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	CreatedBy    string `json:"created_by"`
	UpdatedBy    string `json:"updated_by"`
}

// SkillStorer defines CRUD operations for skill definitions.
type SkillStorer interface {
	ListSkills(ctx context.Context, q *query.Query) (*ListResult[Skill], error)
	GetSkill(ctx context.Context, id string) (*Skill, error)
	GetSkillByName(ctx context.Context, name string) (*Skill, error)
	CreateSkill(ctx context.Context, s Skill) (*Skill, error)
	UpdateSkill(ctx context.Context, id string, s Skill) (*Skill, error)
	DeleteSkill(ctx context.Context, id string) error
}

// ─── Variable Management ───

// Variable represents a key-value variable stored in the database.
// Variables can be secret (encrypted at rest, redacted in list responses)
// or non-secret (stored as plaintext, shown in list responses).
// Accessed from workflow JS handlers via getVar() and bash handlers via $VAR_<KEY>.
type Variable struct {
	ID          string `json:"id"`
	Key         string `json:"key"`         // unique key for lookup (e.g. "jira_token", "base_url")
	Value       string `json:"value"`       // plaintext value; encrypted at rest when Secret=true; redacted in API list responses for secrets
	Description string `json:"description"` // human-readable description
	Secret      bool   `json:"secret"`      // true = encrypted at rest, value redacted in list API
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	CreatedBy   string `json:"created_by"`
	UpdatedBy   string `json:"updated_by"`
}

// VariableStorer defines CRUD operations for variables.
type VariableStorer interface {
	ListVariables(ctx context.Context, q *query.Query) (*ListResult[Variable], error)
	GetVariable(ctx context.Context, id string) (*Variable, error)
	GetVariableByKey(ctx context.Context, key string) (*Variable, error)
	CreateVariable(ctx context.Context, v Variable) (*Variable, error)
	UpdateVariable(ctx context.Context, id string, v Variable) (*Variable, error)
	DeleteVariable(ctx context.Context, id string) error
}

// ─── Node Configs ───

// NodeConfig represents a reusable configuration for workflow nodes (e.g. SMTP server settings for email nodes).
// The Data field is a JSON blob whose schema depends on the Type (e.g. type "email" stores host, port, username, password, from, tls).
// Sensitive fields within Data (like password) are encrypted at rest.
type NodeConfig struct {
	ID        string `json:"id"`
	Name      string `json:"name"` // unique human-readable name (e.g. "Production SMTP")
	Type      string `json:"type"` // config type discriminator (e.g. "email", "slack", "sms")
	Data      string `json:"data"` // JSON blob with type-specific configuration
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`
}

// NodeConfigStorer defines CRUD operations for node configs.
type NodeConfigStorer interface {
	ListNodeConfigs(ctx context.Context, q *query.Query) (*ListResult[NodeConfig], error)
	ListNodeConfigsByType(ctx context.Context, configType string) ([]NodeConfig, error)
	GetNodeConfig(ctx context.Context, id string) (*NodeConfig, error)
	CreateNodeConfig(ctx context.Context, nc NodeConfig) (*NodeConfig, error)
	UpdateNodeConfig(ctx context.Context, id string, nc NodeConfig) (*NodeConfig, error)
	DeleteNodeConfig(ctx context.Context, id string) error
}

// ─── Agent Registry ───

// AgentConfig holds the configuration fields for an agent, stored as JSON in the database.
type AgentConfig struct {
	Description   string   `json:"description,omitempty"`
	Provider      string   `json:"provider"`                // Provider key
	Model         string   `json:"model,omitempty"`         // Model identifier
	SystemPrompt  string   `json:"system_prompt,omitempty"` // System prompt
	Skills        []string `json:"skills,omitempty"`        // List of skill IDs/names
	MCPs          []string `json:"mcp_urls,omitempty"`      // List of MCP server URLs
	MaxIterations int      `json:"max_iterations"`          // Max iterations for the loop
	ToolTimeout   int      `json:"tool_timeout"`            // Timeout in seconds
}

// Agent represents a reusable agent configuration that can be referenced
// by agent_call nodes in workflows.
type Agent struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Config    AgentConfig `json:"config"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
	CreatedBy string      `json:"created_by"`
	UpdatedBy string      `json:"updated_by"`
}

// AgentStorer defines CRUD operations for agents.
type AgentStorer interface {
	ListAgents(ctx context.Context, q *query.Query) (*ListResult[Agent], error)
	GetAgent(ctx context.Context, id string) (*Agent, error)
	CreateAgent(ctx context.Context, agent Agent) (*Agent, error)
	UpdateAgent(ctx context.Context, id string, agent Agent) (*Agent, error)
	DeleteAgent(ctx context.Context, id string) error
}

// ─── RAG Collections ───

// RAGCollectionConfig holds the configuration fields for a RAG collection, stored as JSON in the database.
type RAGCollectionConfig struct {
	Description         string               `json:"description,omitempty"`
	VectorStore         RAGVectorStoreConfig `json:"vector_store"`
	EmbeddingProvider   string               `json:"embedding_provider"` // key into AT providers
	EmbeddingModel      string               `json:"embedding_model,omitempty"`
	EmbeddingURL        string               `json:"embedding_url,omitempty"`         // optional custom embedding endpoint URL
	EmbeddingAPIType    string               `json:"embedding_api_type,omitempty"`    // "openai" (default) or "gemini"
	EmbeddingBearerAuth bool                 `json:"embedding_bearer_auth,omitempty"` // if true, use Bearer auth header
	ChunkSize           int                  `json:"chunk_size"`
	ChunkOverlap        int                  `json:"chunk_overlap"`
}

// RAGCollection represents a named namespace for RAG documents.
// Each collection has its own vector store backend and embedding configuration.
// Documents are stored directly in the vector store (no separate document metadata table).
type RAGCollection struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Config    RAGCollectionConfig `json:"config"`
	CreatedAt string              `json:"created_at"`
	UpdatedAt string              `json:"updated_at"`
	CreatedBy string              `json:"created_by"`
	UpdatedBy string              `json:"updated_by"`
}

// RAGVectorStoreConfig holds the type and connection parameters for a vector store backend.
type RAGVectorStoreConfig struct {
	Type   string         `json:"type"`   // "pgvector", "chroma", "qdrant", "weaviate", "pinecone", "milvus", etc.
	Config map[string]any `json:"config"` // type-specific config (connection URL, API key, etc.)
}

// RAGCollectionStorer defines CRUD operations for RAG collection configurations.
type RAGCollectionStorer interface {
	ListRAGCollections(ctx context.Context, q *query.Query) (*ListResult[RAGCollection], error)
	GetRAGCollection(ctx context.Context, id string) (*RAGCollection, error)
	GetRAGCollectionByName(ctx context.Context, name string) (*RAGCollection, error)
	CreateRAGCollection(ctx context.Context, c RAGCollection) (*RAGCollection, error)
	UpdateRAGCollection(ctx context.Context, id string, c RAGCollection) (*RAGCollection, error)
	DeleteRAGCollection(ctx context.Context, id string) error
}

// ─── RAG State ───

// RAGState represents the last processed state for a RAG source (e.g. git commit hash).
type RAGState struct {
	Key       string     `json:"key"`
	Value     string     `json:"value"`
	UpdatedAt types.Time `json:"updated_at"`
}

// RAGStateStorer defines CRUD operations for RAG states.
type RAGStateStorer interface {
	GetRAGState(ctx context.Context, key string) (*RAGState, error)
	SetRAGState(ctx context.Context, key string, value string) error
}

// ─── RAG MCP Servers ───

// RAGMCPServerConfig holds the configuration for a named RAG MCP endpoint.
type RAGMCPServerConfig struct {
	Description       string   `json:"description,omitempty"`
	CollectionIDs     []string `json:"collection_ids"`                // which RAG collections this MCP searches
	EnabledTools      []string `json:"enabled_tools"`                 // subset of: rag_search, rag_list_collections, rag_fetch_source
	FetchMode         string   `json:"fetch_mode"`                    // "auto" | "local" | "remote"
	GitCacheDir       string   `json:"git_cache_dir,omitempty"`       // default: /tmp/at-git-cache
	DefaultNumResults int      `json:"default_num_results,omitempty"` // default: 10
	TokenVariable     string   `json:"token_variable,omitempty"`      // variable key for HTTPS auth token (resolved via VariableStorer)
	TokenUser         string   `json:"token_user,omitempty"`          // username for HTTPS token auth (default: "x-token-auth"); e.g. "oauth2" for GitLab
	SSHKeyVariable    string   `json:"ssh_key_variable,omitempty"`    // variable key for SSH private key (resolved via VariableStorer)
}

// RAGMCPServer represents a named, gateway-facing MCP endpoint that exposes
// RAG tools scoped to specific collections. External agents connect to
// /gateway/v1/mcp/rag/{name} to use these tools.
type RAGMCPServer struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"` // URL-safe slug used in the endpoint path
	Config    RAGMCPServerConfig `json:"config"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
	CreatedBy string             `json:"created_by"`
	UpdatedBy string             `json:"updated_by"`
}

// RAGMCPServerStorer defines CRUD operations for RAG MCP server configurations.
type RAGMCPServerStorer interface {
	ListRAGMCPServers(ctx context.Context, q *query.Query) (*ListResult[RAGMCPServer], error)
	GetRAGMCPServer(ctx context.Context, id string) (*RAGMCPServer, error)
	GetRAGMCPServerByName(ctx context.Context, name string) (*RAGMCPServer, error)
	CreateRAGMCPServer(ctx context.Context, s RAGMCPServer) (*RAGMCPServer, error)
	UpdateRAGMCPServer(ctx context.Context, id string, s RAGMCPServer) (*RAGMCPServer, error)
	DeleteRAGMCPServer(ctx context.Context, id string) error
}
