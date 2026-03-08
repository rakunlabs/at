package service

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

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

// Agent status constants for lifecycle management.
const (
	AgentStatusActive     = "active"
	AgentStatusPaused     = "paused"
	AgentStatusTerminated = "terminated"
)

// AgentConfig holds the configuration fields for an agent, stored as JSON in the database.
type AgentConfig struct {
	Description               string   `json:"description,omitempty"`
	Provider                  string   `json:"provider"`                              // Provider key
	Model                     string   `json:"model,omitempty"`                       // Model identifier
	SystemPrompt              string   `json:"system_prompt,omitempty"`               // System prompt
	Skills                    []string `json:"skills,omitempty"`                      // List of skill IDs/names
	MCPs                      []string `json:"mcp_urls,omitempty"`                    // List of MCP server URLs (legacy)
	MCPSets                   []string `json:"mcp_sets,omitempty"`                    // List of MCP Set names
	BuiltinTools              []string `json:"builtin_tools,omitempty"`               // Enabled builtin tool names
	MaxIterations             int      `json:"max_iterations"`                        // Max iterations for the loop
	ToolTimeout               int      `json:"tool_timeout"`                          // Timeout in seconds
	ConfirmationRequiredTools []string `json:"confirmation_required_tools,omitempty"` // Tools that require human confirmation before execution

	// NOTE: Organizational fields (role, title, parent_agent_id, organization_id,
	// status, delegation_rules) have been moved to the OrganizationAgent join table
	// so that agents can belong to multiple organizations with per-org metadata.
	// Only heartbeat_schedule remains here because it is agent-global.
	HeartbeatSchedule string `json:"heartbeat_schedule,omitempty"` // Cron expression for periodic wake-ups
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

// ─── Organizations (Multi-Tenant Isolation) ───

// Organization represents a tenant scope for grouping agents, goals, and tasks.
// All organizational entities (goals, tasks, agents) can be scoped to an organization.
// When OrganizationID is empty on an entity, it belongs to the global/legacy scope.
type Organization struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	IssuePrefix          string          `json:"issue_prefix,omitempty"`                // e.g. "PAP" — used for human-readable issue identifiers like "PAP-42"
	IssueCounter         int64           `json:"issue_counter,omitempty"`               // atomic sequential counter for issue identifiers
	BudgetMonthlyCents   int64           `json:"budget_monthly_cents,omitempty"`        // company-level monthly budget in cents
	SpentMonthlyCents    int64           `json:"spent_monthly_cents,omitempty"`         // company-level monthly spend accumulator in cents
	BudgetResetAt        string          `json:"budget_reset_at,omitempty"`             // when the monthly budget was last reset (RFC3339)
	RequireBoardApproval bool            `json:"require_board_approval_for_new_agents"` // if true, new agent creation requires approval
	HeadAgentID          string          `json:"head_agent_id,omitempty"`               // the designated head agent for this organization's hierarchy
	MaxDelegationDepth   int             `json:"max_delegation_depth,omitempty"`        // configurable maximum delegation chain depth (default: 10)
	CanvasLayout         json.RawMessage `json:"canvas_layout,omitempty"`               // JSON blob storing canvas groups, sticky notes, and node positions
	CreatedAt            string          `json:"created_at"`
	UpdatedAt            string          `json:"updated_at"`
	CreatedBy            string          `json:"created_by"`
	UpdatedBy            string          `json:"updated_by"`
}

// OrganizationStorer defines CRUD operations for organizations.
type OrganizationStorer interface {
	ListOrganizations(ctx context.Context, q *query.Query) (*ListResult[Organization], error)
	GetOrganization(ctx context.Context, id string) (*Organization, error)
	CreateOrganization(ctx context.Context, org Organization) (*Organization, error)
	UpdateOrganization(ctx context.Context, id string, org Organization) (*Organization, error)
	DeleteOrganization(ctx context.Context, id string) error
	// IncrementIssueCounter atomically increments the issue counter for an organization
	// and returns the new value. Used when creating tasks/issues to generate identifiers.
	IncrementIssueCounter(ctx context.Context, orgID string) (int64, error)
}

// ─── Organization–Agent Membership (Join Table) ───

