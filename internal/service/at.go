package service

import (
	"context"
	"fmt"
	"net/http"

	"github.com/rakunlabs/at/internal/config"
	"github.com/worldline-go/types"
)

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
	ListProviders(ctx context.Context) ([]ProviderRecord, error)
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

// APIToken represents a bearer token stored in the database for gateway auth.
type APIToken struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name"`
	TokenPrefix      string                 `json:"token_prefix"`      // first 8 chars for display (e.g. "at_xxxx…")
	AllowedProviders types.Slice[string]    `json:"allowed_providers"` // nil = all providers allowed
	AllowedModels    types.Slice[string]    `json:"allowed_models"`    // nil = all models allowed ("provider/model" format)
	AllowedWebhooks  types.Slice[string]    `json:"allowed_webhooks"`  // nil = all webhooks allowed (trigger IDs or aliases)
	ExpiresAt        types.Null[types.Time] `json:"expires_at"`        // zero value = no expiry
	CreatedAt        types.Time             `json:"created_at"`
	LastUsedAt       types.Null[types.Time] `json:"last_used_at"`
	CreatedBy        string                 `json:"created_by"`
	UpdatedBy        string                 `json:"updated_by"`
}

// APITokenStorer defines CRUD operations for API tokens.
type APITokenStorer interface {
	ListAPITokens(ctx context.Context) ([]APIToken, error)
	GetAPITokenByHash(ctx context.Context, hash string) (*APIToken, error)
	CreateAPIToken(ctx context.Context, token APIToken, tokenHash string) (*APIToken, error)
	UpdateAPIToken(ctx context.Context, id string, token APIToken) (*APIToken, error)
	DeleteAPIToken(ctx context.Context, id string) error
	UpdateLastUsed(ctx context.Context, id string) error
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
	Content      string
	InlineImages []InlineImage
	ToolCalls    []ToolCall
	Finished     bool
	Usage        Usage
	Header       http.Header
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
	ListWorkflows(ctx context.Context) ([]Workflow, error)
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
	ListSkills(ctx context.Context) ([]Skill, error)
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
	ListVariables(ctx context.Context) ([]Variable, error)
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
	ListNodeConfigs(ctx context.Context) ([]NodeConfig, error)
	ListNodeConfigsByType(ctx context.Context, configType string) ([]NodeConfig, error)
	GetNodeConfig(ctx context.Context, id string) (*NodeConfig, error)
	CreateNodeConfig(ctx context.Context, nc NodeConfig) (*NodeConfig, error)
	UpdateNodeConfig(ctx context.Context, id string, nc NodeConfig) (*NodeConfig, error)
	DeleteNodeConfig(ctx context.Context, id string) error
}

// Agent orchestrates MCP and LLM
type Agent struct {
	mcp      *HTTPMCPClient
	provider LLMProvider
	messages []Message

	Tools []Tool
}

func NewAgent(mcp *HTTPMCPClient, provider LLMProvider) *Agent {
	return &Agent{
		mcp:      mcp,
		provider: provider,
		messages: []Message{},
		Tools:    []Tool{},
	}
}

func (a *Agent) SetTools(ctx context.Context) error {
	tools, err := a.mcp.ListTools(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("\nAvailable tools: %d\n", len(tools))
	for _, tool := range tools {
		fmt.Printf("  - %s: %s\n", tool.Name, tool.Description)
	}

	a.Tools = tools

	return nil
}

func (a *Agent) Run(ctx context.Context, userMessage string) error {
	a.messages = append(a.messages, Message{
		Role:    "user",
		Content: userMessage,
	})

	for {
		resp, err := a.provider.Chat(ctx, "", a.messages, a.Tools)
		if err != nil {
			return err
		}

		if resp.Content != "" {
			fmt.Printf("\n🤖 Assistant: %s\n", resp.Content)
		}

		// Build assistant message content
		var assistantContent []ContentBlock
		if resp.Content != "" {
			assistantContent = append(assistantContent, ContentBlock{
				Type: "text",
				Text: resp.Content,
			})
		}
		for _, tc := range resp.ToolCalls {
			assistantContent = append(assistantContent, ContentBlock{
				Type:             "tool_use",
				ID:               tc.ID,
				Name:             tc.Name,
				Input:            tc.Arguments,
				ThoughtSignature: tc.ThoughtSignature,
			})
		}

		a.messages = append(a.messages, Message{
			Role:    "assistant",
			Content: assistantContent,
		})

		if resp.Finished {
			break
		}

		// Execute tool calls
		if len(resp.ToolCalls) > 0 {
			var toolResults []ContentBlock
			for _, tc := range resp.ToolCalls {
				fmt.Printf("\n🔧 [Tool Call: %s]\n", tc.Name)
				fmt.Printf("   Arguments: %v\n", tc.Arguments)

				result, err := a.mcp.CallTool(ctx, tc.Name, tc.Arguments)
				if err != nil {
					result = fmt.Sprintf("Error: %v", err)
				}
				fmt.Printf("   ✅ Result: %s\n", result)

				toolResults = append(toolResults, ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Name:      tc.Name,
					Content:   result,
				})
			}

			a.messages = append(a.messages, Message{
				Role:    "user",
				Content: toolResults,
			})
		}
	}

	return nil
}
