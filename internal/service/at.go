// Package service defines the core domain types and store interfaces for the AT platform.
//
// Types are organized across domain-specific files:
//
//   - types_llm.go     — LLM provider interfaces, message types, chat options
//   - types_media.go   — Image, audio, embedding provider interfaces and types
//   - types_provider.go — Provider record, storer, key rotation interfaces
//   - types_token.go   — API token and token usage types
//   - types_workflow.go — Workflow, trigger, and node config types
//   - types_agent.go   — Agent, heartbeat, runtime state, memory types
//   - types_org.go     — Organization, org-agent membership, goal, project types
//   - types_task.go    — Task, issue comment, label, approval types
//   - types_budget.go  — Agent budget, cost event, audit types
//   - types_chat.go    — Chat session, bot config, user preference, marketplace types
//   - types_rag.go     — RAG collection, state, page types
//   - types_mcp.go     — MCP server, MCP set types
package service

import (
	"context"

	"github.com/rakunlabs/query"
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

// Storer is the composite interface aggregating all domain store interfaces.
// Store backends (postgres, sqlite3, memory) implement this interface.
type Storer interface {
	ProviderStorer
	APITokenStorer
	TokenUsageStorer
	WorkflowStorer
	WorkflowVersionStorer
	TriggerStorer
	SkillStorer
	VariableStorer
	NodeConfigStorer
	AgentStorer
	ChatSessionStorer
	RAGCollectionStorer
	RAGStateStorer
	RAGPageStorer
	MCPServerStorer
	MCPSetStorer
	BotConfigStorer
	MarketplaceSourceStorer
	UserPreferenceStorer
	OrganizationStorer
	GoalStorer
	TaskStorer
	AgentBudgetStorer
	AuditStorer
	AgentHeartbeatStorer
	ProjectStorer
	IssueCommentStorer
	LabelStorer
	HeartbeatRunStorer
	WakeupRequestStorer
	AgentRuntimeStateStorer
	AgentTaskSessionStorer
	ApprovalStorer
	AgentConfigRevisionStorer
	CostEventStorer
	OrganizationAgentStorer
	AgentMemoryStorer
	PackSourceStorer
	GuideStorer
	ConnectionStorer
}

// ─── Skill Management ───

// Skill represents a reusable skill that bundles a system prompt fragment
// and a set of tools. Skills can be attached to agent_call workflow nodes
// to provide the agent with domain-specific capabilities.
type Skill struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Category     string   `json:"category,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	SystemPrompt string   `json:"system_prompt"` // Prompt fragment appended to the agent's system prompt
	Tools        []Tool   `json:"tools"`         // Built-in tool definitions (may include JS handlers)
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	CreatedBy    string   `json:"created_by"`
	UpdatedBy    string   `json:"updated_by"`
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

// ─── Pack Source Management ───

// PackSource represents a Git repository registered as a source of integration packs.
type PackSource struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Branch    string `json:"branch"`
	Status    string `json:"status"` // "pending", "synced", "error"
	LastSync  string `json:"last_sync,omitempty"`
	Error     string `json:"error,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// PackSourceStorer defines CRUD operations for pack sources.
type PackSourceStorer interface {
	ListPackSources(ctx context.Context, q *query.Query) (*ListResult[PackSource], error)
	GetPackSource(ctx context.Context, id string) (*PackSource, error)
	CreatePackSource(ctx context.Context, ps PackSource) (*PackSource, error)
	UpdatePackSource(ctx context.Context, id string, ps PackSource) (*PackSource, error)
	DeletePackSource(ctx context.Context, id string) error
}

// ─── Variable Management ───

// Variable represents a key-value variable stored in the database.
// Variables can be secret (encrypted at rest, redacted in list responses)
// or non-secret (stored as plaintext, shown in list responses).
// Accessed from workflow JS handlers via getVar() and bash handlers via $VAR_<KEY>.
type Variable struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description"`
	Secret      bool   `json:"secret"`
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

// ─── Guide Management ───

// Guide represents a user-authored documentation guide. Content is stored
// as raw markdown and rendered client-side. Built-in guides are hardcoded
// in the UI; user guides live in the database alongside them.
type Guide struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Icon        string `json:"icon"`    // lucide-svelte icon name (e.g. "BookOpen")
	Content     string `json:"content"` // raw markdown
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	CreatedBy   string `json:"created_by"`
	UpdatedBy   string `json:"updated_by"`
}

// GuideStorer defines CRUD operations for user-authored guides.
type GuideStorer interface {
	ListGuides(ctx context.Context, q *query.Query) (*ListResult[Guide], error)
	GetGuide(ctx context.Context, id string) (*Guide, error)
	CreateGuide(ctx context.Context, g Guide) (*Guide, error)
	UpdateGuide(ctx context.Context, id string, g Guide) (*Guide, error)
	DeleteGuide(ctx context.Context, id string) error
}