// OrganizationAgent represents the many-to-many relationship between an
// organization and an agent.  Per-org metadata (role, title, hierarchy) lives
// here instead of inside the agent's config blob, so the same agent can
// participate in multiple organizations with different roles.
type OrganizationAgent struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	AgentID        string `json:"agent_id"`
	Role           string `json:"role,omitempty"`            // e.g. "CTO", "Engineer"
	Title          string `json:"title,omitempty"`           // e.g. "Senior Backend Engineer"
	ParentAgentID  string `json:"parent_agent_id,omitempty"` // reporting line within this org
	Status         string `json:"status,omitempty"`          // "active" (default), "paused", "terminated"
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// OrganizationAgentStorer defines CRUD operations for the organization–agent
// join table.
type OrganizationAgentStorer interface {
	// ListOrganizationAgents returns all agent memberships for an organization.
	ListOrganizationAgents(ctx context.Context, orgID string) ([]OrganizationAgent, error)
	// ListAgentOrganizations returns all org memberships for a single agent.
	ListAgentOrganizations(ctx context.Context, agentID string) ([]OrganizationAgent, error)
	// GetOrganizationAgent returns a single membership by ID.
	GetOrganizationAgent(ctx context.Context, id string) (*OrganizationAgent, error)
	// GetOrganizationAgentByPair returns the membership for a specific (org, agent) pair.
	GetOrganizationAgentByPair(ctx context.Context, orgID, agentID string) (*OrganizationAgent, error)
	// CreateOrganizationAgent adds an agent to an organization.
	CreateOrganizationAgent(ctx context.Context, oa OrganizationAgent) (*OrganizationAgent, error)
	// UpdateOrganizationAgent updates role/title/parent/status of a membership.
	UpdateOrganizationAgent(ctx context.Context, id string, oa OrganizationAgent) (*OrganizationAgent, error)
	// DeleteOrganizationAgent removes an agent from an organization.
	DeleteOrganizationAgent(ctx context.Context, id string) error
	// DeleteOrganizationAgentByPair removes a membership by (org, agent) pair.
	DeleteOrganizationAgentByPair(ctx context.Context, orgID, agentID string) error
}

// ─── Goals (Mission Alignment) ───

// Goal level constants (Paperclip-style).
const (
	GoalLevelCompany = "company"
	GoalLevelTeam    = "team"
	GoalLevelAgent   = "agent"
	GoalLevelTask    = "task"
)

// Goal represents a hierarchical objective in an organization.
// Goals form a tree: company mission → project goals → task-level goals.
// Every task links to a goal, giving agents full "why" context.
type Goal struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id,omitempty"` // tenant scope
	ParentGoalID   string `json:"parent_goal_id,omitempty"`  // parent in the goal hierarchy
	Name           string `json:"name"`
	Description    string `json:"description"`
	Level          string `json:"level,omitempty"` // "company", "team", "agent", "task" — scoping level
	Status         string `json:"status"`          // "active", "completed", "archived"
	Priority       int    `json:"priority"`        // higher = more important
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	CreatedBy      string `json:"created_by"`
	UpdatedBy      string `json:"updated_by"`
}

// GoalStorer defines CRUD operations for goals.
type GoalStorer interface {
	ListGoals(ctx context.Context, q *query.Query) (*ListResult[Goal], error)
	GetGoal(ctx context.Context, id string) (*Goal, error)
	CreateGoal(ctx context.Context, goal Goal) (*Goal, error)
	UpdateGoal(ctx context.Context, id string, goal Goal) (*Goal, error)
	DeleteGoal(ctx context.Context, id string) error
	// ListGoalsByParent returns all direct child goals of a parent.
	ListGoalsByParent(ctx context.Context, parentID string) ([]Goal, error)
	// GetGoalAncestry returns the full chain from the given goal up to the root mission.
	// The result is ordered from the given goal (index 0) to the root (last element).
	GetGoalAncestry(ctx context.Context, id string) ([]Goal, error)
}

// ─── Tasks (Ticket System) ───

// Task status constants (Paperclip-compatible).
const (
	TaskStatusBacklog    = "backlog"
	TaskStatusOpen       = "open"
	TaskStatusTodo       = "todo"
	TaskStatusInProgress = "in_progress"
	TaskStatusInReview   = "in_review"
	TaskStatusBlocked    = "blocked"
	TaskStatusReview     = "review"
	TaskStatusCompleted  = "completed"
	TaskStatusDone       = "done"
	TaskStatusCancelled  = "cancelled"
)

// Task priority constants (Paperclip-compatible).
const (
	TaskPriorityCritical = "critical"
	TaskPriorityHigh     = "high"
	TaskPriorityMedium   = "medium"
	TaskPriorityLow      = "low"
)

// Task represents a unit of work (issue) assigned to an agent, linked to a goal.
// Tasks support atomic checkout to prevent double-work across agents.
// Enhanced with Paperclip-style fields: identifier, parent hierarchy, project link, billing.
type Task struct {
	ID              string `json:"id"`
	OrganizationID  string `json:"organization_id,omitempty"` // tenant scope
	ProjectID       string `json:"project_id,omitempty"`      // link to project
	GoalID          string `json:"goal_id,omitempty"`         // links to goal hierarchy for "why" context
	ParentID        string `json:"parent_id,omitempty"`       // parent task/issue ID for sub-issues
	AssignedAgentID string `json:"assigned_agent_id,omitempty"`
	Identifier      string `json:"identifier,omitempty"` // human-readable like "PAP-42" (auto-generated from org prefix + counter)
	Title           string `json:"title"`
	Description     string `json:"description"`
	Status          string `json:"status"`                   // "backlog", "todo", "in_progress", "in_review", "blocked", "done", "cancelled"
	PriorityLevel   string `json:"priority_level,omitempty"` // "critical", "high", "medium", "low"
	Priority        int    `json:"priority"`                 // numeric priority (higher = more important), kept for backward compat
	Result          string `json:"result,omitempty"`         // output/result of the completed task
	BillingCode     string `json:"billing_code,omitempty"`   // cost attribution code
	RequestDepth    int    `json:"request_depth,omitempty"`  // cross-team delegation depth
	CheckedOutBy    string `json:"checked_out_by,omitempty"` // agent ID holding exclusive lock
	CheckedOutAt    string `json:"checked_out_at,omitempty"` // when the checkout happened
	StartedAt       string `json:"started_at,omitempty"`     // when work actually started
	CompletedAt     string `json:"completed_at,omitempty"`   // when task was completed
	CancelledAt     string `json:"cancelled_at,omitempty"`   // when task was cancelled
	HiddenAt        string `json:"hidden_at,omitempty"`      // soft-hide timestamp
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	CreatedBy       string `json:"created_by"`
	UpdatedBy       string `json:"updated_by"`
}

// TaskStorer defines CRUD operations for tasks.
type TaskStorer interface {
	ListTasks(ctx context.Context, q *query.Query) (*ListResult[Task], error)
	GetTask(ctx context.Context, id string) (*Task, error)
	CreateTask(ctx context.Context, task Task) (*Task, error)
	UpdateTask(ctx context.Context, id string, task Task) (*Task, error)
	DeleteTask(ctx context.Context, id string) error
	// ListTasksByAgent returns all tasks assigned to an agent.
	ListTasksByAgent(ctx context.Context, agentID string) ([]Task, error)
	// ListTasksByGoal returns all tasks linked to a goal.
	ListTasksByGoal(ctx context.Context, goalID string) ([]Task, error)
	// CheckoutTask atomically sets checked_out_by to agentID if the task is not already checked out.
	// Returns an error if the task is already checked out by another agent.
	CheckoutTask(ctx context.Context, taskID, agentID string) error
	// ReleaseTask clears the checked_out_by field, making the task available again.
	ReleaseTask(ctx context.Context, taskID string) error
	// ListChildTasks returns all tasks with the given parent_id.
	ListChildTasks(ctx context.Context, parentID string) ([]Task, error)
	// UpdateTaskStatus updates only the status and result fields of a task.
	// Safe for concurrent use — no field clobbering.
	UpdateTaskStatus(ctx context.Context, id string, status string, result string) error
}

// ─── Agent Budgets & Cost Tracking ───

// AgentBudget represents a spending limit for an agent within a time period.
type AgentBudget struct {
	ID           string  `json:"id"`
	AgentID      string  `json:"agent_id"`
	MonthlyLimit float64 `json:"monthly_limit"` // USD
	CurrentSpend float64 `json:"current_spend"` // accumulated this period
	PeriodStart  string  `json:"period_start"`
	PeriodEnd    string  `json:"period_end"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// AgentUsageRecord represents a single cost event from an agent's LLM call.
type AgentUsageRecord struct {
	ID               string  `json:"id"`
	AgentID          string  `json:"agent_id"`
	TaskID           string  `json:"task_id,omitempty"`         // optional: which task incurred this
	WorkflowRunID    string  `json:"workflow_run_id,omitempty"` // optional: which workflow run
	SessionID        string  `json:"session_id,omitempty"`      // optional: which chat session
	Model            string  `json:"model"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	EstimatedCost    float64 `json:"estimated_cost"` // USD, calculated from model pricing
	CreatedAt        string  `json:"created_at"`
}

// ModelPricing defines the cost per token for a specific provider/model combination.
type ModelPricing struct {
	ID                   string  `json:"id"`
	ProviderKey          string  `json:"provider_key"`
	Model                string  `json:"model"`
	PromptPricePer1M     float64 `json:"prompt_price_per_1m"`     // USD per 1M prompt tokens
	CompletionPricePer1M float64 `json:"completion_price_per_1m"` // USD per 1M completion tokens
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
}

// AgentBudgetStorer defines operations for agent budgets and cost tracking.
type AgentBudgetStorer interface {
	// Budget CRUD
	GetAgentBudget(ctx context.Context, agentID string) (*AgentBudget, error)
	SetAgentBudget(ctx context.Context, budget AgentBudget) error // upsert by agent_id

	// Usage recording
	RecordAgentUsage(ctx context.Context, usage AgentUsageRecord) error
	GetAgentUsage(ctx context.Context, agentID string, q *query.Query) (*ListResult[AgentUsageRecord], error)
	GetAgentTotalSpend(ctx context.Context, agentID string) (float64, error)

	// Model pricing
	ListModelPricing(ctx context.Context) ([]ModelPricing, error)
	SetModelPricing(ctx context.Context, pricing ModelPricing) error // upsert by provider_key + model
}

// ─── Audit Trail ───

// AuditEntry represents an immutable log entry for agent/system actions.
// Audit entries are append-only — they cannot be updated or deleted.
type AuditEntry struct {
	ID             string         `json:"id"`
	OrganizationID string         `json:"organization_id,omitempty"`
	ActorType      string         `json:"actor_type"`    // "agent", "user", "system"
	ActorID        string         `json:"actor_id"`      // agent ID, user email, or "system"
	Action         string         `json:"action"`        // "tool_call", "task_checkout", "status_change", "config_update", etc.
	ResourceType   string         `json:"resource_type"` // "agent", "task", "goal", "workflow", etc.
	ResourceID     string         `json:"resource_id"`
	Details        map[string]any `json:"details,omitempty"` // action-specific payload
	CreatedAt      string         `json:"created_at"`
}

// AuditStorer defines operations for the immutable audit log.
type AuditStorer interface {
	// RecordAudit appends a new audit entry. Entries are immutable.
	RecordAudit(ctx context.Context, entry AuditEntry) error
	// ListAuditEntries returns audit entries matching the given filters.
	ListAuditEntries(ctx context.Context, q *query.Query) (*ListResult[AuditEntry], error)
	// GetAuditTrail returns all audit entries for a specific resource.
	GetAuditTrail(ctx context.Context, resourceType, resourceID string) ([]AuditEntry, error)
}

// ─── Agent Heartbeats ───

// AgentHeartbeat tracks the last heartbeat for an agent.
type AgentHeartbeat struct {
	AgentID         string         `json:"agent_id"`
	Status          string         `json:"status"` // "healthy", "stale", "unresponsive"
	LastHeartbeatAt string         `json:"last_heartbeat_at"`
	Metadata        map[string]any `json:"metadata,omitempty"` // free-form agent state
	UpdatedAt       string         `json:"updated_at"`
}

// AgentHeartbeatStorer defines operations for agent heartbeat tracking.
type AgentHeartbeatStorer interface {
	// RecordHeartbeat upserts a heartbeat record for an agent.
	RecordHeartbeat(ctx context.Context, agentID string, metadata map[string]any) error
	// GetHeartbeat returns the heartbeat record for an agent, or nil if none exists.
	GetHeartbeat(ctx context.Context, agentID string) (*AgentHeartbeat, error)
	// ListHeartbeats returns all heartbeat records.
	ListHeartbeats(ctx context.Context) ([]AgentHeartbeat, error)
	// MarkStale marks agents whose last heartbeat is older than the given threshold as stale.
	MarkStale(ctx context.Context, threshold time.Duration) (int, error)
}

// ─── Projects ───

// Project links goals to actual work, tracking progress and ownership.
type Project struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id,omitempty"`
	GoalID         string `json:"goal_id,omitempty"`       // which goal this project serves
	LeadAgentID    string `json:"lead_agent_id,omitempty"` // agent leading this project
	Name           string `json:"name"`
	Description    string `json:"description"`
	Status         string `json:"status"`                // "active", "completed", "archived", "on_hold"
	Color          string `json:"color,omitempty"`       // hex color for UI display
	TargetDate     string `json:"target_date,omitempty"` // RFC3339 target completion date
	ArchivedAt     string `json:"archived_at,omitempty"` // when archived
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	CreatedBy      string `json:"created_by"`
	UpdatedBy      string `json:"updated_by"`
}

// ProjectStorer defines CRUD operations for projects.
type ProjectStorer interface {
	ListProjects(ctx context.Context, q *query.Query) (*ListResult[Project], error)
	GetProject(ctx context.Context, id string) (*Project, error)
	CreateProject(ctx context.Context, project Project) (*Project, error)
	UpdateProject(ctx context.Context, id string, project Project) (*Project, error)
	DeleteProject(ctx context.Context, id string) error
	ListProjectsByGoal(ctx context.Context, goalID string) ([]Project, error)
	ListProjectsByOrganization(ctx context.Context, orgID string) ([]Project, error)
}

// ─── Issue Comments ───

// IssueComment represents a threaded comment on a task/issue.
type IssueComment struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`             // the task/issue this comment belongs to
	AuthorType string `json:"author_type"`         // "agent", "user", "system"
	AuthorID   string `json:"author_id"`           // agent ID, user email, or "system"
	Body       string `json:"body"`                // comment text (markdown)
	ParentID   string `json:"parent_id,omitempty"` // for threaded replies
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// IssueCommentStorer defines operations for issue comments.
type IssueCommentStorer interface {
	ListCommentsByTask(ctx context.Context, taskID string) ([]IssueComment, error)
	GetComment(ctx context.Context, id string) (*IssueComment, error)
	CreateComment(ctx context.Context, comment IssueComment) (*IssueComment, error)
	UpdateComment(ctx context.Context, id string, comment IssueComment) (*IssueComment, error)
	DeleteComment(ctx context.Context, id string) error
}

// ─── Labels ───

// Label represents a per-organization label with a color, used to tag tasks.
type Label struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id,omitempty"` // tenant scope
	Name           string `json:"name"`
	Color          string `json:"color"` // hex color e.g. "#ff5500"
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// LabelStorer defines CRUD operations for labels and task-label associations.
type LabelStorer interface {
	ListLabels(ctx context.Context, orgID string) ([]Label, error)
	GetLabel(ctx context.Context, id string) (*Label, error)
	CreateLabel(ctx context.Context, label Label) (*Label, error)
	UpdateLabel(ctx context.Context, id string, label Label) (*Label, error)
	DeleteLabel(ctx context.Context, id string) error
	// Task-label associations
	AddLabelToTask(ctx context.Context, taskID, labelID string) error
	RemoveLabelFromTask(ctx context.Context, taskID, labelID string) error
	ListLabelsForTask(ctx context.Context, taskID string) ([]Label, error)
	ListTasksForLabel(ctx context.Context, labelID string) ([]string, error) // returns task IDs
}

// ─── Heartbeat Runs (Execution Tracking) ───

// HeartbeatRun invocation source constants.
const (
	InvocationTimer      = "timer"
	InvocationAssignment = "assignment"
	InvocationOnDemand   = "on_demand"
	InvocationAutomation = "automation"
)

// HeartbeatRun status constants.
const (
	RunStatusQueued    = "queued"
	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"
	RunStatusTimedOut  = "timed_out"
)

// HeartbeatRun represents a single execution run for an agent's heartbeat.
// This is the core execution tracking unit — each run tracks invocation source,
// status, context snapshot, usage, logs, and session state.
type HeartbeatRun struct {
	ID               string         `json:"id"`
	AgentID          string         `json:"agent_id"`
	InvocationSource string         `json:"invocation_source"`          // "timer", "assignment", "on_demand", "automation"
	TriggerDetail    string         `json:"trigger_detail,omitempty"`   // human-readable trigger info
	Status           string         `json:"status"`                     // "queued", "running", "succeeded", "failed", "cancelled", "timed_out"
	ContextSnapshot  map[string]any `json:"context_snapshot,omitempty"` // carries issueId, taskKey, commentId, wakeReason
	UsageJSON        map[string]any `json:"usage_json,omitempty"`       // token usage from adapter
	ResultJSON       map[string]any `json:"result_json,omitempty"`      // adapter result
	LogRef           string         `json:"log_ref,omitempty"`          // reference to log storage
	LogBytes         int64          `json:"log_bytes,omitempty"`        // size of stored log
	LogSHA256        string         `json:"log_sha256,omitempty"`       // integrity hash
	StdoutExcerpt    string         `json:"stdout_excerpt,omitempty"`   // tail of stdout
	StderrExcerpt    string         `json:"stderr_excerpt,omitempty"`   // tail of stderr
	SessionIDBefore  string         `json:"session_id_before,omitempty"`
	SessionIDAfter   string         `json:"session_id_after,omitempty"`
	StartedAt        string         `json:"started_at,omitempty"`
	FinishedAt       string         `json:"finished_at,omitempty"`
	CreatedAt        string         `json:"created_at"`
}

// HeartbeatRunStorer defines operations for heartbeat run tracking.
type HeartbeatRunStorer interface {
	CreateHeartbeatRun(ctx context.Context, run HeartbeatRun) (*HeartbeatRun, error)
	GetHeartbeatRun(ctx context.Context, id string) (*HeartbeatRun, error)
	UpdateHeartbeatRun(ctx context.Context, id string, run HeartbeatRun) (*HeartbeatRun, error)
	ListHeartbeatRuns(ctx context.Context, agentID string, q *query.Query) (*ListResult[HeartbeatRun], error)
	// GetActiveRun returns the currently-running heartbeat run for an agent, or nil if none.
	GetActiveRun(ctx context.Context, agentID string) (*HeartbeatRun, error)
}

// ─── Wakeup Requests ───

// WakeupRequest status constants.
const (
	WakeupStatusPending                = "pending"
	WakeupStatusDispatched             = "dispatched"
	WakeupStatusDeferredIssueExecution = "deferred_issue_execution"
	WakeupStatusCancelled              = "cancelled"
)

// WakeupRequest represents a request to wake up an agent.
// Supports coalescing: multiple wakeups for the same agent merge context
// and increment coalesced_count, using idempotency keys for dedup.
type WakeupRequest struct {
	ID             string         `json:"id"`
	AgentID        string         `json:"agent_id"`
	Status         string         `json:"status"`                    // "pending", "dispatched", "deferred_issue_execution", "cancelled"
	IdempotencyKey string         `json:"idempotency_key,omitempty"` // for deduplication
	Context        map[string]any `json:"context,omitempty"`         // merged context from coalesced requests
	CoalescedCount int            `json:"coalesced_count"`           // how many requests were merged
	RunID          string         `json:"run_id,omitempty"`          // heartbeat run that handled this wakeup
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}

// WakeupRequestStorer defines operations for wakeup requests with coalescing.
type WakeupRequestStorer interface {
	// CreateOrCoalesce creates a new wakeup request, or coalesces with an existing
	// pending request for the same agent (merging context, incrementing count).
	// If idempotencyKey is non-empty and matches an existing request, returns the existing one.
	CreateOrCoalesce(ctx context.Context, req WakeupRequest) (*WakeupRequest, error)
	GetWakeupRequest(ctx context.Context, id string) (*WakeupRequest, error)
	// ListPendingForAgent returns all pending wakeup requests for an agent, ordered FIFO.
	ListPendingForAgent(ctx context.Context, agentID string) ([]WakeupRequest, error)
	// MarkDispatched marks a wakeup request as dispatched, linking it to a run.
	MarkDispatched(ctx context.Context, id, runID string) error
	// PromoteDeferred promotes the next deferred wakeup request to pending for an agent.
	PromoteDeferred(ctx context.Context, agentID string) error
}

// ─── Agent Runtime State ───

// AgentRuntimeState represents persistent per-agent runtime state (1:1 with agent).
// Tracks session, accumulated costs, and last run info.
type AgentRuntimeState struct {
	AgentID           string         `json:"agent_id"`
	SessionID         string         `json:"session_id,omitempty"`
	StateJSON         map[string]any `json:"state_json,omitempty"` // persistent agent state
	TotalInputTokens  int64          `json:"total_input_tokens"`
	TotalOutputTokens int64          `json:"total_output_tokens"`
	TotalCostCents    int64          `json:"total_cost_cents"`
	LastRunID         string         `json:"last_run_id,omitempty"`
	LastRunStatus     string         `json:"last_run_status,omitempty"`
	LastError         string         `json:"last_error,omitempty"`
	UpdatedAt         string         `json:"updated_at"`
}

// AgentRuntimeStateStorer defines operations for persistent agent runtime state.
type AgentRuntimeStateStorer interface {
	GetAgentRuntimeState(ctx context.Context, agentID string) (*AgentRuntimeState, error)
	// UpsertAgentRuntimeState creates or updates the runtime state for an agent.
	UpsertAgentRuntimeState(ctx context.Context, state AgentRuntimeState) error
	// AccumulateUsage atomically increments token and cost counters.
	AccumulateUsage(ctx context.Context, agentID string, inputTokens, outputTokens, costCents int64) error
}

// ─── Agent Task Sessions ───

// AgentTaskSession represents a per-agent, per-task session.
// Keyed by (agent_id, task_key). Sessions are RESET on new assignment
// but PRESERVED across heartbeats for the same task.
type AgentTaskSession struct {
	ID                string         `json:"id"`
	AgentID           string         `json:"agent_id"`
	TaskKey           string         `json:"task_key"`                      // task identifier or ID
	AdapterType       string         `json:"adapter_type,omitempty"`        // adapter-specific type
	SessionParamsJSON map[string]any `json:"session_params_json,omitempty"` // adapter-specific session state
	SessionDisplayID  string         `json:"session_display_id,omitempty"`  // human-readable session ID
	CreatedAt         string         `json:"created_at"`
	UpdatedAt         string         `json:"updated_at"`
}

// AgentTaskSessionStorer defines operations for per-task agent sessions.
type AgentTaskSessionStorer interface {
	GetAgentTaskSession(ctx context.Context, agentID, taskKey string) (*AgentTaskSession, error)
	UpsertAgentTaskSession(ctx context.Context, session AgentTaskSession) error
	DeleteAgentTaskSession(ctx context.Context, agentID, taskKey string) error
	ListAgentTaskSessions(ctx context.Context, agentID string) ([]AgentTaskSession, error)
}

// ─── Approvals ───

// Approval type constants.
const (
	ApprovalTypeHireAgent    = "hire_agent"
	ApprovalTypeBudgetChange = "budget_change"
	ApprovalTypeTaskEscalate = "task_escalate"
)

// Approval status constants.
const (
	ApprovalStatusPending           = "pending"
	ApprovalStatusRevisionRequested = "revision_requested"
	ApprovalStatusApproved          = "approved"
	ApprovalStatusRejected          = "rejected"
	ApprovalStatusApprovalCancelled = "cancelled"
)

// Approval represents a governance approval request.
type Approval struct {
	ID              string         `json:"id"`
	OrganizationID  string         `json:"organization_id,omitempty"`
	Type            string         `json:"type"`              // "hire_agent", "budget_change", "task_escalate"
	Status          string         `json:"status"`            // "pending", "revision_requested", "approved", "rejected", "cancelled"
	RequestedByType string         `json:"requested_by_type"` // "agent" or "user"
	RequestedByID   string         `json:"requested_by_id"`
	RequestDetails  map[string]any `json:"request_details,omitempty"` // type-specific request payload
	DecisionNote    string         `json:"decision_note,omitempty"`
	DecidedByUserID string         `json:"decided_by_user_id,omitempty"`
	DecidedAt       string         `json:"decided_at,omitempty"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
}

// ApprovalStorer defines operations for the approval workflow.
type ApprovalStorer interface {
	ListApprovals(ctx context.Context, q *query.Query) (*ListResult[Approval], error)
	GetApproval(ctx context.Context, id string) (*Approval, error)
	CreateApproval(ctx context.Context, approval Approval) (*Approval, error)
	UpdateApproval(ctx context.Context, id string, approval Approval) (*Approval, error)
	ListPendingApprovals(ctx context.Context, orgID string) ([]Approval, error)
}

// ─── Agent Config Revisions ───

// AgentConfigRevision captures a full before/after snapshot of an agent config change.
type AgentConfigRevision struct {
	ID           string      `json:"id"`
	AgentID      string      `json:"agent_id"`
	Version      int         `json:"version"`               // sequential version number
	ConfigBefore AgentConfig `json:"config_before"`         // snapshot before the change
	ConfigAfter  AgentConfig `json:"config_after"`          // snapshot after the change
	ChangedBy    string      `json:"changed_by"`            // user email or "system"
	ChangeNote   string      `json:"change_note,omitempty"` // human-readable description
	CreatedAt    string      `json:"created_at"`
}

// AgentConfigRevisionStorer defines operations for agent config revision tracking.
type AgentConfigRevisionStorer interface {
	// CreateRevision records a new config revision for an agent.
	CreateRevision(ctx context.Context, rev AgentConfigRevision) (*AgentConfigRevision, error)
	// ListRevisions returns all revisions for an agent, newest first.
	ListRevisions(ctx context.Context, agentID string) ([]AgentConfigRevision, error)
	// GetRevision returns a specific revision by ID.
	GetRevision(ctx context.Context, id string) (*AgentConfigRevision, error)
	// GetLatestRevision returns the most recent revision for an agent.
	GetLatestRevision(ctx context.Context, agentID string) (*AgentConfigRevision, error)
}

// ─── Cost Events ───

// CostEvent records a single LLM call cost, linked to agent, issue, project, goal, and billing code.
type CostEvent struct {
	ID             string  `json:"id"`
	OrganizationID string  `json:"organization_id,omitempty"`
	AgentID        string  `json:"agent_id"`
	TaskID         string  `json:"task_id,omitempty"`      // issue that incurred this cost
	ProjectID      string  `json:"project_id,omitempty"`   // project attribution
	GoalID         string  `json:"goal_id,omitempty"`      // goal attribution
	BillingCode    string  `json:"billing_code,omitempty"` // cost center code
	RunID          string  `json:"run_id,omitempty"`       // heartbeat run that generated this
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	InputTokens    int64   `json:"input_tokens"`
	OutputTokens   int64   `json:"output_tokens"`
	CostCents      float64 `json:"cost_cents"` // cost in cents
	CreatedAt      string  `json:"created_at"`
}

// CostEventStorer defines operations for per-call cost tracking.
type CostEventStorer interface {
	RecordCostEvent(ctx context.Context, event CostEvent) error
	ListCostEvents(ctx context.Context, q *query.Query) (*ListResult[CostEvent], error)
	// GetCostSummary returns total cost in cents for a given filter (agent, project, goal, billing code).
	GetCostByAgent(ctx context.Context, agentID string) (float64, error)
	GetCostByProject(ctx context.Context, projectID string) (float64, error)
	GetCostByGoal(ctx context.Context, goalID string) (float64, error)
	GetCostByBillingCode(ctx context.Context, billingCode string) (float64, error)
}

// ─── Chat Sessions ───

// ChatSessionConfig holds extensible session metadata.
type ChatSessionConfig struct {
	Platform          string `json:"platform,omitempty"`
	PlatformUserID    string `json:"platform_user_id,omitempty"`
	PlatformChannelID string `json:"platform_channel_id,omitempty"`
}

// ChatSession represents a persistent chat session tied to an agent.
type ChatSession struct {
	ID        string            `json:"id"`
	AgentID   string            `json:"agent_id"`
	Name      string            `json:"name"`
	Config    ChatSessionConfig `json:"config"`
	CreatedAt string            `json:"created_at"`
	UpdatedAt string            `json:"updated_at"`
	CreatedBy string            `json:"created_by"`
	UpdatedBy string            `json:"updated_by"`
}

// ChatMessageData holds the extensible payload of a chat message.
type ChatMessageData struct {
	Content    any    `json:"content"`                // string or []ContentBlock
	ToolCalls  any    `json:"tool_calls,omitempty"`   // []ToolCall for assistant messages
	ToolCallID string `json:"tool_call_id,omitempty"` // for tool result messages
}

// ChatMessage represents a single message in a chat session.
type ChatMessage struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Role      string          `json:"role"` // "user", "assistant", "system", "tool"
	Data      ChatMessageData `json:"data"`
	CreatedAt string          `json:"created_at"`
}

// ChatSessionStorer defines CRUD operations for chat sessions and messages.
type ChatSessionStorer interface {
	ListChatSessions(ctx context.Context, q *query.Query) (*ListResult[ChatSession], error)
	GetChatSession(ctx context.Context, id string) (*ChatSession, error)
	GetChatSessionByPlatform(ctx context.Context, platform, platformUserID, platformChannelID string) (*ChatSession, error)
	CreateChatSession(ctx context.Context, session ChatSession) (*ChatSession, error)
	UpdateChatSession(ctx context.Context, id string, session ChatSession) (*ChatSession, error)
	DeleteChatSession(ctx context.Context, id string) error
	ListChatMessages(ctx context.Context, sessionID string) ([]ChatMessage, error)
	CreateChatMessage(ctx context.Context, msg ChatMessage) (*ChatMessage, error)
	CreateChatMessages(ctx context.Context, msgs []ChatMessage) error
	DeleteChatMessages(ctx context.Context, sessionID string) error
}

// ─── Bot Configs ───

// BotConfig represents a Discord or Telegram bot configuration stored in the database.
type BotConfig struct {
	ID              string            `json:"id"`
	Platform        string            `json:"platform"` // "discord" or "telegram"
	Name            string            `json:"name"`     // human-readable label
	Token           string            `json:"token"`    // bot token
	DefaultAgentID  string            `json:"default_agent_id"`
	ChannelAgents   map[string]string `json:"channel_agents,omitempty"`    // channel/chat ID -> agent ID overrides
	AllowedAgentIDs []string          `json:"allowed_agent_ids,omitempty"` // agent IDs users may /switch to; empty = switching disabled
	AccessMode      string            `json:"access_mode"`                 // "open" (default) or "allowlist"
	PendingApproval bool              `json:"pending_approval"`            // when true, unknown users in allowlist mode get "pending approval" reply
	AllowedUsers    []string          `json:"allowed_users"`
	PendingUsers    []string          `json:"pending_users"`
	Enabled         bool              `json:"enabled"`
	CreatedAt       string            `json:"created_at"`
	UpdatedAt       string            `json:"updated_at"`
	CreatedBy       string            `json:"created_by"`
	UpdatedBy       string            `json:"updated_by"`
}

// BotConfigStorer defines CRUD operations for bot configurations.
type BotConfigStorer interface {
	ListBotConfigs(ctx context.Context, q *query.Query) (*ListResult[BotConfig], error)
	GetBotConfig(ctx context.Context, id string) (*BotConfig, error)
	CreateBotConfig(ctx context.Context, bot BotConfig) (*BotConfig, error)
	UpdateBotConfig(ctx context.Context, id string, bot BotConfig) (*BotConfig, error)
	DeleteBotConfig(ctx context.Context, id string) error
}

// ─── User Preferences ───

// UserPreference stores a per-user key-value preference.
// Value is a JSON blob allowing structured data (e.g. {"timezone":"Europe/Istanbul","utc_offset":"+03:00"}).
// Secret preferences (like OAuth tokens) are encrypted at rest and excluded from system prompt injection.
type UserPreference struct {
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"` // platform-scoped user identity (e.g. "telegram::12345")
	Key       string          `json:"key"`     // preference key (e.g. "timezone", "location", "google_refresh_token")
	Value     json.RawMessage `json:"value"`   // JSON value (string, object, etc.)
	Secret    bool            `json:"secret"`  // true = encrypted at rest, excluded from system prompt
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

// UserPreferenceStorer defines operations for per-user preferences.
type UserPreferenceStorer interface {
	ListUserPreferences(ctx context.Context, userID string) ([]UserPreference, error)
	GetUserPreference(ctx context.Context, userID, key string) (*UserPreference, error)
	SetUserPreference(ctx context.Context, pref UserPreference) error // upsert by (user_id, key)
	DeleteUserPreference(ctx context.Context, userID, key string) error
}

// ─── Marketplace Sources ───

// MarketplaceSource represents a configurable skill marketplace source.
type MarketplaceSource struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // "generic"
	SearchURL string `json:"search_url"`
	TopURL    string `json:"top_url"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// MarketplaceSourceStorer defines CRUD operations for marketplace source configurations.
type MarketplaceSourceStorer interface {
	ListMarketplaceSources(ctx context.Context) ([]MarketplaceSource, error)
	GetMarketplaceSource(ctx context.Context, id string) (*MarketplaceSource, error)
	CreateMarketplaceSource(ctx context.Context, src MarketplaceSource) (*MarketplaceSource, error)
	UpdateMarketplaceSource(ctx context.Context, id string, src MarketplaceSource) (*MarketplaceSource, error)
	DeleteMarketplaceSource(ctx context.Context, id string) error
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

// ─── General MCP Servers ───

// MCPHTTPTool defines a custom HTTP-based tool exposed via an MCP server.
type MCPHTTPTool struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Method       string            `json:"method"`                  // GET, POST, PUT, DELETE, PATCH, HEAD
	URL          string            `json:"url"`                     // URL template with {{.param}} placeholders
	Headers      map[string]string `json:"headers,omitempty"`       // Static/template headers
	BodyTemplate string            `json:"body_template,omitempty"` // Body template with {{.param}} placeholders
	InputSchema  map[string]any    `json:"input_schema"`            // JSON schema for the tool inputs
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

	// MCPs — names of MCPs whose tools should be included.
	MCPs []string `json:"mcps,omitempty"`
}

// MCPUpstream represents an upstream MCP server — either HTTP or stdio (local command).
type MCPUpstream struct {
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// MCPServer represents a named, gateway-facing MCP endpoint that can expose
// RAG tools, custom HTTP tools, or both. External agents connect to
// /gateway/v1/mcp/{name} to use these tools.
type MCPServer struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"` // URL-safe slug used in the endpoint path
	Config    MCPServerConfig `json:"config"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	CreatedBy string          `json:"created_by"`
	UpdatedBy string          `json:"updated_by"`
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

// ─── MCP Sets ───

// MCPSet represents a named bundle of MCP server references, custom URLs,
// and its own tool configuration (RAG/HTTP/External/Skills).
// Agents reference MCP Sets by name instead of manually entering gateway URLs.
type MCPSet struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Config      MCPServerConfig `json:"config"`  // RAG/HTTP/External/Skills config (own tools)
	Servers     []string        `json:"servers"` // MCP Server names (resolved to gateway URLs at runtime)
	URLs        []string        `json:"urls"`    // Custom MCP endpoint URLs
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
